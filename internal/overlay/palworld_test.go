package overlay

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

var palworldPresentationFieldIDs = []string{
	"identity.account", "identity.uid", "identity.level",
	"presence.status", "presence.last_online",
	"network.latency", "location.coordinates",
	"activity.today", "activity.week",
	"policy.strategy", "policy.cycle_used", "policy.remaining",
	"policy.period_end", "policy.enforcement",
}

func TestPalworldProviderBuildsPresentationFieldCatalog(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 10, 0, time.UTC)
	lastOnline := now.Add(-time.Hour)
	guard := &fakeSnapshotSource{snapshot: domain.PlayerSnapshot{
		Player: domain.Player{UserID: "steam_1", Name: "Keeper", AccountName: "account_1", Level: 42, LastOnline: lastOnline, Ping: 38.5, LocationX: 187.25, LocationY: -64.5},
		Policy: domain.ResolvedPolicy{Enabled: true, Strategy: "fixed_window", WarningBefore: []time.Duration{45 * time.Minute}},
		Period: domain.Period{End: now.Add(30 * time.Minute)}, Used: 90 * time.Minute, Remaining: 30 * time.Minute,
		Enforcement: domain.EnforcementState{Status: "pending"}, Online: true,
	}}
	daily := &fakeDailySource{rows: []store.DailyActivity{{Date: "2026-07-16", Observed: 30 * time.Minute}}}
	p := NewPalworldProvider(guard, daily, fakeStatusSource{domain.PollStatus{LastSuccess: now}}, time.UTC, 15*time.Second)
	p.now = func() time.Time { return now }

	got, err := p.Presentation(t.Context(), "palworld", " steam_1 ")
	if err != nil {
		t.Fatalf("Presentation() error = %v", err)
	}
	if got.Schema != PresentationSchemaV1 || got.SourceStatus != "online" || got.Map == nil {
		t.Fatalf("presentation header = %#v", got)
	}
	if len(got.Fields) != len(palworldPresentationFieldIDs) {
		t.Fatalf("field count = %d, want %d", len(got.Fields), len(palworldPresentationFieldIDs))
	}
	for i, want := range palworldPresentationFieldIDs {
		if got.Fields[i].ID != want {
			t.Errorf("Fields[%d].ID = %q, want %q", i, got.Fields[i].ID, want)
		}
	}
	assertDisplayField(t, got.Fields, "identity.account", "text", true, "account_1", "normal")
	assertDisplayField(t, got.Fields, "identity.uid", "text", true, "steam_1", "normal")
	assertDisplayField(t, got.Fields, "identity.level", "integer", true, float64(42), "normal")
	assertDisplayField(t, got.Fields, "presence.status", "status", true, "online", "normal")
	assertDisplayField(t, got.Fields, "presence.last_online", "timestamp", true, lastOnline.Format(time.RFC3339Nano), "muted")
	assertDisplayField(t, got.Fields, "network.latency", "latency_ms", true, 38.5, "normal")
	assertDisplayField(t, got.Fields, "activity.today", "duration_ms", true, float64((30 * time.Minute).Milliseconds()), "normal")
	assertDisplayField(t, got.Fields, "policy.strategy", "text", true, "Fixed window", "normal")
	assertDisplayField(t, got.Fields, "policy.enforcement", "status", true, "Pending", "warning")
	remaining := displayFieldByID(t, got.Fields, "policy.remaining")
	if remaining.Tone != "warning" || remaining.Progress == nil || *remaining.Progress != 0.25 {
		t.Errorf("policy.remaining = %#v", remaining)
	}
	if err := ValidatePresentation(got); err != nil {
		t.Fatalf("ValidatePresentation() error = %v", err)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"ip", "password", "credential", "private_samples", "warnings", "revision", "attempts"} {
		if strings.Contains(strings.ToLower(string(data)), forbidden) {
			t.Errorf("presentation leaked forbidden data %q: %s", forbidden, data)
		}
	}
}

