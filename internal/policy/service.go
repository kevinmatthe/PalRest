package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/kevinmatt/palworld-playtime-guard/internal/config"
	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type Repository interface {
	PolicyDocument(context.Context) (string, error)
	UpsertPolicyDocument(context.Context, string, time.Time) error
}

type Service struct {
	repo Repository
	mu   sync.RWMutex
	cfg  config.Policy
	loc  *time.Location
}

func New(repo Repository, seed config.Policy) (*Service, error) {
	ctx := context.Background()
	policyYAML, err := repo.PolicyDocument(ctx)
	if errors.Is(err, store.ErrNotFound) {
		data, marshalErr := config.MarshalPolicy(seed)
		if marshalErr != nil {
			return nil, marshalErr
		}
		policyYAML = string(data)
		if writeErr := repo.UpsertPolicyDocument(ctx, policyYAML, time.Now().UTC()); writeErr != nil {
			return nil, writeErr
		}
	} else if err != nil {
		return nil, err
	}
	cfg, err := config.ParsePolicy([]byte(policyYAML))
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("load policy timezone: %w", err)
	}
	return &Service{repo: repo, cfg: cfg, loc: loc}, nil
}

func (s *Service) Policy() config.Policy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clonePolicy(s.cfg)
}

func (s *Service) SetPolicy(ctx context.Context, cfg config.Policy) error {
	if err := config.ValidatePolicy(cfg); err != nil {
		return err
	}
	data, err := config.MarshalPolicy(cfg)
	if err != nil {
		return err
	}
	if err := s.repo.UpsertPolicyDocument(ctx, string(data), time.Now().UTC()); err != nil {
		return err
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return fmt.Errorf("load policy timezone: %w", err)
	}
	s.mu.Lock()
	s.cfg = cfg
	s.loc = loc
	s.mu.Unlock()
	return nil
}

func (s *Service) Resolve(userID string) domain.ResolvedPolicy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rule := s.cfg.Default
	override, ok := s.cfg.Overrides[userID]
	if ok {
		if override.Enabled != nil {
			rule.Enabled = *override.Enabled
		}
		if override.Strategy != nil {
			rule.Strategy = *override.Strategy
		}
		if override.Period != nil {
			rule.Period = *override.Period
		}
		if override.ResetAt != nil {
			rule.ResetAt = *override.ResetAt
		}
		if override.ResetWeekday != nil {
			rule.ResetWeekday = *override.ResetWeekday
		}
		if override.Limit != nil {
			rule.Limit = *override.Limit
		}
		if override.CooldownEvery != nil {
			rule.CooldownEvery = *override.CooldownEvery
		}
		if override.CooldownRest != nil {
			rule.CooldownRest = *override.CooldownRest
		}
		if override.CreditRecoverEvery != nil {
			rule.CreditRecoverEvery = *override.CreditRecoverEvery
		}
		if override.CreditRecoverAmount != nil {
			rule.CreditRecoverAmount = *override.CreditRecoverAmount
		}
		if override.CreditMax != nil {
			rule.CreditMax = *override.CreditMax
		}
		if override.WarningBefore != nil {
			rule.WarningBefore = append([]config.Duration(nil), (*override.WarningBefore)...)
		}
	}
	warnings := make([]time.Duration, len(rule.WarningBefore))
	for i, warning := range rule.WarningBefore {
		warnings[i] = warning.Duration
	}
	exempt := ok && override.Exempt
	strategy := normalizedStrategy(rule.Strategy)
	resolved := domain.ResolvedPolicy{
		Enabled:             rule.Enabled && !exempt,
		Exempt:              exempt,
		Strategy:            strategy,
		PeriodType:          rule.Period,
		Timezone:            s.cfg.Timezone,
		ResetAt:             rule.ResetAt,
		ResetWeekday:        rule.ResetWeekday,
		Limit:               ruleLimit(rule),
		CooldownEvery:       rule.CooldownEvery.Duration,
		CooldownRest:        rule.CooldownRest.Duration,
		CreditRecoverEvery:  rule.CreditRecoverEvery.Duration,
		CreditRecoverAmount: rule.CreditRecoverAmount.Duration,
		CreditMax:           rule.CreditMax.Duration,
		WarningBefore:       warnings,
	}
	resolved.Revision = revision(resolved)
	return resolved
}

