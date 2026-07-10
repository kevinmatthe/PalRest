package analytics

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

const analyticsBucketSize = 5 * time.Minute

type Recorder interface {
	RecordAnalyticsObservation(context.Context, store.AnalyticsObservation) error
	CleanupAnalytics(context.Context, time.Time, string, int) error
}

type Service struct {
	mu          sync.Mutex
	repo        Recorder
	maxGap      time.Duration
	location    *time.Location
	lastAt      time.Time // interval-continuity baseline; may reset on timezone change
	asOf        time.Time // latest successfully persisted observation
	online      map[string]domain.Player
	lastCleanup time.Time
}

func New(repo Recorder, maxGap time.Duration, location *time.Location) *Service {
	if repo == nil {
		panic("analytics: nil recorder")
	}
	if maxGap <= 0 {
		panic("analytics: max gap must be positive")
	}
	if location == nil {
		panic("analytics: nil location")
	}
	return &Service{
		repo:     repo,
		maxGap:   maxGap,
		location: location,
		online:   make(map[string]domain.Player),
	}
}

func (s *Service) Observe(ctx context.Context, at time.Time, players []domain.Player) error {
	if at.IsZero() {
		return fmt.Errorf("observe analytics: observation time is zero")
	}
	at = at.UTC()
	current, orderedPlayers, err := normalizePlayers(players)
	if err != nil {
		return fmt.Errorf("observe analytics: %w", err)
	}

	s.mu.Lock()
	if !s.asOf.IsZero() && !at.After(s.asOf) {
		s.mu.Unlock()
		return fmt.Errorf("observe analytics: observation time %s must be after %s", at.Format(time.RFC3339Nano), s.asOf.Format(time.RFC3339Nano))
	}
	if !s.asOf.IsZero() && at.Sub(s.asOf).Milliseconds() <= 0 {
		s.mu.Unlock()
		return fmt.Errorf("observe analytics: observation must advance by at least 1ms")
	}

	joined := differenceIDs(current, s.online)
	left := differenceIDs(s.online, current)
	var intervals []store.AnalyticsInterval
	if !s.lastAt.IsZero() {
		gap := at.Sub(s.lastAt)
		if gap <= s.maxGap {
			intervals = s.splitIntervals(s.lastAt, at, sortedPlayerIDs(s.online))
		}
	}
	observation := store.AnalyticsObservation{
		At:            at,
		LocalDate:     at.In(s.location).Format(time.DateOnly),
		Players:       orderedPlayers,
		JoinedUserIDs: joined,
		LeftUserIDs:   left,
		Intervals:     intervals,
	}
	if err := s.repo.RecordAnalyticsObservation(ctx, observation); err != nil {
		s.mu.Unlock()
		return err
	}
	s.lastAt = at
	s.asOf = at
	s.online = current
	doCleanup := s.lastCleanup.IsZero() || at.Sub(s.lastCleanup) >= 24*time.Hour
	cleanupDate := ""
	if doCleanup {
		s.lastCleanup = at
		cleanupDate = at.In(s.location).AddDate(0, 0, -90).Format(time.DateOnly)
	}
	s.mu.Unlock()
	if doCleanup {
		if err := s.repo.CleanupAnalytics(ctx, at.AddDate(0, 0, -90), cleanupDate, 500); err != nil {
			slog.Warn("cleanup analytics failed", "error", err)
		}
	}
	return nil
}

// SetLocation changes the calendar timezone used by future observations.
// Existing daily rows retain the timezone in which they were recorded. A
// changed zone deliberately clears the observation baseline while retaining
// online players: the single interval crossing the transition is omitted
// rather than retroactively assigning it to either calendar timezone.
func (s *Service) SetLocation(location *time.Location) error {
	if location == nil {
		return fmt.Errorf("set analytics location: location is nil")
	}
	s.mu.Lock()
	if s.location.String() == location.String() {
		s.mu.Unlock()
		return nil
	}
	s.location = location
	s.lastAt = time.Time{}
	s.mu.Unlock()
	return nil
}

// Restore establishes the in-memory baseline for sessions recovered from the
// repository without writing a new observation.
func (s *Service) Restore(at time.Time, players []domain.Player) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.asOf.IsZero() || !s.lastAt.IsZero() || len(s.online) != 0 {
		return fmt.Errorf("restore analytics: service is already initialized")
	}
	if at.IsZero() {
		if len(players) == 0 {
			return nil
		}
		return fmt.Errorf("restore analytics: baseline time is zero")
	}
	current, _, err := normalizePlayers(players)
	if err != nil {
		return fmt.Errorf("restore analytics: %w", err)
	}
	s.lastAt = at.UTC()
	s.asOf = at.UTC()
	s.online = current
	return nil
}

func (s *Service) Current() ([]string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.online) == 0 {
		return nil, s.asOf
	}
	return sortedPlayerIDs(s.online), s.asOf
}

func normalizePlayers(players []domain.Player) (map[string]domain.Player, []domain.Player, error) {
	current := make(map[string]domain.Player, len(players))
	ordered := append([]domain.Player(nil), players...)
	for _, player := range ordered {
		if player.UserID == "" {
			return nil, nil, fmt.Errorf("player user ID cannot be empty")
		}
		if _, exists := current[player.UserID]; exists {
			return nil, nil, fmt.Errorf("player user ID %q is duplicated", player.UserID)
		}
		current[player.UserID] = player
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].UserID < ordered[j].UserID })
	return current, ordered, nil
}

func differenceIDs(a, b map[string]domain.Player) []string {
	ids := make([]string, 0)
	for id := range a {
		if _, exists := b[id]; !exists {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func sortedPlayerIDs(players map[string]domain.Player) []string {
	ids := make([]string, 0, len(players))
	for id := range players {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Service) splitIntervals(start, end time.Time, onlineUserIDs []string) []store.AnalyticsInterval {
	intervals := make([]store.AnalyticsInterval, 0, 1)
	for cursor := start; cursor.Before(end); {
		next := end
		bucketBoundary := cursor.Truncate(analyticsBucketSize).Add(analyticsBucketSize)
		if bucketBoundary.Before(next) {
			next = bucketBoundary
		}
		midnight := nextLocalMidnight(cursor, s.location)
		if midnight.After(cursor) && midnight.Before(next) {
			next = midnight
		}
		if next.Sub(cursor).Milliseconds() > 0 {
			ids := append([]string(nil), onlineUserIDs...)
			if ids == nil {
				ids = []string{}
			}
			intervals = append(intervals, store.AnalyticsInterval{
				Start: cursor, End: next, OnlineUserIDs: ids,
				LocalDate: cursor.In(s.location).Format(time.DateOnly),
			})
		}
		cursor = next
	}
	return intervals
}

func nextLocalMidnight(at time.Time, location *time.Location) time.Time {
	local := at.In(location)
	return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, location).UTC()
}
