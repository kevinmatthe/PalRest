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

type ObservationResult struct {
	UserID        string
	PlayerName    string
	PolicyEnabled bool
	Exempt        bool
	Continuous    bool
	Accounting    bool
	SkipReason    string
	Gap           time.Duration
	MaxGap        time.Duration
	Added         time.Duration
	Used          time.Duration
	Remaining     time.Duration
	Limit         time.Duration
	Period        domain.Period
	Generation    int64
}

type Decisions struct {
	Observations []ObservationResult
	Warnings     []WarningDecision
	Kicks        []KickDecision
}

type continuityObservation struct {
	at         time.Time
	accounting bool
	generation int64
}

type Repository interface {
	WithTx(context.Context, func(*store.Tx) error) error
	Players(context.Context) ([]domain.Player, error)
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
	observed          map[string]continuityObservation
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
		observed:          make(map[string]continuityObservation),
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
			if err := s.refreshStrategyState(tx, player.UserID, rule, now); err != nil {
				return err
			}
			previous, continuous := s.observed[player.UserID]
			if !continuous {
				s.generations[player.UserID]++
			}
			generation := s.generations[player.UserID]
			result := ObservationResult{
				UserID: player.UserID, PlayerName: player.Name, PolicyEnabled: rule.Enabled, Exempt: rule.Exempt,
				Continuous: continuous, Accounting: previous.accounting && rule.Enabled, MaxGap: s.maxGap,
				Limit: rule.Limit, Generation: generation,
			}
			if continuous && previous.accounting && rule.Enabled {
				gap := now.Sub(previous.at)
				result.Gap = gap
				if gap >= 0 && gap <= s.maxGap {
					added, err := s.addInterval(tx, player.UserID, rule, previous.at, now)
					if err != nil {
						return err
					}
					result.Added = added
				} else {
					result.SkipReason = "gap_exceeded"
				}
			} else if !rule.Enabled {
				result.SkipReason = "policy_disabled"
			} else if !continuous {
				result.SkipReason = "first_observation"
			} else if !previous.accounting {
				result.SkipReason = "previous_not_accounting"
			}
			s.observed[player.UserID] = continuityObservation{at: now, accounting: rule.Enabled, generation: generation}
			period, used, remaining, err := s.currentUsage(tx, player.UserID, rule, now)
			if err != nil {
				return err
			}
			result.Period = period
			result.Used = used
			result.Remaining = remaining
			decisions.Observations = append(decisions.Observations, result)
			overLimit, resetAt, err := s.enforcementState(tx, player.UserID, rule, period, now, used)
			if err != nil {
				return err
			}
			if rule.Enabled && overLimit {
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
					decisions.Kicks = append(decisions.Kicks, KickDecision{UserID: player.UserID, PlayerName: player.Name, Period: period, ResetAt: resetAt, Generation: generation, PolicyRevision: rule.Revision})
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
	s.observed = make(map[string]continuityObservation)
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

func (s *Service) addInterval(tx *store.Tx, userID string, rule domain.ResolvedPolicy, start, end time.Time) (time.Duration, error) {
	switch rule.Strategy {
	case "cooldown":
		return s.addCooldownInterval(tx, userID, rule, start, end)
	case "credit":
		return s.addCreditInterval(tx, userID, rule, start, end)
	default:
		return s.addFixedWindowInterval(tx, userID, rule, start, end)
	}
}

func (s *Service) addFixedWindowInterval(tx *store.Tx, userID string, rule domain.ResolvedPolicy, start, end time.Time) (time.Duration, error) {
	var added time.Duration
	for cursor := start; cursor.Before(end); {
		period := s.policies.Period(rule, cursor)
		segmentEnd := end
		if period.End.Before(segmentEnd) {
			segmentEnd = period.End
		}
		if !segmentEnd.After(cursor) {
			return 0, fmt.Errorf("non-advancing period boundary at %s", cursor)
		}
		if _, err := tx.AddUsage(userID, period, segmentEnd.Sub(cursor), end); err != nil {
			return 0, err
		}
		added += segmentEnd.Sub(cursor)
		cursor = segmentEnd
	}
	return added, nil
}

func (s *Service) addCooldownInterval(tx *store.Tx, userID string, rule domain.ResolvedPolicy, start, end time.Time) (time.Duration, error) {
	state, err := s.policyState(tx, userID, rule, end)
	if err != nil {
		return 0, err
	}
	if !state.CooldownUntil.IsZero() && end.Before(state.CooldownUntil) {
		state.UpdatedAt = end
		return 0, tx.UpsertPolicyState(state)
	}
	if !state.CooldownUntil.IsZero() && !end.Before(state.CooldownUntil) {
		if start.Before(state.CooldownUntil) {
			start = state.CooldownUntil
		}
		state.WindowStart = start
		state.Used = 0
		state.CooldownUntil = time.Time{}
	}
	if state.WindowStart.IsZero() {
		state.WindowStart = start
	}
	delta := end.Sub(start)
	accounted := delta
	if remaining := rule.CooldownEvery - state.Used; remaining > 0 && delta >= remaining {
		accounted = remaining
		state.CooldownUntil = start.Add(remaining).Add(rule.CooldownRest)
	}
	state.Used += accounted
	if state.Used >= rule.CooldownEvery {
		state.Used = rule.CooldownEvery
		if state.CooldownUntil.IsZero() {
			state.CooldownUntil = end.Add(rule.CooldownRest)
		}
	}
	state.UpdatedAt = end
	if err := tx.UpsertPolicyState(state); err != nil {
		return 0, err
	}
	period := s.strategyPeriod(rule, state.WindowStart, state.CooldownUntil, end)
	if _, err := tx.AddUsage(userID, period, accounted, end); err != nil {
		return 0, err
	}
	return accounted, nil
}

func (s *Service) addCreditInterval(tx *store.Tx, userID string, rule domain.ResolvedPolicy, start, end time.Time) (time.Duration, error) {
	state, err := s.policyState(tx, userID, rule, start)
	if err != nil {
		return 0, err
	}
	state = accrueCredit(state, rule, start)
	delta := end.Sub(start)
	if delta < 0 {
		return 0, fmt.Errorf("credit usage delta cannot be negative")
	}
	if delta >= state.Credit {
		state.Credit = 0
	} else {
		state.Credit -= delta
	}
	state.LastCreditAt = end
	state.UpdatedAt = end
	if err := tx.UpsertPolicyState(state); err != nil {
		return 0, err
	}
	period := s.strategyPeriod(rule, state.WindowStart, time.Time{}, end)
	if _, err := tx.AddUsage(userID, period, delta, end); err != nil {
		return 0, err
	}
	return delta, nil
}

func (s *Service) refreshStrategyState(tx *store.Tx, userID string, rule domain.ResolvedPolicy, now time.Time) error {
	if rule.Strategy != "credit" && rule.Strategy != "cooldown" {
		return nil
	}
	state, err := s.policyState(tx, userID, rule, now)
	if err != nil {
		return err
	}
	if rule.Strategy == "credit" {
		state = accrueCredit(state, rule, now)
	}
	state.UpdatedAt = now
	return tx.UpsertPolicyState(state)
}

func (s *Service) policyState(tx *store.Tx, userID string, rule domain.ResolvedPolicy, now time.Time) (store.PolicyState, error) {
	state, err := tx.PolicyState(userID, rule.Revision)
	if err == nil {
		return state, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.PolicyState{}, err
	}
	state = store.PolicyState{
		UserID: userID, PolicyRevision: rule.Revision, Strategy: rule.Strategy,
		WindowStart: now, UpdatedAt: now,
	}
	if rule.Strategy == "credit" {
		state.Credit = rule.CreditMax
		state.LastCreditAt = now
	}
	return state, nil
}

func (s *Service) currentUsage(tx *store.Tx, userID string, rule domain.ResolvedPolicy, now time.Time) (domain.Period, time.Duration, time.Duration, error) {
	switch rule.Strategy {
	case "cooldown":
		state, err := s.policyState(tx, userID, rule, now)
		if err != nil {
			return domain.Period{}, 0, 0, err
		}
		period := s.strategyPeriod(rule, state.WindowStart, state.CooldownUntil, now)
		remaining := rule.CooldownEvery - state.Used
		if !state.CooldownUntil.IsZero() && now.Before(state.CooldownUntil) {
			remaining = 0
		}
		if remaining < 0 {
			remaining = 0
		}
		return period, state.Used, remaining, nil
	case "credit":
		state, err := s.policyState(tx, userID, rule, now)
		if err != nil {
			return domain.Period{}, 0, 0, err
		}
		state = accrueCredit(state, rule, now)
		period := s.strategyPeriod(rule, state.WindowStart, time.Time{}, now)
		used := rule.CreditMax - state.Credit
		if used < 0 {
			used = 0
		}
		return period, used, state.Credit, nil
	default:
		period := s.policies.Period(rule, now)
		used, err := tx.Usage(userID, period.Key)
		if errors.Is(err, store.ErrNotFound) {
			used = 0
		} else if err != nil {
			return domain.Period{}, 0, 0, err
		}
		remaining := rule.Limit - used
		if remaining < 0 {
			remaining = 0
		}
		return period, used, remaining, nil
	}
}

func accrueCredit(state store.PolicyState, rule domain.ResolvedPolicy, now time.Time) store.PolicyState {
	if state.LastCreditAt.IsZero() || !now.After(state.LastCreditAt) || rule.CreditRecoverEvery <= 0 {
		return state
	}
	elapsed := now.Sub(state.LastCreditAt)
	recovered := proportionalDuration(elapsed, rule.CreditRecoverAmount, rule.CreditRecoverEvery)
	if recovered <= 0 {
		return state
	}
	state.Credit += recovered
	if state.Credit > rule.CreditMax {
		state.Credit = rule.CreditMax
	}
	state.LastCreditAt = now
	return state
}

func (s *Service) enforcementState(tx *store.Tx, userID string, rule domain.ResolvedPolicy, period domain.Period, now time.Time, fixedUsed time.Duration) (bool, time.Time, error) {
	switch rule.Strategy {
	case "cooldown":
		state, err := s.policyState(tx, userID, rule, now)
		if err != nil {
			return false, time.Time{}, err
		}
		if !state.CooldownUntil.IsZero() && now.Before(state.CooldownUntil) {
			return true, state.CooldownUntil, nil
		}
		return state.Used >= rule.CooldownEvery, now.Add(rule.CooldownRest), nil
	case "credit":
		state, err := s.policyState(tx, userID, rule, now)
		if err != nil {
			return false, time.Time{}, err
		}
		return state.Credit <= 0, creditResetAt(rule, now), nil
	default:
		return fixedUsed >= rule.Limit, period.End, nil
	}
}

func (s *Service) strategyPeriod(rule domain.ResolvedPolicy, start, end, now time.Time) domain.Period {
	if start.IsZero() {
		start = now
	}
	if end.IsZero() {
		end = start.Add(365 * 24 * time.Hour)
	}
	return domain.Period{Key: rule.Strategy + "|" + rule.Revision, Start: start.UTC(), End: end.UTC()}
}

func creditResetAt(rule domain.ResolvedPolicy, now time.Time) time.Time {
	if rule.CreditRecoverAmount <= 0 {
		return now
	}
	needed := rule.CreditMax
	return now.Add(proportionalDuration(needed, rule.CreditRecoverEvery, rule.CreditRecoverAmount))
}

func proportionalDuration(value, numerator, denominator time.Duration) time.Duration {
	if denominator <= 0 {
		return 0
	}
	valueMS := value.Milliseconds()
	numeratorMS := numerator.Milliseconds()
	denominatorMS := denominator.Milliseconds()
	if valueMS <= 0 || numeratorMS <= 0 || denominatorMS <= 0 {
		return 0
	}
	return time.Duration(valueMS*numeratorMS/denominatorMS) * time.Millisecond
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

func (s *Service) AllSnapshots(ctx context.Context) ([]domain.PlayerSnapshot, error) {
	players, err := s.repo.Players(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.PlayerSnapshot, 0, len(players))
	for _, player := range players {
		snapshot, err := s.Snapshot(ctx, player.UserID)
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
	if err := s.repo.WithTx(ctx, func(tx *store.Tx) error {
		period, used, remaining, err := s.currentUsage(tx, userID, rule, now)
		if err != nil {
			return err
		}
		snapshot.Period = period
		snapshot.Used = used
		snapshot.Remaining = remaining
		return nil
	}); err != nil {
		return domain.PlayerSnapshot{}, err
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