func TestPalworldPresentationAvailabilityAndPolicyStates(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name              string
		snapshot          domain.PlayerSnapshot
		lastSuccess       time.Time
		wantSource        string
		wantPresence      string
		wantPolicy        bool
		wantRemaining     string
		wantRemainingMS   int64
		wantRemainingTone string
		wantEnforcement   string
		wantEnforceTone   string
	}{
		{name: "offline normal", snapshot: domain.PlayerSnapshot{Player: domain.Player{LastOnline: now.Add(-time.Hour)}, Policy: domain.ResolvedPolicy{Enabled: true, Strategy: "credit"}, Period: domain.Period{End: now.Add(time.Hour)}, Remaining: 2 * time.Hour}, lastSuccess: now, wantSource: "offline", wantPresence: "offline", wantPolicy: true, wantRemaining: "Available credit", wantRemainingMS: (2 * time.Hour).Milliseconds(), wantRemainingTone: "normal", wantEnforcement: "Inactive", wantEnforceTone: "normal"},
		{name: "unknown stale danger", snapshot: domain.PlayerSnapshot{Online: true, Policy: domain.ResolvedPolicy{Enabled: true, Strategy: "fixed_window"}, Period: domain.Period{End: now.Add(time.Hour)}, Remaining: 0, Enforcement: domain.EnforcementState{Status: "failure"}}, lastSuccess: now.Add(-time.Minute), wantSource: "unknown", wantPresence: "unknown", wantPolicy: true, wantRemaining: "Remaining", wantRemainingTone: "danger", wantEnforcement: "Failed", wantEnforceTone: "danger"},
		{name: "disabled", snapshot: domain.PlayerSnapshot{Policy: domain.ResolvedPolicy{Enabled: false, Strategy: "fixed_window"}}, lastSuccess: now, wantSource: "offline", wantPresence: "offline"},
		{name: "exempt", snapshot: domain.PlayerSnapshot{Policy: domain.ResolvedPolicy{Enabled: true, Exempt: true, Strategy: "fixed_window"}}, lastSuccess: now, wantSource: "offline", wantPresence: "offline"},
		{name: "cooldown rest", snapshot: domain.PlayerSnapshot{Policy: domain.ResolvedPolicy{Enabled: true, Strategy: "cooldown"}, Period: domain.Period{End: now.Add(15 * time.Minute)}, Used: 30 * time.Minute, Remaining: 0}, lastSuccess: now, wantSource: "offline", wantPresence: "offline", wantPolicy: true, wantRemaining: "Rest remaining", wantRemainingMS: (15 * time.Minute).Milliseconds(), wantRemainingTone: "danger", wantEnforcement: "Inactive", wantEnforceTone: "normal"},
		{name: "enforced success", snapshot: domain.PlayerSnapshot{Online: true, Policy: domain.ResolvedPolicy{Enabled: true, Strategy: "fixed_window"}, Period: domain.Period{End: now.Add(time.Hour)}, Remaining: -time.Minute, Enforcement: domain.EnforcementState{Status: "success"}}, lastSuccess: now, wantSource: "online", wantPresence: "online", wantPolicy: true, wantRemaining: "Remaining", wantRemainingMS: (-time.Minute).Milliseconds(), wantRemainingTone: "danger", wantEnforcement: "Enforced", wantEnforceTone: "danger"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPalworldProvider(&fakeSnapshotSource{snapshot: tt.snapshot}, &fakeDailySource{}, fakeStatusSource{domain.PollStatus{LastSuccess: tt.lastSuccess}}, time.UTC, 30*time.Second)
			p.now = func() time.Time { return now }
			got, err := p.Presentation(t.Context(), "palworld", "u")
			if err != nil {
				t.Fatal(err)
			}
			if got.SourceStatus != tt.wantSource {
				t.Errorf("SourceStatus = %q, want %q", got.SourceStatus, tt.wantSource)
			}
			presenceTone := "normal"
			if tt.wantPresence == "unknown" {
				presenceTone = "muted"
			}
			assertDisplayField(t, got.Fields, "presence.status", "status", true, tt.wantPresence, presenceTone)
			for _, id := range []string{"network.latency", "location.coordinates"} {
				if field := displayFieldByID(t, got.Fields, id); field.Available != (tt.wantSource == "online") {
					t.Errorf("%s available = %v", id, field.Available)
				}
			}
			for _, id := range []string{"policy.strategy", "policy.cycle_used", "policy.remaining", "policy.period_end", "policy.enforcement"} {
				field := displayFieldByID(t, got.Fields, id)
				if field.Available != tt.wantPolicy {
					t.Errorf("%s available = %v, want %v", id, field.Available, tt.wantPolicy)
				}
				if !field.Available && (len(field.Value) != 0 || field.Progress != nil) {
					t.Errorf("unavailable %s carries value/progress: %#v", id, field)
				}
			}
			if tt.wantPolicy {
				remaining := displayFieldByID(t, got.Fields, "policy.remaining")
				if remaining.Label != tt.wantRemaining || remaining.Tone != tt.wantRemainingTone {
					t.Errorf("policy.remaining = %#v", remaining)
				}
				if gotMS := decodeFieldValue[float64](t, remaining); int64(gotMS) != tt.wantRemainingMS {
					t.Errorf("remaining value = %v, want %d", gotMS, tt.wantRemainingMS)
				}
				assertDisplayField(t, got.Fields, "policy.enforcement", "status", true, tt.wantEnforcement, tt.wantEnforceTone)
			}
			if err := ValidatePresentation(got); err != nil {
				t.Errorf("ValidatePresentation() error = %v", err)
			}
		})
	}
}

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

