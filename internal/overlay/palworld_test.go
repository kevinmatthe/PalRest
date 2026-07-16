package overlay

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type fakeSnapshotSource struct {
	snapshot domain.PlayerSnapshot
	err      error
	calls    int
	userID   string
}

func (f *fakeSnapshotSource) Snapshot(_ context.Context, userID string) (domain.PlayerSnapshot, error) {
	f.calls++
	f.userID = userID
	return f.snapshot, f.err
}

type fakeDailySource struct {
	rows                       []store.DailyActivity
	err                        error
	calls                      int
	userID, startDate, endDate string
}

func (f *fakeDailySource) PlayerDailyActivity(_ context.Context, userID, startDate, endDate string) ([]store.DailyActivity, error) {
	f.calls++
	f.userID, f.startDate, f.endDate = userID, startDate, endDate
	return f.rows, f.err
}

type fakeStatusSource struct{ status domain.PollStatus }

func (f fakeStatusSource) Status() domain.PollStatus { return f.status }

func TestPalworldProviderBuildsOnlineSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 10, 0, time.UTC)
	lastSuccess := now.Add(-10 * time.Second)
	guard := &fakeSnapshotSource{snapshot: domain.PlayerSnapshot{
		Player: domain.Player{
			UserID: "steam_1", Name: "Lamball Keeper", AccountName: "account_1",
			Ping: 38.5, LocationX: 187.25, LocationY: -64.5, Level: 42,
		},
		Policy: domain.ResolvedPolicy{
			Enabled: true, Strategy: "fixed_window", WarningBefore: []time.Duration{45 * time.Minute, 5 * time.Minute},
		},
		Used: 90 * time.Minute, Remaining: 30 * time.Minute, Online: true,
	}}
	daily := &fakeDailySource{rows: []store.DailyActivity{
		{Date: "2026-07-14", Observed: 90 * time.Minute},
		{Date: "2026-07-16", Observed: 30 * time.Minute},
	}}
	p := NewPalworldProvider(guard, daily, fakeStatusSource{domain.PollStatus{LastSuccess: lastSuccess}}, time.UTC, 15*time.Second)
	p.now = func() time.Time { return now }

	got, err := p.Snapshot(t.Context(), "palworld", " steam_1 ")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if guard.calls != 1 || daily.calls != 1 || guard.userID != "steam_1" || daily.userID != "steam_1" {
		t.Fatalf("dependency calls: guard=%d/%q daily=%d/%q", guard.calls, guard.userID, daily.calls, daily.userID)
	}
	if got.Schema != SchemaV1 || got.GameID != "palworld" || got.UserID != "steam_1" {
		t.Errorf("snapshot identity fields = %#v", got)
	}
	if got.ObservedAt != lastSuccess || got.FreshUntil != lastSuccess.Add(15*time.Second) || got.SourceStatus != "online" {
		t.Errorf("freshness = observed %s fresh %s status %q", got.ObservedAt, got.FreshUntil, got.SourceStatus)
	}
	if got.Identity.DisplayName != "Lamball Keeper" || got.Identity.AccountName != "account_1" || got.Identity.Level == nil || *got.Identity.Level != 42 {
		t.Errorf("Identity = %#v", got.Identity)
	}
	if got.Latency == nil || got.Latency.Milliseconds != 38.5 {
		t.Errorf("Latency = %#v", got.Latency)
	}
	if got.Map == nil || got.Map.X != 187.25 || got.Map.Y != -64.5 {
		t.Errorf("Map = %#v", got.Map)
	}
	wantCaps := []string{"identity", "latency", "timers", "map"}
	if !equalStrings(got.Capabilities, wantCaps) {
		t.Errorf("Capabilities = %q, want %q", got.Capabilities, wantCaps)
	}
	assertTimer(t, got.Timers, "today_observed", "Today observed", 30*time.Minute)
	assertTimer(t, got.Timers, "week_observed", "Week observed", 2*time.Hour)
	used := assertTimer(t, got.Timers, "policy_cycle_used", "Policy cycle used", 90*time.Minute)
	remaining := assertTimer(t, got.Timers, "policy_remaining", "频控剩余", 30*time.Minute)
	if used.Progress == nil || *used.Progress != 0.75 || remaining.Progress == nil || *remaining.Progress != 0.25 {
		t.Errorf("policy progress: used=%v remaining=%v", used.Progress, remaining.Progress)
	}
	if used.Tone != "warning" || remaining.Tone != "warning" {
		t.Errorf("policy tones: used=%q remaining=%q", used.Tone, remaining.Tone)
	}
}