func revision(rule domain.ResolvedPolicy) string {
	parts := []string{
		fmt.Sprintf("enabled=%t", rule.Enabled),
		fmt.Sprintf("exempt=%t", rule.Exempt),
		"strategy=" + rule.Strategy,
		"period=" + rule.PeriodType,
		"timezone=" + rule.Timezone,
		"reset_at=" + rule.ResetAt,
		"reset_weekday=" + strings.ToLower(rule.ResetWeekday),
		fmt.Sprintf("limit=%d", rule.Limit.Nanoseconds()),
		fmt.Sprintf("cooldown_every=%d", rule.CooldownEvery.Nanoseconds()),
		fmt.Sprintf("cooldown_rest=%d", rule.CooldownRest.Nanoseconds()),
		fmt.Sprintf("credit_recover_every=%d", rule.CreditRecoverEvery.Nanoseconds()),
		fmt.Sprintf("credit_recover_amount=%d", rule.CreditRecoverAmount.Nanoseconds()),
		fmt.Sprintf("credit_max=%d", rule.CreditMax.Nanoseconds()),
	}
	for _, warning := range rule.WarningBefore {
		parts = append(parts, fmt.Sprintf("warning=%d", warning.Nanoseconds()))
	}
	hash := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(hash[:12])
}

func normalizedStrategy(strategy string) string {
	if strategy == "" {
		return "fixed_window"
	}
	return strategy
}

func ruleLimit(rule config.Rule) time.Duration {
	switch normalizedStrategy(rule.Strategy) {
	case "cooldown":
		return rule.CooldownEvery.Duration
	case "credit":
		return rule.CreditMax.Duration
	default:
		return rule.Limit.Duration
	}
}

func clonePolicy(policy config.Policy) config.Policy {
	clone := policy
	clone.Default.WarningBefore = append([]config.Duration(nil), policy.Default.WarningBefore...)
	clone.Overrides = make(map[string]config.RuleOverride, len(policy.Overrides))
	for userID, override := range policy.Overrides {
		if override.WarningBefore != nil {
			warnings := append([]config.Duration(nil), (*override.WarningBefore)...)
			override.WarningBefore = &warnings
		}
		clone.Overrides[userID] = override
	}
	if clone.Overrides == nil {
		clone.Overrides = map[string]config.RuleOverride{}
	}
	return clone
}

func (s *Service) Period(rule domain.ResolvedPolicy, now time.Time) domain.Period {
	s.mu.RLock()
	loc := s.loc
	s.mu.RUnlock()
	localNow := now.In(loc)
	hour, minute := parseClock(rule.ResetAt)
	var start, end time.Time
	if rule.PeriodType == "weekly" {
		weekday := parseWeekday(rule.ResetWeekday)
		daysSince := (7 + int(localNow.Weekday()) - int(weekday)) % 7
		date := localNow.AddDate(0, 0, -daysSince)
		start = time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, loc)
		if localNow.Before(start) {
			start = start.AddDate(0, 0, -7)
		}
		end = start.AddDate(0, 0, 7)
	} else {
		start = time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, loc)
		if localNow.Before(start) {
			start = start.AddDate(0, 0, -1)
		}
		end = start.AddDate(0, 0, 1)
	}
	start = start.UTC()
	end = end.UTC()
	identity := strings.Join([]string{rule.PeriodType, rule.Timezone, rule.ResetAt, strings.ToLower(rule.ResetWeekday), start.Format(time.RFC3339Nano)}, "|")
	hash := sha256.Sum256([]byte(identity))
	return domain.Period{Key: hex.EncodeToString(hash[:12]), Start: start, End: end}
}

func parseClock(value string) (int, int) {
	parsed, _ := time.Parse("15:04", value)
	return parsed.Hour(), parsed.Minute()
}

func parseWeekday(value string) time.Weekday {
	for day := time.Sunday; day <= time.Saturday; day++ {
		if strings.EqualFold(day.String(), value) {
			return day
		}
	}
	return time.Sunday
}
