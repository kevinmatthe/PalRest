package guard

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/config"
	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/policy"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type harness struct {
	t       *testing.T
	repo    *store.Repository
	policy  *policy.Service
	service *Service
	start   time.Time
}

func newHarness(t *testing.T, limit, maxGap time.Duration) *harness {
	t.Helper()
	repo, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "guard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	policyService, err := policy.New(config.Policy{
		Timezone: "Asia/Shanghai",
		Default: config.Rule{
			Enabled:       true,
			Period:        "daily",
			ResetAt:       "04:00",
			Limit:         config.Duration{Duration: limit},
			WarningBefore: []config.Duration{{Duration: 30 * time.Minute}, {Duration: 5 * time.Minute}},
		},
		Overrides: map[string]config.RuleOverride{},
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	return &harness{
		t:       t,
		repo:    repo,
		policy:  policyService,
		service: New(repo, policyService, maxGap, 15*time.Second, 5*time.Minute),
		start:   start,
	}
}

func player() domain.Player { return domain.Player{UserID: "steam_1", PlayerID: "ABC", Name: "Kevin"} }

func (h *harness) observe(at time.Time, players ...domain.Player) Decisions {
	h.t.Helper()
	got, err := h.service.Observe(context.Background(), at, players)
	if err != nil {
		h.t.Fatal(err)
	}
	return got
}

func (h *harness) used(at time.Time) time.Duration {
	h.t.Helper()
	period := h.policy.Period(h.policy.Resolve("steam_1"), at)
	usage, err := h.repo.Usage(context.Background(), "steam_1", period.Key)
	if err == store.ErrNotFound {
		return 0
	}
	if err != nil {
		h.t.Fatal(err)
	}
	return usage.Used
}

func TestFirstObservationDoesNotChargeTime(t *testing.T) {
	h := newHarness(t, 2*time.Hour, 2*time.Hour)
	h.observe(h.start, player())
	if got := h.used(h.start); got != 0 {
		t.Fatalf("usage=%s", got)
	}
}

func TestContinuousObservationChargesElapsedTime(t *testing.T) {
	h := newHarness(t, 2*time.Hour, 2*time.Hour)
	h.observe(h.start, player())
	h.observe(h.start.Add(30*time.Second), player())
	if got := h.used(h.start); got != 30*time.Second {
		t.Fatalf("usage=%s", got)
	}
}

func TestPollFailureAndLongGapBreakContinuity(t *testing.T) {
	h := newHarness(t, 2*time.Hour, time.Minute)
	h.observe(h.start, player())
	h.service.PollFailed()
	h.observe(h.start.Add(30*time.Second), player())
	h.observe(h.start.Add(2*time.Minute), player())
	if got := h.used(h.start); got != 0 {
		t.Fatalf("usage=%s", got)
	}
}

func TestOfflinePlayerBreaksContinuity(t *testing.T) {
	h := newHarness(t, 2*time.Hour, time.Hour)
	h.observe(h.start, player())
	h.observe(h.start.Add(30 * time.Second))
	h.observe(h.start.Add(time.Minute), player())
	if got := h.used(h.start); got != 0 {
		t.Fatalf("usage=%s", got)
	}
}

func TestIntervalCrossingResetIsSplit(t *testing.T) {
	h := newHarness(t, 2*time.Hour, time.Hour)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	before := time.Date(2026, 7, 10, 3, 59, 30, 0, loc)
	after := before.Add(time.Minute)
	h.observe(before, player())
	h.observe(after, player())
	oldPeriod := h.policy.Period(h.policy.Resolve("steam_1"), before)
	newPeriod := h.policy.Period(h.policy.Resolve("steam_1"), after)
	oldUsage, err := h.repo.Usage(t.Context(), "steam_1", oldPeriod.Key)
	if err != nil {
		t.Fatal(err)
	}
	newUsage, err := h.repo.Usage(t.Context(), "steam_1", newPeriod.Key)
	if err != nil {
		t.Fatal(err)
	}
	if oldUsage.Used != 30*time.Second || newUsage.Used != 30*time.Second {
		t.Fatalf("old=%s new=%s", oldUsage.Used, newUsage.Used)
	}
}

