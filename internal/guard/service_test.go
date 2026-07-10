package guard

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/config"
	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/policy"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type commitErrorRepository struct {
	inner *store.Repository
}

func (r commitErrorRepository) WithTx(ctx context.Context, fn func(*store.Tx) error) error {
	if err := r.inner.WithTx(ctx, fn); err != nil {
		return err
	}
	return errors.New("simulated commit result failure")
}

func (r commitErrorRepository) Players(ctx context.Context) ([]domain.Player, error) {
	return r.inner.Players(ctx)
}

func (r commitErrorRepository) Player(ctx context.Context, userID string) (domain.Player, error) {
	return r.inner.Player(ctx, userID)
}

func (r commitErrorRepository) Usage(ctx context.Context, userID, periodKey string) (domain.Usage, error) {
	return r.inner.Usage(ctx, userID, periodKey)
}

func (r commitErrorRepository) WarningEvents(ctx context.Context, userID, periodKey string) ([]store.WarningEvent, error) {
	return r.inner.WarningEvents(ctx, userID, periodKey)
}

func (r commitErrorRepository) EnforcementEventsForPolicy(ctx context.Context, userID, periodKey, policyRevision string) ([]store.EnforcementEvent, error) {
	return r.inner.EnforcementEventsForPolicy(ctx, userID, periodKey, policyRevision)
}

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
	warnings := []config.Duration{{Duration: 30 * time.Minute}, {Duration: 5 * time.Minute}}
	if limit <= 30*time.Minute {
		warnings = []config.Duration{{Duration: limit / 2}}
	}
	policyService, err := policy.New(repo, config.Policy{
		Timezone: "Asia/Shanghai",
		Default: config.Rule{
			Enabled:       true,
			Period:        "daily",
			ResetAt:       "04:00",
			Limit:         config.Duration{Duration: limit},
			WarningBefore: warnings,
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
	decisions := h.observe(h.start.Add(30*time.Second), player())
	if got := h.used(h.start); got != 30*time.Second {
		t.Fatalf("usage=%s", got)
	}
	if len(decisions.Observations) != 1 || decisions.Observations[0].Added != 30*time.Second || decisions.Observations[0].SkipReason != "" {
		t.Fatalf("observations=%+v", decisions.Observations)
	}
}

func TestObservationResultExplainsDisabledPolicy(t *testing.T) {
	h := newHarness(t, 2*time.Hour, 2*time.Hour)
	if err := h.policy.SetPolicy(context.Background(), config.Policy{
		Timezone: "Asia/Shanghai",
		Default: config.Rule{
			Enabled:       false,
			Period:        "daily",
			ResetAt:       "04:00",
			Limit:         config.Duration{Duration: 2 * time.Hour},
			WarningBefore: []config.Duration{{Duration: 30 * time.Minute}},
		},
		Overrides: map[string]config.RuleOverride{},
	}); err != nil {
		t.Fatal(err)
	}
	h.observe(h.start, player())
	decisions := h.observe(h.start.Add(30*time.Second), player())
	if got := h.used(h.start); got != 0 {
		t.Fatalf("usage=%s", got)
	}
	if len(decisions.Observations) != 1 || decisions.Observations[0].SkipReason != "policy_disabled" || decisions.Observations[0].PolicyEnabled {
		t.Fatalf("observations=%+v", decisions.Observations)
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

func TestPersistenceFailureClearsContinuity(t *testing.T) {
	h := newHarness(t, 2*time.Hour, time.Hour)
	h.observe(h.start, player())
	h.service.repo = commitErrorRepository{inner: h.repo}
	if _, err := h.service.Observe(t.Context(), h.start.Add(30*time.Second), []domain.Player{player()}); err == nil {
		t.Fatal("expected persistence error")
	}
	if len(h.service.observed) != 0 {
		t.Fatalf("failed observation retained continuity: %+v", h.service.observed)
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

func TestCooldownPolicyRequiresRestAfterPlayBudget(t *testing.T) {
	h := newHarness(t, 2*time.Hour, 2*time.Hour)
	if err := h.policy.SetPolicy(context.Background(), config.Policy{
		Timezone: "Asia/Shanghai",
		Default: config.Rule{
			Enabled:       true,
			Strategy:      "cooldown",
			CooldownEvery: config.Duration{Duration: time.Hour},
			CooldownRest:  config.Duration{Duration: 30 * time.Minute},
			WarningBefore: []config.Duration{{Duration: 10 * time.Minute}},
		},
		Overrides: map[string]config.RuleOverride{},
	}); err != nil {
		t.Fatal(err)
	}
	h.observe(h.start, player())
	first := h.observe(h.start.Add(time.Hour), player())
	if len(first.Kicks) != 1 {
		t.Fatalf("kicks=%+v", first.Kicks)
	}
	if !first.Kicks[0].ResetAt.Equal(h.start.Add(90 * time.Minute)) {
		t.Fatalf("reset_at=%v", first.Kicks[0].ResetAt)
	}
	if first.Observations[0].Added != time.Hour || first.Observations[0].Remaining != 0 {
		t.Fatalf("observations=%+v", first.Observations)
	}
	duringRest := h.observe(h.start.Add(70*time.Minute), player())
	if len(duringRest.Kicks) != 1 || duringRest.Observations[0].Added != 0 {
		t.Fatalf("during rest=%+v observations=%+v", duringRest.Kicks, duringRest.Observations)
	}
	afterRest := h.observe(h.start.Add(91*time.Minute), player())
	if len(afterRest.Kicks) != 0 || afterRest.Observations[0].Used != time.Minute || afterRest.Observations[0].Remaining != 59*time.Minute {
		t.Fatalf("after rest kicks=%+v observations=%+v", afterRest.Kicks, afterRest.Observations)
	}
}

func TestCreditPolicyRecoversWhileOfflineAndConsumesOnline(t *testing.T) {
	h := newHarness(t, 2*time.Hour, 2*time.Hour)
	if err := h.policy.SetPolicy(context.Background(), config.Policy{
		Timezone: "Asia/Shanghai",
		Default: config.Rule{
			Enabled:             true,
			Strategy:            "credit",
			CreditRecoverEvery:  config.Duration{Duration: time.Hour},
			CreditRecoverAmount: config.Duration{Duration: 30 * time.Minute},
			CreditMax:           config.Duration{Duration: time.Hour},
			WarningBefore:       []config.Duration{{Duration: 10 * time.Minute}},
		},
		Overrides: map[string]config.RuleOverride{},
	}); err != nil {
		t.Fatal(err)
	}
	h.observe(h.start, player())
	usedHalf := h.observe(h.start.Add(30*time.Minute), player())
	if usedHalf.Observations[0].Remaining != 30*time.Minute {
		t.Fatalf("used half observations=%+v", usedHalf.Observations)
	}
	h.observe(h.start.Add(30 * time.Minute))
	afterOfflineRecovery := h.observe(h.start.Add(90*time.Minute), player())
	if afterOfflineRecovery.Observations[0].Remaining != time.Hour {
		t.Fatalf("recovered observations=%+v", afterOfflineRecovery.Observations)
	}
	empty := h.observe(h.start.Add(150*time.Minute), player())
	if len(empty.Kicks) != 1 || empty.Observations[0].Remaining != 0 {
		t.Fatalf("empty kicks=%+v observations=%+v", empty.Kicks, empty.Observations)
	}
}

func TestKickIsNotSuppressedWhenAuditCommitFails(t *testing.T) {
	h := newHarness(t, time.Minute, 2*time.Minute)
	h.observe(h.start, player())
	kickAt := h.start.Add(time.Minute)
	decision := h.observe(kickAt, player()).Kicks[0]
	h.service.repo = commitErrorRepository{inner: h.repo}
	if err := h.service.RecordKickResult(t.Context(), decision, nil, kickAt); err == nil {
		t.Fatal("expected audit failure")
	}
	h.service.repo = h.repo
	retry := h.observe(kickAt.Add(10*time.Second), player())
	if len(retry.Kicks) != 1 {
		t.Fatalf("unpersisted success suppressed retry: %+v", retry.Kicks)
	}
}

func TestSuccessfulKickDoesNotSuppressNextPeriod(t *testing.T) {
	h := newHarness(t, time.Minute, 2*time.Minute)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	start := time.Date(2026, 7, 10, 3, 58, 0, 0, loc)
	h.observe(start, player())
	first := h.observe(start.Add(time.Minute), player())
	if len(first.Kicks) != 1 {
		t.Fatalf("first period kicks=%+v", first.Kicks)
	}
	if err := h.service.RecordKickResult(t.Context(), first.Kicks[0], nil, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	h.observe(start.Add(2*time.Minute), player())
	nextPeriod := h.observe(start.Add(3*time.Minute), player())
	if len(nextPeriod.Kicks) != 1 {
		t.Fatalf("next period kick was suppressed: %+v", nextPeriod.Kicks)
	}
}

func TestFailedKickBackoffSurvivesServiceRestart(t *testing.T) {
	h := newHarness(t, time.Minute, 2*time.Minute)
	h.observe(h.start, player())
	firstAt := h.start.Add(time.Minute)
	first := h.observe(firstAt, player())
	if len(first.Kicks) != 1 {
		t.Fatalf("kicks=%+v", first.Kicks)
	}
	if err := h.service.RecordKickResult(t.Context(), first.Kicks[0], errors.New("temporary failure"), firstAt); err != nil {
		t.Fatal(err)
	}
	h.service = New(h.repo, h.policy, 2*time.Minute, 15*time.Second, 5*time.Minute)
	before := h.observe(firstAt.Add(10*time.Second), player())
	if len(before.Kicks) != 0 {
		t.Fatalf("restart bypassed cooldown: %+v", before.Kicks)
	}
	after := h.observe(firstAt.Add(16*time.Second), player())
	if len(after.Kicks) != 1 {
		t.Fatalf("kick did not resume after persisted cooldown: %+v", after.Kicks)
	}
}

func TestSnapshotRestoresWarningAndEnforcementFromRepository(t *testing.T) {
	h := newHarness(t, 2*time.Hour, 3*time.Hour)
	h.observe(h.start, player())
	warningAt := h.start.Add(91 * time.Minute)
	warning := h.observe(warningAt, player())
	if len(warning.Warnings) != 1 {
		t.Fatalf("warnings=%+v", warning.Warnings)
	}
	if err := h.service.RecordWarningResult(t.Context(), warning.Warnings[0], nil, warningAt); err != nil {
		t.Fatal(err)
	}
	kickAt := h.start.Add(2 * time.Hour)
	kick := h.observe(kickAt, player())
	if len(kick.Kicks) != 1 {
		t.Fatalf("kicks=%+v", kick.Kicks)
	}
	if err := h.service.RecordKickResult(t.Context(), kick.Kicks[0], nil, kickAt); err != nil {
		t.Fatal(err)
	}
	restarted := New(h.repo, h.policy, 3*time.Hour, 15*time.Second, 5*time.Minute)
	restarted.now = func() time.Time { return kickAt }
	snapshot, err := restarted.Snapshot(t.Context(), "steam_1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Used != 2*time.Hour || len(snapshot.Warnings) != 1 || snapshot.Warnings[0].Status != "success" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if snapshot.Enforcement.Status != "success" {
		t.Fatalf("enforcement=%+v", snapshot.Enforcement)
	}
}

func TestSnapshotRecalculatesCurrentPeriodForKnownOfflinePlayer(t *testing.T) {
	h := newHarness(t, 2*time.Hour, time.Hour)
	h.observe(h.start, player())
	h.observe(h.start.Add(30*time.Minute), player())
	h.observe(h.start.Add(31 * time.Minute))
	nextDay := h.start.Add(24 * time.Hour)
	h.service.now = func() time.Time { return nextDay }
	snapshot, err := h.service.Snapshot(t.Context(), "steam_1")
	if err != nil {
		t.Fatal(err)
	}
	wantPeriod := h.policy.Period(h.policy.Resolve("steam_1"), nextDay)
	if snapshot.Period.Key != wantPeriod.Key || snapshot.Used != 0 || snapshot.Online {
		t.Fatalf("snapshot=%+v want period=%+v", snapshot, wantPeriod)
	}
}

func TestDisabledAndExemptPoliciesDoNotCharge(t *testing.T) {
	h := newHarness(t, 2*time.Hour, time.Hour)
	disabled := false
	if err := h.policy.SetPolicy(context.Background(), config.Policy{
		Timezone:  "Asia/Shanghai",
		Default:   config.Rule{Enabled: false, Period: "daily", ResetAt: "04:00", Limit: config.Duration{Duration: 2 * time.Hour}},
		Overrides: map[string]config.RuleOverride{"steam_1": {Enabled: &disabled, Exempt: true}},
	}); err != nil {
		t.Fatal(err)
	}
	h.observe(h.start, player())
	h.observe(h.start.Add(30*time.Second), player())
	if got := h.used(h.start); got != 0 {
		t.Fatalf("usage=%s", got)
	}
}
