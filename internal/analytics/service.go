package analytics

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

const analyticsBucketSize = 5 * time.Minute

type Recorder interface {
	RecordAnalyticsObservation(context.Context, store.AnalyticsObservation) error
}

type Service struct {
	mu       sync.Mutex
	repo     Recorder
	maxGap   time.Duration
	location *time.Location
	lastAt   time.Time
	online   map[string]domain.Player
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
	defer s.mu.Unlock()
	if !s.lastAt.IsZero() && !at.After(s.lastAt) {
		return fmt.Errorf("observe analytics: observation time %s must be after %s", at.Format(time.RFC3339Nano), s.lastAt.Format(time.RFC3339Nano))
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
		return err
	}
	s.lastAt = at
	s.online = current
	return nil
}

func (s *Service) Current() ([]string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastAt.IsZero() {
		return nil, time.Time{}
	}
	return sortedPlayerIDs(s.online), s.lastAt
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
		ids := append([]string(nil), onlineUserIDs...)
		if ids == nil {
			ids = []string{}
		}
		intervals = append(intervals, store.AnalyticsInterval{
			Start: cursor, End: next, OnlineUserIDs: ids,
			LocalDate: cursor.In(s.location).Format(time.DateOnly),
		})
		cursor = next
	}
	return intervals
}

func nextLocalMidnight(at time.Time, location *time.Location) time.Time {
	local := at.In(location)
	return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, location).UTC()
}