func TestWarningThresholdIsCreatedOnce(t *testing.T) {
	h := newHarness(t, 2*time.Hour, 3*time.Hour)
	h.observe(h.start, player())
	first := h.observe(h.start.Add(91*time.Minute), player())
	if len(first.Warnings) != 1 {
		t.Fatalf("first warnings=%+v", first.Warnings)
	}
	if err := h.service.RecordWarningResult(t.Context(), first.Warnings[0], nil, h.start.Add(91*time.Minute)); err != nil {
		t.Fatal(err)
	}
	second := h.observe(h.start.Add(92*time.Minute), player())
	if len(first.Warnings) != 1 || first.Warnings[0].Threshold != 30*time.Minute {
		t.Fatalf("first warnings=%+v", first.Warnings)
	}
	if len(second.Warnings) != 0 {
		t.Fatalf("duplicate warnings=%+v", second.Warnings)
	}
}

func TestFailedWarningRetriesAfterBackoff(t *testing.T) {
	h := newHarness(t, 2*time.Hour, 3*time.Hour)
	h.observe(h.start, player())
	firstAt := h.start.Add(91 * time.Minute)
	first := h.observe(firstAt, player())
	if len(first.Warnings) != 1 {
		t.Fatalf("warnings=%+v", first.Warnings)
	}
	if err := h.service.RecordWarningResult(t.Context(), first.Warnings[0], context.DeadlineExceeded, firstAt); err != nil {
		t.Fatal(err)
	}
	beforeBackoff := h.observe(firstAt.Add(10*time.Second), player())
	if len(beforeBackoff.Warnings) != 0 {
		t.Fatalf("early retry=%+v", beforeBackoff.Warnings)
	}
	afterBackoff := h.observe(firstAt.Add(16*time.Second), player())
	if len(afterBackoff.Warnings) != 1 {
		t.Fatalf("retry=%+v", afterBackoff.Warnings)
	}
}

func TestOverLimitKickAndReconnectKick(t *testing.T) {
	h := newHarness(t, time.Minute, 2*time.Minute)
	h.observe(h.start, player())
	first := h.observe(h.start.Add(time.Minute), player())
	if len(first.Kicks) != 1 {
		t.Fatalf("kicks=%+v", first.Kicks)
	}
	if err := h.service.RecordKickResult(t.Context(), first.Kicks[0], nil, h.start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	suppressed := h.observe(h.start.Add(70*time.Second), player())
	if len(suppressed.Kicks) != 0 {
		t.Fatalf("successful connection was kicked twice: %+v", suppressed.Kicks)
	}
	h.observe(h.start.Add(80 * time.Second))
	reconnected := h.observe(h.start.Add(90*time.Second), player())
	if len(reconnected.Kicks) != 1 {
		t.Fatalf("reconnect kicks=%+v", reconnected.Kicks)
	}
}

func TestDisabledAndExemptPoliciesDoNotCharge(t *testing.T) {
	h := newHarness(t, 2*time.Hour, time.Hour)
	disabled := false
	h.policy.Update(config.Policy{
		Timezone:  "Asia/Shanghai",
		Default:   config.Rule{Enabled: false, Period: "daily", ResetAt: "04:00", Limit: config.Duration{Duration: 2 * time.Hour}},
		Overrides: map[string]config.RuleOverride{"steam_1": {Enabled: &disabled, Exempt: true}},
	})
	h.observe(h.start, player())
	h.observe(h.start.Add(30*time.Second), player())
	if got := h.used(h.start); got != 0 {
		t.Fatalf("usage=%s", got)
	}
}