func TestPalworldProviderUsesMondayWeekBoundary(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}
	now := time.Date(2026, 3, 11, 23, 30, 0, 0, location)
	guard := &fakeSnapshotSource{snapshot: domain.PlayerSnapshot{Player: domain.Player{UserID: "u"}}}
	daily := &fakeDailySource{}
	p := NewPalworldProvider(guard, daily, fakeStatusSource{}, location, time.Minute)
	p.now = func() time.Time { return now }

	if _, err := p.Snapshot(t.Context(), "palworld", "u"); err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if daily.startDate != "2026-03-09" || daily.endDate != "2026-03-12" {
		t.Errorf("date range = [%s,%s), want [2026-03-09,2026-03-12)", daily.startDate, daily.endDate)
	}
}

func TestPalworldProviderMarksStalePollUnknown(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 1, 0, 0, time.UTC)
	lastSuccess := now.Add(-31 * time.Second)
	guard := &fakeSnapshotSource{snapshot: domain.PlayerSnapshot{
		Player: domain.Player{UserID: "u", Ping: 1, LocationX: 2, LocationY: -3}, Online: true,
	}}
	p := NewPalworldProvider(guard, &fakeDailySource{}, fakeStatusSource{domain.PollStatus{LastSuccess: lastSuccess}}, time.UTC, 30*time.Second)
	p.now = func() time.Time { return now }

	got, err := p.Snapshot(t.Context(), "palworld", "u")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got.SourceStatus != "unknown" || got.ObservedAt != lastSuccess || got.FreshUntil != lastSuccess.Add(30*time.Second) {
		t.Errorf("stale freshness = observed %s fresh %s status %q", got.ObservedAt, got.FreshUntil, got.SourceStatus)
	}
	if got.Latency != nil || got.Map != nil {
		t.Errorf("stale source exposed live fields: latency=%#v map=%#v", got.Latency, got.Map)
	}
}

func TestPalworldProviderOmitsUnknownLatencyAndMap(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name                 string
		ping, x, y           float64
		wantLatency, wantMap bool
		wantCapabilities     []string
	}{
		{name: "invalid ping preserves valid map", ping: math.NaN(), x: -1, y: 2, wantMap: true, wantCapabilities: []string{"identity", "timers", "map"}},
		{name: "valid zero ping preserves latency with invalid map", ping: 0, x: 1, y: math.Inf(1), wantLatency: true, wantCapabilities: []string{"identity", "latency", "timers"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guard := &fakeSnapshotSource{snapshot: domain.PlayerSnapshot{
				Player: domain.Player{UserID: "u", Ping: tt.ping, LocationX: tt.x, LocationY: tt.y}, Online: true,
			}}
			p := NewPalworldProvider(guard, &fakeDailySource{}, fakeStatusSource{domain.PollStatus{LastSuccess: now}}, time.UTC, time.Minute)
			p.now = func() time.Time { return now }

			got, err := p.Snapshot(t.Context(), "palworld", "u")
			if err != nil {
				t.Fatalf("Snapshot() error = %v", err)
			}
			if (got.Latency != nil) != tt.wantLatency || (got.Map != nil) != tt.wantMap {
				t.Errorf("live fields: latency=%#v map=%#v, want latency=%v map=%v", got.Latency, got.Map, tt.wantLatency, tt.wantMap)
			}
			if !equalStrings(got.Capabilities, tt.wantCapabilities) {
				t.Errorf("Capabilities = %q, want %q", got.Capabilities, tt.wantCapabilities)
			}
		})
	}
}

