package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
			previous, continuous := s.observed[player.UserID]
			if err := s.refreshStrategyState(tx, player.UserID, rule, now, !continuous); err != nil {
				return err
			}
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
			period, used, remaining, err := s.currentUsage(tx, player.UserID, rule, now, false)
			if err != nil {
				return err
			}
			result.Period = period
			result.Used = used
			result.Remaining = remaining
			lastCreditRecovered, err := s.lastCreditRecovery(tx, player.UserID, rule)
			if err != nil {
				return err
			}
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
			s.snapshots[player.UserID] = domain.PlayerSnapshot{Player: player, Policy: rule, Period: period, Used: used, Remaining: remaining, LastCreditRecovered: lastCreditRecovered, Online: true}
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
		ref := guardEventRef("warning", decision.UserID, decision.Period.Key, fmt.Sprintf("%d", decision.Threshold.Milliseconds()), now)
		attempted, err := appendGuardActivity(tx, ref, "01", "guard_warning_attempted", decision.UserID, now, map[string]any{
			"action": "warning", "player_name": decision.PlayerName,
			"threshold_ms": decision.Threshold.Milliseconds(),
		})
		if err != nil || !attempted {
			return err
		}
		if err := tx.UpdateWarningResult(decision.UserID, decision.Period.Key, decision.Threshold, status, errorSummary, next, now); err != nil {
			return err
		}
		resultType := "guard_warning_delivered"
		payload := map[string]any{
			"action": "warning", "outcome": status,
			"player_name": decision.PlayerName, "threshold_ms": decision.Threshold.Milliseconds(),
		}
		if resultErr != nil {
			resultType = "guard_warning_failed"
			payload["error_code"] = "delivery_failed"
		}
		_, err = appendGuardActivity(tx, ref, "02", resultType, decision.UserID, now, payload)
		return err
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

func (s *Service) refreshStrategyState(tx *store.Tx, userID string, rule domain.ResolvedPolicy, now time.Time, recoverCredit bool) error {
	if rule.Strategy != "credit" && rule.Strategy != "cooldown" {
		return nil
	}
	state, err := s.policyState(tx, userID, rule, now)
	if err != nil {
		return err
	}
	if rule.Strategy == "credit" && recoverCredit {
		state, state.LastCreditRecovered = accrueCredit(state, rule, now)
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

func (s *Service) currentUsage(tx *store.Tx, userID string, rule domain.ResolvedPolicy, now time.Time, recoverCredit bool) (domain.Period, time.Duration, time.Duration, error) {
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
		if recoverCredit {
			state, _ = accrueCredit(state, rule, now)
		}
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

func (s *Service) lastCreditRecovery(tx *store.Tx, userID string, rule domain.ResolvedPolicy) (time.Duration, error) {
	if rule.Strategy != "credit" {
		return 0, nil
	}
	state, err := tx.PolicyState(userID, rule.Revision)
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return state.LastCreditRecovered, nil
}

func accrueCredit(state store.PolicyState, rule domain.ResolvedPolicy, now time.Time) (store.PolicyState, time.Duration) {
	if state.LastCreditAt.IsZero() || !now.After(state.LastCreditAt) || rule.CreditRecoverEvery <= 0 {
		return state, 0
	}
	elapsed := now.Sub(state.LastCreditAt)
	recovered := proportionalDuration(elapsed, rule.CreditRecoverAmount, rule.CreditRecoverEvery)
	if recovered <= 0 {
		return state, 0
	}
	before := state.Credit
	state.Credit += recovered
	if state.Credit > rule.CreditMax {
		state.Credit = rule.CreditMax
	}
	state.LastCreditAt = now
	return state, state.Credit - before
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

func (s *Service) ResetUser(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.observed, userID)
	delete(s.generations, userID)
	for key := range s.kickedConnections {
		if strings.HasPrefix(key, userID+"|") {
			delete(s.kickedConnections, key)
		}
	}
	if snapshot, ok := s.snapshots[userID]; ok {
		snapshot.Used = 0
		snapshot.Remaining = snapshot.Policy.Limit
		snapshot.Warnings = nil
		snapshot.Enforcement = domain.EnforcementState{}
		s.snapshots[userID] = snapshot
	}
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
		ref := guardEventRef("kick", decision.UserID, decision.Period.Key, fmt.Sprintf("%s|%d", decision.PolicyRevision, decision.Generation), now)
		attempted, appendErr := appendGuardActivity(tx, ref, "01", "enforcement_attempted", decision.UserID, now, map[string]any{
			"action": "kick", "generation": decision.Generation, "player_name": decision.PlayerName,
			"reset_at": decision.ResetAt.UTC().Format(time.RFC3339Nano),
		})
		if appendErr != nil || !attempted {
			return appendErr
		}
		if err := tx.AppendEnforcement(store.EnforcementEvent{
			UserID: decision.UserID, PeriodKey: decision.Period.Key, Action: "kick", Result: result,
			PolicyRevision: decision.PolicyRevision, Generation: decision.Generation, ErrorSummary: errorSummary, CreatedAt: now,
		}); err != nil {
			return err
		}
		resultType := "enforcement_succeeded"
		payload := map[string]any{
			"action": "kick", "generation": decision.Generation, "outcome": result,
			"player_name": decision.PlayerName, "reset_at": decision.ResetAt.UTC().Format(time.RFC3339Nano),
		}
		if resultErr != nil {
			resultType = "enforcement_failed"
			payload["error_code"] = "kick_failed"
		}
		_, appendErr = appendGuardActivity(tx, ref, "02", resultType, decision.UserID, now, payload)
		return appendErr
	})
	if err != nil {
		return err
	}
	if resultErr == nil {
		s.kickedConnections[key] = true
	}
	return nil
}

func guardEventRef(action, userID, periodKey, discriminator string, at time.Time) string {
	sum := sha256.Sum256([]byte(action + "\x00" + userID + "\x00" + periodKey + "\x00" + discriminator + "\x00" + at.UTC().Format(time.RFC3339Nano)))
	return "guard_action_" + hex.EncodeToString(sum[:])
}

func appendGuardActivity(tx *store.Tx, ref, order, eventType, userID string, at time.Time, payloadValue map[string]any) (bool, error) {
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return false, fmt.Errorf("encode guard activity event: %w", err)
	}
	return tx.AppendActivityEvent(store.ActivityEvent{
		ID: ref + "_" + order, EventType: eventType, SubjectType: "player", SubjectID: userID,
		OccurredAt: at, ObservedAt: at, Source: "guard", SourceRef: ref, CorrelationID: ref,
		Confidence: "observed", SchemaVersion: 1, PayloadJSON: string(payload),
	})
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
		period, used, remaining, err := s.currentUsage(tx, userID, rule, now, !snapshot.Online)
		if err != nil {
			return err
		}
		snapshot.Period = period
		snapshot.Used = used
		snapshot.Remaining = remaining
		lastCreditRecovered, err := s.lastCreditRecovery(tx, userID, rule)
		if err != nil {
			return err
		}
		snapshot.LastCreditRecovered = lastCreditRecovered
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
