package guard

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/policy"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type WarningDecision struct {
	UserID     string
	PlayerName string
	Period     domain.Period
	Threshold  time.Duration
	Remaining  time.Duration
}

type KickDecision struct {
	UserID         string
	PlayerName     string
	Period         domain.Period
	ResetAt        time.Time
	Generation     int64
	PolicyRevision string
}

type Decisions struct {
	Warnings []WarningDecision
	Kicks    []KickDecision
}

type observation struct {
	at         time.Time
	accounting bool
	generation int64
}

type Repository interface {
	WithTx(context.Context, func(*store.Tx) error) error
	Player(context.Context, string) (domain.Player, error)
	Usage(context.Context, string, string) (domain.Usage, error)
	WarningEvents(context.Context, string, string) ([]store.WarningEvent, error)
	EnforcementEventsForPolicy(context.Context, string, string, string) ([]store.EnforcementEvent, error)
}

type Service struct {
	mu                sync.Mutex
	repo              Repository
	policies          *policy.Service
	maxGap            time.Duration
	retryInitial      time.Duration
	retryMax          time.Duration
	observed          map[string]observation
	generations       map[string]int64
	kickedConnections map[string]bool
	snapshots         map[string]domain.PlayerSnapshot
	now               func() time.Time
}

func New(repo Repository, policies *policy.Service, maxGap, retryInitial, retryMax time.Duration) *Service {
	return &Service{
		repo:              repo,
		policies:          policies,
		maxGap:            maxGap,
		retryInitial:      retryInitial,
		retryMax:          retryMax,
		observed:          make(map[string]observation),
		generations:       make(map[string]int64),
		kickedConnections: make(map[string]bool),
		snapshots:         make(map[string]domain.PlayerSnapshot),
		now:               time.Now,
	}
}

func (s *Service) Observe(ctx context.Context, now time.Time, players []domain.Player) (Decisions, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now = now.UTC()
	online := make(map[string]bool, len(players))
	for _, player := range players {
		online[player.UserID] = true
	}
	for userID := range s.observed {
		if !online[userID] {
			delete(s.observed, userID)
			if snapshot, ok := s.snapshots[userID]; ok {
				snapshot.Online = false
				s.snapshots[userID] = snapshot
			}
		}
	}

	var decisions Decisions
	err := s.repo.WithTx(ctx, func(tx *store.Tx) error {
		for _, player := range players {
			if player.UserID == "" {
				return fmt.Errorf("online player has empty user ID")
			}
			player.LastOnline = now
			if err := tx.UpsertPlayer(player, now); err != nil {
				return err
			}
			rule := s.policies.Resolve(player.UserID)
			period := s.policies.Period(rule, now)
			previous, continuous := s.observed[player.UserID]
			if !continuous {
				s.generations[player.UserID]++
			}
			generation := s.generations[player.UserID]
			if continuous && previous.accounting && rule.Enabled {
				gap := now.Sub(previous.at)
				if gap >= 0 && gap <= s.maxGap {
					if err := s.addInterval(tx, player.UserID, rule, previous.at, now); err != nil {
						return err
					}
				}
			}
			s.observed[player.UserID] = observation{at: now, accounting: rule.Enabled, generation: generation}
			used, err := tx.Usage(player.UserID, period.Key)
			if errors.Is(err, store.ErrNotFound) {
				used = 0
			} else if err != nil {
				return err
			}
			remaining := rule.Limit - used
			if remaining < 0 {
				remaining = 0
			}
			if rule.Enabled && used >= rule.Limit {
				key := retryKey(player.UserID, period.Key, rule.Revision, generation)
				retry, err := tx.EnforcementRetry(player.UserID, period.Key, rule.Revision)
				if err != nil {
					return err
				}
				retryReady := true
				if retry.Attempts > 0 {
					retryReady = !now.Before(retry.LastAttempt.Add(s.retryDelay(retry.Attempts)))
				}
				if !s.kickedConnections[key] && retryReady {
					decisions.Kicks = append(decisions.Kicks, KickDecision{UserID: player.UserID, PlayerName: player.Name, Period: period, ResetAt: period.End, Generation: generation, PolicyRevision: rule.Revision})
				}
			} else if rule.Enabled {
				for i := len(rule.WarningBefore) - 1; i >= 0; i-- {
					threshold := rule.WarningBefore[i]
					if remaining <= threshold {
						created, err := tx.EnsureWarning(player.UserID, period.Key, threshold, now)
						if err != nil {
							return err
						}
						shouldSend := created
						if !created {
							event, err := tx.Warning(player.UserID, period.Key, threshold)
							if err != nil {
								return err
							}
							shouldSend = event.Status == "failure" && !now.Before(event.NextAttempt)
							if event.Status == "pending" {
								shouldSend = !now.Before(event.UpdatedAt.Add(s.retryInitial))
							}
						}
						if shouldSend {
							decisions.Warnings = append(decisions.Warnings, WarningDecision{UserID: player.UserID, PlayerName: player.Name, Period: period, Threshold: threshold, Remaining: remaining})
						}
						break
					}
				}
			}
			s.snapshots[player.UserID] = domain.PlayerSnapshot{Player: player, Policy: rule, Period: period, Used: used, Remaining: remaining, Online: true}
		}
		return nil
	})
	if err != nil {
		s.clearContinuity()
		return Decisions{}, err
	}
	return decisions, nil
}

func (s *Service) clearContinuity() {
	s.observed = make(map[string]observation)
	for userID, snapshot := range s.snapshots {
		snapshot.Online = false
		s.snapshots[userID] = snapshot
	}
}