func TestPalworldProviderOmitsPolicyTimersWhenDisabledOrExempt(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	for _, policy := range []domain.ResolvedPolicy{
		{Enabled: false, Strategy: "fixed_window"},
		{Enabled: true, Exempt: true, Strategy: "fixed_window"},
	} {
		guard := &fakeSnapshotSource{snapshot: domain.PlayerSnapshot{Player: domain.Player{UserID: "u"}, Policy: policy}}
		daily := &fakeDailySource{rows: []store.DailyActivity{{Date: "2026-07-15", Observed: time.Hour}, {Date: "2026-07-16", Observed: 30 * time.Minute}}}
		p := NewPalworldProvider(guard, daily, fakeStatusSource{domain.PollStatus{LastSuccess: now}}, time.UTC, time.Minute)
		p.now = func() time.Time { return now }

		got, err := p.Snapshot(t.Context(), "palworld", "u")
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if len(got.Timers) != 2 {
			t.Fatalf("Timers = %#v, want analytics timers only", got.Timers)
		}
		assertTimer(t, got.Timers, "today_observed", "Today observed", 30*time.Minute)
		assertTimer(t, got.Timers, "week_observed", "Week observed", 90*time.Minute)
		if !equalStrings(got.Capabilities, []string{"identity", "timers"}) {
			t.Errorf("Capabilities = %q, want identity/timers", got.Capabilities)
		}
	}
}

func TestPalworldProviderMapsNotFound(t *testing.T) {
	tests := []struct {
		name  string
		guard *fakeSnapshotSource
		daily *fakeDailySource
	}{
		{name: "guard", guard: &fakeSnapshotSource{err: store.ErrNotFound}, daily: &fakeDailySource{}},
		{name: "analytics", guard: &fakeSnapshotSource{}, daily: &fakeDailySource{err: store.ErrNotFound}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPalworldProvider(tt.guard, tt.daily, fakeStatusSource{}, time.UTC, time.Minute)
			_, err := p.Snapshot(t.Context(), "palworld", "u")
			if !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("Snapshot() error = %v, want errors.Is(ErrNotFound)", err)
			}
		})
	}
}

func TestPalworldProviderUsesStrategyTimerLabels(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		snapshot  domain.PlayerSnapshot
		wantLabel string
		wantValue time.Duration
	}{
		{name: "fixed window", snapshot: domain.PlayerSnapshot{Policy: domain.ResolvedPolicy{Enabled: true, Strategy: "fixed_window"}, Used: 10 * time.Minute, Remaining: 20 * time.Minute}, wantLabel: "频控剩余", wantValue: 20 * time.Minute},
		{name: "credit", snapshot: domain.PlayerSnapshot{Policy: domain.ResolvedPolicy{Enabled: true, Strategy: "credit"}, Used: 10 * time.Minute, Remaining: 20 * time.Minute}, wantLabel: "可用额度", wantValue: 20 * time.Minute},
		{name: "cooldown rest", snapshot: domain.PlayerSnapshot{Policy: domain.ResolvedPolicy{Enabled: true, Strategy: "cooldown"}, Period: domain.Period{End: now.Add(15 * time.Minute)}, Used: 30 * time.Minute, Remaining: 0}, wantLabel: "休息剩余", wantValue: 15 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.snapshot.Player.UserID = "u"
			p := NewPalworldProvider(&fakeSnapshotSource{snapshot: tt.snapshot}, &fakeDailySource{}, fakeStatusSource{domain.PollStatus{LastSuccess: now}}, time.UTC, time.Minute)
			p.now = func() time.Time { return now }
			got, err := p.Snapshot(t.Context(), "palworld", "u")
			if err != nil {
				t.Fatalf("Snapshot() error = %v", err)
			}
			assertTimer(t, got.Timers, "policy_remaining", tt.wantLabel, tt.wantValue)
		})
	}
}

func TestPalworldProviderRejectsInvalidRequests(t *testing.T) {
	p := NewPalworldProvider(&fakeSnapshotSource{}, &fakeDailySource{}, fakeStatusSource{}, time.UTC, time.Minute)
	if _, err := p.Snapshot(t.Context(), "minecraft", "u"); !errors.Is(err, ErrGameNotSupported) {
		t.Errorf("unsupported game error = %v", err)
	}
	if _, err := p.Snapshot(t.Context(), "palworld", "  "); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("empty user error = %v", err)
	}
}

