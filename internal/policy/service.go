package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/config"
	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

type Service struct {
	mu  sync.RWMutex
	cfg config.Policy
	loc *time.Location
}

func New(cfg config.Policy) (*Service, error) {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("load policy timezone: %w", err)
	}
	return &Service{cfg: cfg, loc: loc}, nil
}

func (s *Service) Update(cfg config.Policy) error {
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
		if override.WarningBefore != nil {
			rule.WarningBefore = append([]config.Duration(nil), (*override.WarningBefore)...)
		}
	}
	warnings := make([]time.Duration, len(rule.WarningBefore))
	for i, warning := range rule.WarningBefore {
		warnings[i] = warning.Duration
	}
	exempt := ok && override.Exempt
	return domain.ResolvedPolicy{
		Enabled:       rule.Enabled && !exempt,
		Exempt:        exempt,
		PeriodType:    rule.Period,
		Timezone:      s.cfg.Timezone,
		ResetAt:       rule.ResetAt,
		ResetWeekday:  rule.ResetWeekday,
		Limit:         rule.Limit.Duration,
		WarningBefore: warnings,
	}
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