func (s *Service) RecordWarningResult(ctx context.Context, decision WarningDecision, resultErr error, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repo.WithTx(ctx, func(tx *store.Tx) error {
		event, err := tx.Warning(decision.UserID, decision.Period.Key, decision.Threshold)
		if err != nil {
			return err
		}
		status := "success"
		errorSummary := ""
		var next time.Time
		if resultErr != nil {
			status = "failure"
			errorSummary = sanitizeError(resultErr)
			delay := s.retryInitial
			for i := 0; i < event.Attempts && delay < s.retryMax; i++ {
				delay *= 2
				if delay > s.retryMax {
					delay = s.retryMax
				}
			}
			next = now.Add(delay)
		}
		return tx.UpdateWarningResult(decision.UserID, decision.Period.Key, decision.Threshold, status, errorSummary, next, now)
	})
}

func (s *Service) addInterval(tx *store.Tx, userID string, rule domain.ResolvedPolicy, start, end time.Time) error {
	for cursor := start; cursor.Before(end); {
		period := s.policies.Period(rule, cursor)
		segmentEnd := end
		if period.End.Before(segmentEnd) {
			segmentEnd = period.End
		}
		if !segmentEnd.After(cursor) {
			return fmt.Errorf("non-advancing period boundary at %s", cursor)
		}
		if _, err := tx.AddUsage(userID, period, segmentEnd.Sub(cursor), end); err != nil {
			return err
		}
		cursor = segmentEnd
	}
	return nil
}

func (s *Service) PollFailed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearContinuity()
}

func (s *Service) RecordKickResult(ctx context.Context, decision KickDecision, resultErr error, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := "success"
	errorSummary := ""
	key := retryKey(decision.UserID, decision.Period.Key, decision.PolicyRevision, decision.Generation)
	if resultErr != nil {
		result = "failure"
		errorSummary = sanitizeError(resultErr)
	}
	err := s.repo.WithTx(ctx, func(tx *store.Tx) error {
		return tx.AppendEnforcement(store.EnforcementEvent{
			UserID: decision.UserID, PeriodKey: decision.Period.Key, Action: "kick", Result: result,
			PolicyRevision: decision.PolicyRevision, Generation: decision.Generation, ErrorSummary: errorSummary, CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	if resultErr == nil {
		s.kickedConnections[key] = true
	}
	return nil
}

func (s *Service) retryDelay(attempts int) time.Duration {
	delay := s.retryInitial
	for i := 1; i < attempts && delay < s.retryMax; i++ {
		delay *= 2
		if delay > s.retryMax {
			delay = s.retryMax
		}
	}
	return delay
}

func (s *Service) Snapshots() []domain.PlayerSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]domain.PlayerSnapshot, 0, len(s.snapshots))
	for _, snapshot := range s.snapshots {
		result = append(result, snapshot)
	}
	return result
}

func (s *Service) OnlineSnapshots(ctx context.Context) ([]domain.PlayerSnapshot, error) {
	s.mu.Lock()
	userIDs := make([]string, 0, len(s.snapshots))
	for userID, snapshot := range s.snapshots {
		if snapshot.Online {
			userIDs = append(userIDs, userID)
		}
	}
	s.mu.Unlock()
	result := make([]domain.PlayerSnapshot, 0, len(userIDs))
	for _, userID := range userIDs {
		snapshot, err := s.Snapshot(ctx, userID)
		if err != nil {
			return nil, err
		}
		result = append(result, snapshot)
	}
	return result, nil
}

func (s *Service) Snapshot(ctx context.Context, userID string) (domain.PlayerSnapshot, error) {
	s.mu.Lock()
	snapshot, present := s.snapshots[userID]
	now := s.now().UTC()
	s.mu.Unlock()
	if !present {
		player, err := s.repo.Player(ctx, userID)
		if err != nil {
			return domain.PlayerSnapshot{}, err
		}
		snapshot = domain.PlayerSnapshot{Player: player, Online: false}
	}
	rule := s.policies.Resolve(userID)
	snapshot.Policy = rule
	snapshot.Period = s.policies.Period(rule, now)
	usage, err := s.repo.Usage(ctx, userID, snapshot.Period.Key)
	if errors.Is(err, store.ErrNotFound) {
		snapshot.Used = 0
	} else if err != nil {
		return domain.PlayerSnapshot{}, err
	} else {
		snapshot.Used = usage.Used
	}
	snapshot.Remaining = snapshot.Policy.Limit - snapshot.Used
	if snapshot.Remaining < 0 {
		snapshot.Remaining = 0
	}
	warnings, err := s.repo.WarningEvents(ctx, userID, snapshot.Period.Key)
	if err != nil {
		return domain.PlayerSnapshot{}, err
	}
	snapshot.Warnings = make([]domain.WarningState, 0, len(warnings))
	for _, warning := range warnings {
		snapshot.Warnings = append(snapshot.Warnings, domain.WarningState{
			Threshold: warning.Threshold, Status: warning.Status, Attempts: warning.Attempts, NextAttempt: warning.NextAttempt,
		})
	}
	events, err := s.repo.EnforcementEventsForPolicy(ctx, userID, snapshot.Period.Key, rule.Revision)
	if err != nil {
		return domain.PlayerSnapshot{}, err
	}
	if len(events) > 0 {
		latest := events[len(events)-1]
		attempts := 0
		for i := len(events) - 1; i >= 0 && events[i].Result == "failure"; i-- {
			attempts++
		}
		snapshot.Enforcement = domain.EnforcementState{Status: latest.Result, Attempts: attempts, Generation: latest.Generation}
	}
	return snapshot, nil
}

func retryKey(userID, periodKey, policyRevision string, generation int64) string {
	return fmt.Sprintf("%s|%s|%s|%d", userID, periodKey, policyRevision, generation)
}

func sanitizeError(err error) string {
	const max = 500
	message := err.Error()
	if len(message) > max {
		message = message[:max]
	}
	return message
}