func TestPalworldProviderPolicyProgressOmitsOnlyZeroDenominator(t *testing.T) {
	used, remaining := policyProgress(time.Minute, -2*time.Minute)
	if used == nil || remaining == nil {
		t.Fatalf("negative nonzero denominator progress = (%v, %v), want values", used, remaining)
	}
	if *used != 0 || *remaining != 1 {
		t.Errorf("negative nonzero denominator progress = (%v, %v), want (0, 1)", *used, *remaining)
	}

	used, remaining = policyProgress(time.Minute, -time.Minute)
	if used != nil || remaining != nil {
		t.Errorf("zero denominator progress = (%v, %v), want nil values", used, remaining)
	}
}

func TestNewPalworldProviderRejectsInvalidConfiguration(t *testing.T) {
	snapshots := &fakeSnapshotSource{}
	daily := &fakeDailySource{}
	status := fakeStatusSource{}
	tests := []struct {
		name string
		new  func()
	}{
		{name: "nil snapshot source", new: func() { NewPalworldProvider(nil, daily, status, time.UTC, time.Second) }},
		{name: "nil daily source", new: func() { NewPalworldProvider(snapshots, nil, status, time.UTC, time.Second) }},
		{name: "nil status source", new: func() { NewPalworldProvider(snapshots, daily, nil, time.UTC, time.Second) }},
		{name: "nil location", new: func() { NewPalworldProvider(snapshots, daily, status, nil, time.Second) }},
		{name: "zero max gap", new: func() { NewPalworldProvider(snapshots, daily, status, time.UTC, 0) }},
		{name: "negative max gap", new: func() { NewPalworldProvider(snapshots, daily, status, time.UTC, -time.Second) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			didPanic := false
			func() {
				defer func() { didPanic = recover() != nil }()
				tt.new()
			}()
			if !didPanic {
				t.Fatal("NewPalworldProvider() did not panic")
			}
		})
	}
}

func TestPalworldProviderTreatsFreshUntilAsInclusive(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 30, 0, time.UTC)
	guard := &fakeSnapshotSource{snapshot: domain.PlayerSnapshot{
		Player: domain.Player{UserID: "u", Ping: 0, LocationX: -1, LocationY: 2}, Online: true,
	}}
	p := NewPalworldProvider(guard, &fakeDailySource{}, fakeStatusSource{domain.PollStatus{LastSuccess: now.Add(-30 * time.Second)}}, time.UTC, 30*time.Second)
	p.now = func() time.Time { return now }

	got, err := p.Snapshot(t.Context(), "palworld", "u")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got.SourceStatus != "online" || got.Latency == nil || got.Map == nil {
		t.Errorf("inclusive fresh boundary snapshot = status %q latency=%#v map=%#v", got.SourceStatus, got.Latency, got.Map)
	}
}

func TestPalworldProviderMarksNeverSuccessfulPollUnknown(t *testing.T) {
	guard := &fakeSnapshotSource{snapshot: domain.PlayerSnapshot{
		Player: domain.Player{UserID: "u", Ping: 0, LocationX: -1, LocationY: 2}, Online: true,
	}}
	p := NewPalworldProvider(guard, &fakeDailySource{}, fakeStatusSource{}, time.UTC, 30*time.Second)

	got, err := p.Snapshot(t.Context(), "palworld", "u")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got.SourceStatus != "unknown" || !got.ObservedAt.IsZero() || !got.FreshUntil.IsZero() {
		t.Errorf("never-success freshness = observed %s fresh %s status %q", got.ObservedAt, got.FreshUntil, got.SourceStatus)
	}
	if got.Latency != nil || got.Map != nil {
		t.Errorf("never-success source exposed live fields: latency=%#v map=%#v", got.Latency, got.Map)
	}
}

func assertTimer(t *testing.T, timers []Timer, id, label string, value time.Duration) Timer {
	t.Helper()
	for _, timer := range timers {
		if timer.ID == id {
			if timer.Label != label || timer.ValueMS != value.Milliseconds() || timer.Semantic != "duration" {
				t.Errorf("timer %q = %#v, want label=%q value=%s duration", id, timer, label, value)
			}
			return timer
		}
	}
	t.Fatalf("timer %q not found in %#v", id, timers)
	return Timer{}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