func TestPalworldProviderSetLocationUpdatesCalendarBoundaries(t *testing.T) {
	now := time.Date(2026, 7, 13, 1, 0, 0, 0, time.UTC)
	daily := &fakeDailySource{}
	p := NewPalworldProvider(&fakeSnapshotSource{}, daily, fakeStatusSource{}, time.UTC, time.Minute)
	p.now = func() time.Time { return now }

	if _, err := p.Snapshot(t.Context(), "palworld", "u"); err != nil {
		t.Fatal(err)
	}
	if daily.startDate != "2026-07-13" || daily.endDate != "2026-07-14" {
		t.Fatalf("UTC date range=[%s,%s)", daily.startDate, daily.endDate)
	}

	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetLocation(losAngeles); err != nil {
		t.Fatalf("SetLocation() error=%v", err)
	}
	if _, err := p.Snapshot(t.Context(), "palworld", "u"); err != nil {
		t.Fatal(err)
	}
	if daily.startDate != "2026-07-06" || daily.endDate != "2026-07-13" {
		t.Fatalf("Los Angeles date range=[%s,%s), want [2026-07-06,2026-07-13)", daily.startDate, daily.endDate)
	}

	if err := p.SetLocation(nil); err == nil {
		t.Fatal("SetLocation(nil) error=nil")
	}
	if _, err := p.Snapshot(t.Context(), "palworld", "u"); err != nil {
		t.Fatal(err)
	}
	if daily.startDate != "2026-07-06" || daily.endDate != "2026-07-13" {
		t.Fatalf("nil update changed date range=[%s,%s)", daily.startDate, daily.endDate)
	}
}

func TestPalworldProviderLocationIsRaceSafe(t *testing.T) {
	p := NewPalworldProvider(&fakeSnapshotSource{}, &fakeDailySource{}, fakeStatusSource{}, time.UTC, time.Minute)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			location := time.UTC
			if i%2 == 0 {
				location = time.FixedZone("alternate", 14*60*60)
			}
			if err := p.SetLocation(location); err != nil {
				panic(err)
			}
		}
	}()
	for i := 0; i < 1000; i++ {
		if _, err := p.Snapshot(t.Context(), "palworld", "u"); err != nil {
			t.Fatal(err)
		}
	}
	<-done
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

func displayFieldByID(t *testing.T, fields []DisplayField, id string) DisplayField {
	t.Helper()
	for _, field := range fields {
		if field.ID == id {
			return field
		}
	}
	t.Fatalf("display field %q not found in %#v", id, fields)
	return DisplayField{}
}

func assertDisplayField(t *testing.T, fields []DisplayField, id, kind string, available bool, wantValue any, tone string) DisplayField {
	t.Helper()
	field := displayFieldByID(t, fields, id)
	if field.Kind != kind || field.Available != available || field.Tone != tone {
		t.Errorf("display field %q = %#v, want kind=%q available=%v tone=%q", id, field, kind, available, tone)
	}
	if available {
		var gotValue any
		if err := json.Unmarshal(field.Value, &gotValue); err != nil {
			t.Fatalf("decode field %q value: %v", id, err)
		}
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Errorf("display field %q value = %#v, want %#v", id, gotValue, wantValue)
		}
	}
	return field
}

func decodeFieldValue[T any](t *testing.T, field DisplayField) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(field.Value, &value); err != nil {
		t.Fatalf("decode field %q value: %v", field.ID, err)
	}
	return value
}
