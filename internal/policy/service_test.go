package policy

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/config"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

func duration(value time.Duration) config.Duration { return config.Duration{Duration: value} }

func testRepo(t *testing.T) *store.Repository {
	t.Helper()
	repo, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func basePolicy() config.Policy {
	return config.Policy{
		Timezone: "Asia/Shanghai",
		Default: config.Rule{
			Enabled:       true,
			Period:        "daily",
			ResetAt:       "04:00",
			Limit:         duration(2 * time.Hour),
			WarningBefore: []config.Duration{duration(30 * time.Minute), duration(5 * time.Minute)},
		},
		Overrides: map[string]config.RuleOverride{},
	}
}

func TestDailyPeriodBeforeResetUsesPreviousDay(t *testing.T) {
	svc, err := New(testRepo(t), basePolicy())
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, loc)
	got := svc.Period(svc.Resolve("steam_1"), now)
	wantStart := time.Date(2026, 7, 9, 4, 0, 0, 0, loc).UTC()
	wantEnd := time.Date(2026, 7, 10, 4, 0, 0, 0, loc).UTC()
	if !got.Start.Equal(wantStart) || !got.End.Equal(wantEnd) {
		t.Fatalf("period=%+v want %v..%v", got, wantStart, wantEnd)
	}
}

func TestWeeklyPeriodUsesConfiguredWeekday(t *testing.T) {
	cfg := basePolicy()
	cfg.Default.Period = "weekly"
	cfg.Default.ResetWeekday = "Monday"
	svc, err := New(testRepo(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, loc)
	got := svc.Period(svc.Resolve("steam_1"), now)
	want := time.Date(2026, 7, 6, 4, 0, 0, 0, loc).UTC()
	if !got.Start.Equal(want) || got.End.Sub(got.Start) != 7*24*time.Hour {
		t.Fatalf("period=%+v", got)
	}
}

func TestExactResetStartsNewPeriod(t *testing.T) {
	svc, _ := New(testRepo(t), basePolicy())
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 7, 10, 4, 0, 0, 0, loc)
	got := svc.Period(svc.Resolve("steam_1"), now)
	if !got.Start.Equal(now.UTC()) {
		t.Fatalf("start=%v want=%v", got.Start, now.UTC())
	}
}

func TestDailyPeriodHandlesDSTCalendarBoundaries(t *testing.T) {
	cfg := basePolicy()
	cfg.Timezone = "America/New_York"
	svc, err := New(testRepo(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation(cfg.Timezone)
	now := time.Date(2026, 3, 8, 3, 30, 0, 0, loc)
	got := svc.Period(svc.Resolve("steam_1"), now)
	if got.End.Sub(got.Start) != 23*time.Hour {
		t.Fatalf("spring-forward period=%s", got.End.Sub(got.Start))
	}
}

func TestResolveOverrideAndExemption(t *testing.T) {
	cfg := basePolicy()
	fourHours := duration(4 * time.Hour)
	cfg.Overrides["steam_override"] = config.RuleOverride{Limit: &fourHours}
	cfg.Overrides["steam_exempt"] = config.RuleOverride{Exempt: true}
	svc, _ := New(testRepo(t), cfg)
	if got := svc.Resolve("steam_override"); got.Limit != 4*time.Hour || !got.Enabled {
		t.Fatalf("override=%+v", got)
	}
	if got := svc.Resolve("steam_exempt"); !got.Exempt || got.Enabled {
		t.Fatalf("exempt=%+v", got)
	}
	if got := svc.Resolve("steam_default"); got.Limit != 2*time.Hour {
		t.Fatalf("default=%+v", got)
	}
}

func TestPeriodKeyChangesWhenPolicyIdentityChanges(t *testing.T) {
	svc, _ := New(testRepo(t), basePolicy())
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	daily := svc.Resolve("steam_1")
	first := svc.Period(daily, now)
	daily.ResetAt = "05:00"
	second := svc.Period(daily, now)
	if first.Key == second.Key {
		t.Fatal("reset schedule identity must be represented in period key")
	}
}

func TestPeriodKeyDoesNotChangeWhenOnlyLimitChanges(t *testing.T) {
	svc, _ := New(testRepo(t), basePolicy())
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	rule := svc.Resolve("steam_1")
	first := svc.Period(rule, now)
	rule.Limit = 3 * time.Hour
	second := svc.Period(rule, now)
	if first.Key != second.Key {
		t.Fatal("limit changes must preserve current usage period")
	}
}

func TestPolicyRevisionChangesWhenLimitChanges(t *testing.T) {
	svc, _ := New(testRepo(t), basePolicy())
	first := svc.Resolve("steam_1")
	cfg := basePolicy()
	cfg.Default.Limit = duration(3 * time.Hour)
	if err := svc.SetPolicy(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	second := svc.Resolve("steam_1")
	if first.Revision == second.Revision {
		t.Fatal("limit change must produce a new enforcement policy revision")
	}
}
