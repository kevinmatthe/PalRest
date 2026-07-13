package poller

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/guard"
)

type fakeClient struct {
	players   []domain.Player
	listErr   error
	announces []string
	kicks     []string
}

func (f *fakeClient) ListPlayers(context.Context) ([]domain.Player, error) {
	return f.players, f.listErr
}
func (f *fakeClient) Announce(_ context.Context, message string) error {
	f.announces = append(f.announces, message)
	return nil
}
func (f *fakeClient) Kick(_ context.Context, userID, message string) error {
	f.kicks = append(f.kicks, userID+":"+message)
	return nil
}

type fakeGuard struct {
	decisions  guard.Decisions
	observeErr error
	failed     int
	warnings   int
	kicks      int
	observed   int
	order      *[]string
}

func (f *fakeGuard) Observe(context.Context, time.Time, []domain.Player) (guard.Decisions, error) {
	f.observed++
	if f.order != nil {
		*f.order = append(*f.order, "guard")
	}
	return f.decisions, f.observeErr
}
func (f *fakeGuard) PollFailed() { f.failed++ }
func (f *fakeGuard) RecordWarningResult(context.Context, guard.WarningDecision, error, time.Time) error {
	f.warnings++
	return nil
}
func (f *fakeGuard) RecordKickResult(context.Context, guard.KickDecision, error, time.Time) error {
	f.kicks++
	return nil
}

type fakeAnalytics struct {
	err      error
	observed int
	order    *[]string
	location *time.Location
}

type fakePlayerObserver struct {
	err      error
	observed int
	order    *[]string
	ids      []string
}

func (f *fakePlayerObserver) Observe(_ context.Context, _ time.Time, _ []domain.Player, correlationID string) error {
	f.observed++
	f.ids = append(f.ids, correlationID)
	if f.order != nil {
		*f.order = append(*f.order, "player observation")
	}
	return f.err
}

type fakeServerReader struct {
	metricsErr    error
	infoErr       error
	settingsErr   error
	metricsCalls  int
	infoCalls     int
	settingsCalls int
	metrics       domain.ServerMetrics
	info          domain.ServerInfo
	settings      domain.ServerSettings
}

func (f *fakeServerReader) Metrics(context.Context) (domain.ServerMetrics, error) {
	f.metricsCalls++
	return f.metrics, f.metricsErr
}

func (f *fakeServerReader) Info(context.Context) (domain.ServerInfo, error) {
	f.infoCalls++
	return f.info, f.infoErr
}

func (f *fakeServerReader) Settings(context.Context) (domain.ServerSettings, error) {
	f.settingsCalls++
	return f.settings, f.settingsErr
}

type fakeServerRecorder struct {
	metricsErr    error
	infoErr       error
	settingsErr   error
	metricsCalls  int
	infoCalls     int
	settingsCalls int
}

func (f *fakeServerRecorder) RecordMetrics(_ context.Context, at time.Time, _ domain.ServerMetrics) error {
	f.metricsCalls++
	return f.metricsErr
}

func (f *fakeServerRecorder) RecordInfo(_ context.Context, at time.Time, _ domain.ServerInfo) error {
	f.infoCalls++
	return f.infoErr
}

func (f *fakeServerRecorder) RecordSettings(_ context.Context, at time.Time, _ domain.ServerSettings) error {
	f.settingsCalls++
	return f.settingsErr
}

func (f *fakeAnalytics) Observe(context.Context, time.Time, []domain.Player) error {
	f.observed++
	if f.order != nil {
		*f.order = append(*f.order, "analytics")
	}
	return f.err
}
func (f *fakeAnalytics) SetLocation(location *time.Location) error {
	f.location = location
	if f.order != nil {
		*f.order = append(*f.order, "location")
	}
	return nil
}

func startSamplerForTest(t *testing.T, p *Poller) <-chan struct{} {
	t.Helper()
	completed, stop := p.startServerSampler(t.Context())
	t.Cleanup(stop)
	return completed
}

func TestRunOnceOrdersAnalyticsPlayerObservationThenGuard(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	order := []string{}
	observer := &fakePlayerObserver{order: &order}
	p, err := New(
		&fakeClient{players: []domain.Player{{UserID: "steam_1"}}},
		&fakeGuard{order: &order},
		&fakeAnalytics{order: &order},
		time.Minute, "warning", "kick", func() time.Time { return now },
		WithPlayerObserver(observer),
		WithCorrelationIDGenerator(func() string { return "poll-1" }),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "analytics,player observation,guard" {
		t.Fatalf("order=%s", got)
	}
	if len(observer.ids) != 1 || observer.ids[0] != "poll-1" {
		t.Fatalf("correlation IDs=%v", observer.ids)
	}
}

func TestPlayerObservationFailureDoesNotAdvanceStatusOrRunGuard(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	client := &fakeClient{players: []domain.Player{{UserID: "steam_1"}}}
	observer := &fakePlayerObserver{}
	guardService := &fakeGuard{}
	p, err := New(client, guardService, &fakeAnalytics{}, time.Minute, "warning", "kick", func() time.Time { return now }, WithPlayerObserver(observer))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	previous := p.Status()
	now = now.Add(time.Minute)
	client.players = append(client.players, domain.Player{UserID: "steam_2"})
	observer.err = errors.New("player observation unavailable")
	if err := p.RunOnce(t.Context()); !errors.Is(err, observer.err) {
		t.Fatalf("error=%v", err)
	}
	status := p.Status()
	if guardService.observed != 1 {
		t.Fatalf("guard observations=%d", guardService.observed)
	}
	if guardService.failed != 1 {
		t.Fatalf("guard failed calls=%d", guardService.failed)
	}
	if observer.observed != 2 {
		t.Fatalf("player observations=%d", observer.observed)
	}
	if status.LastError == "" || !status.LastSuccess.Equal(previous.LastSuccess) || status.OnlineCount != previous.OnlineCount {
		t.Fatalf("previous=%+v status=%+v", previous, status)
	}
}

func TestDefaultCorrelationIDsAreNonemptyAndDistinct(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	observer := &fakePlayerObserver{}
	p, err := New(&fakeClient{}, &fakeGuard{}, &fakeAnalytics{}, time.Minute, "warning", "kick", func() time.Time { return now }, WithPlayerObserver(observer))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(observer.ids) != 2 || strings.TrimSpace(observer.ids[0]) == "" || observer.ids[0] == observer.ids[1] {
		t.Fatalf("correlation IDs=%v", observer.ids)
	}
	decoded, err := hex.DecodeString(observer.ids[0])
	if err != nil || len(decoded) != 16 {
		t.Fatalf("correlation ID %q is not 128-bit hex: bytes=%d err=%v", observer.ids[0], len(decoded), err)
	}
}

func TestEmptyCorrelationIDStopsBeforeAnalyticsPersistence(t *testing.T) {
	analytics := &fakeAnalytics{}
	observer := &fakePlayerObserver{}
	guardService := &fakeGuard{}
	p, err := New(&fakeClient{}, guardService, analytics, time.Minute, "warning", "kick", time.Now,
		WithPlayerObserver(observer), WithCorrelationIDGenerator(func() string { return " " }))
	if err != nil {
		t.Fatal(err)
	}

	if err := p.RunOnce(t.Context()); err == nil {
		t.Fatal("expected empty correlation ID error")
	}
	if analytics.observed != 0 || observer.observed != 0 || guardService.observed != 0 {
		t.Fatalf("analytics=%d player=%d guard=%d", analytics.observed, observer.observed, guardService.observed)
	}
}

func TestCorrelationIDGenerationFailureStopsBeforeAnalyticsPersistence(t *testing.T) {
	analytics := &fakeAnalytics{}
	observer := &fakePlayerObserver{}
	guardService := &fakeGuard{}
	p, err := New(&fakeClient{}, guardService, analytics, time.Minute, "warning", "kick", time.Now, WithPlayerObserver(observer))
	if err != nil {
		t.Fatal(err)
	}
	p.correlationID = func() (string, error) { return "", errors.New("random source unavailable") }

	if err := p.RunOnce(t.Context()); err == nil {
		t.Fatal("expected correlation ID generation error")
	}
	if analytics.observed != 0 || observer.observed != 0 || guardService.observed != 0 {
		t.Fatalf("analytics=%d player=%d guard=%d", analytics.observed, observer.observed, guardService.observed)
	}
}

func TestMetricsFailureDoesNotAffectSuccessfulPlayerPoll(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	reader := &fakeServerReader{metricsErr: errors.New("metrics unavailable")}
	recorder := &fakeServerRecorder{}
	guardService := &fakeGuard{}
	p, err := New(&fakeClient{players: []domain.Player{{UserID: "steam_1"}}}, guardService, &fakeAnalytics{}, time.Minute, "warning", "kick", func() time.Time { return now }, WithServerObservations(reader, recorder))
	if err != nil {
		t.Fatal(err)
	}
	completed := startSamplerForTest(t, p)

	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, completed, "server sample completion")
	status := p.Status()
	if guardService.observed != 1 || !status.LastSuccess.Equal(now) || status.LastError != "" {
		t.Fatalf("guard=%d status=%+v", guardService.observed, status)
	}
	if reader.metricsCalls != 1 || recorder.metricsCalls != 0 {
		t.Fatalf("metrics reads=%d records=%d", reader.metricsCalls, recorder.metricsCalls)
	}
}

func TestPlayersFailureStillSamplesMetricsButSkipsCriticalObservers(t *testing.T) {
	reader := &fakeServerReader{}
	recorder := &fakeServerRecorder{}
	analytics := &fakeAnalytics{}
	playerObserver := &fakePlayerObserver{}
	guardService := &fakeGuard{}
	p, err := New(&fakeClient{listErr: errors.New("players unavailable")}, guardService, analytics, time.Minute, "warning", "kick", time.Now,
		WithPlayerObserver(playerObserver), WithServerObservations(reader, recorder))
	if err != nil {
		t.Fatal(err)
	}
	completed := startSamplerForTest(t, p)

	if err := p.RunOnce(t.Context()); err == nil {
		t.Fatal("expected player poll error")
	}
	awaitSignal(t, completed, "server sample completion")
	if analytics.observed != 0 || playerObserver.observed != 0 || guardService.observed != 0 {
		t.Fatalf("analytics=%d player=%d guard=%d", analytics.observed, playerObserver.observed, guardService.observed)
	}
	if reader.metricsCalls != 1 || recorder.metricsCalls != 1 {
		t.Fatalf("metrics reads=%d records=%d", reader.metricsCalls, recorder.metricsCalls)
	}
}

func TestServerObservationCadenceUsesPollClock(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	reader := &fakeServerReader{}
	recorder := &fakeServerRecorder{}
	p, err := New(&fakeClient{}, &fakeGuard{}, &fakeAnalytics{}, time.Minute, "warning", "kick", func() time.Time { return now }, WithServerObservations(reader, recorder))
	if err != nil {
		t.Fatal(err)
	}
	completed := startSamplerForTest(t, p)

	for _, advance := range []time.Duration{0, time.Minute, 4 * time.Minute} {
		now = now.Add(advance)
		if err := p.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		awaitSignal(t, completed, "server sample completion")
	}
	if reader.metricsCalls != 3 || reader.infoCalls != 2 || reader.settingsCalls != 2 {
		t.Fatalf("metrics=%d info=%d settings=%d", reader.metricsCalls, reader.infoCalls, reader.settingsCalls)
	}
	if recorder.metricsCalls != 3 || recorder.infoCalls != 2 || recorder.settingsCalls != 2 {
		t.Fatalf("recorded metrics=%d info=%d settings=%d", recorder.metricsCalls, recorder.infoCalls, recorder.settingsCalls)
	}
}

func TestInfoAndSettingsFailuresAreIsolated(t *testing.T) {
	tests := []struct {
		name     string
		infoErr  error
		settings error
	}{
		{name: "info request", infoErr: errors.New("info unavailable")},
		{name: "settings request", settings: errors.New("settings unavailable")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeServerReader{infoErr: tt.infoErr, settingsErr: tt.settings}
			recorder := &fakeServerRecorder{}
			p, err := New(&fakeClient{}, &fakeGuard{}, &fakeAnalytics{}, time.Minute, "warning", "kick", time.Now, WithServerObservations(reader, recorder))
			if err != nil {
				t.Fatal(err)
			}
			completed := startSamplerForTest(t, p)
			if err := p.RunOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			awaitSignal(t, completed, "server sample completion")
			if reader.infoCalls != 1 || reader.settingsCalls != 1 {
				t.Fatalf("info=%d settings=%d", reader.infoCalls, reader.settingsCalls)
			}
			if tt.infoErr != nil && recorder.settingsCalls != 1 {
				t.Fatalf("settings records=%d", recorder.settingsCalls)
			}
			if tt.settings != nil && recorder.infoCalls != 1 {
				t.Fatalf("info records=%d", recorder.infoCalls)
			}
		})
	}
}

func TestOptionalRecorderFailuresDoNotAffectPlayerPoll(t *testing.T) {
	reader := &fakeServerReader{}
	recorder := &fakeServerRecorder{
		metricsErr: errors.New("record metrics"), infoErr: errors.New("record info"), settingsErr: errors.New("record settings"),
	}
	guardService := &fakeGuard{}
	p, err := New(&fakeClient{}, guardService, &fakeAnalytics{}, time.Minute, "warning", "kick", time.Now, WithServerObservations(reader, recorder))
	if err != nil {
		t.Fatal(err)
	}
	completed := startSamplerForTest(t, p)

	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, completed, "server sample completion")
	if guardService.observed != 1 || p.Status().LastError != "" {
		t.Fatalf("guard=%d status=%+v", guardService.observed, p.Status())
	}
	if recorder.metricsCalls != 1 || recorder.infoCalls != 1 || recorder.settingsCalls != 1 {
		t.Fatalf("metrics=%d info=%d settings=%d", recorder.metricsCalls, recorder.infoCalls, recorder.settingsCalls)
	}
}

func TestNilOptionalCollaboratorsAreSafe(t *testing.T) {
	p, err := New(&fakeClient{}, &fakeGuard{}, &fakeAnalytics{}, time.Minute, "warning", "kick", time.Now,
		WithPlayerObserver(nil), WithServerObservations(nil, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestApplyPolicyTimezoneExcludesPollCycleAndOrdersUpdateFirst(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	order := []string{}
	p, _ := New(&fakeClient{}, &fakeGuard{}, &fakeAnalytics{order: &order}, time.Minute, "warning", "kick", time.Now)
	done := make(chan error, 1)
	go func() {
		done <- p.ApplyPolicyTimezone(func() error { order = append(order, "update"); close(entered); <-release; return nil }, time.UTC)
	}()
	<-entered
	pollDone := make(chan error, 1)
	go func() { pollDone <- p.RunOnce(t.Context()) }()
	select {
	case err := <-pollDone:
		t.Fatalf("poll overlapped transition: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-pollDone; err != nil {
		t.Fatal(err)
	}
	if len(order) < 2 || order[0] != "update" || order[1] != "location" {
		t.Fatalf("order=%v", order)
	}
}

func TestApplyPolicyTimezoneFailureDoesNotChangeLocation(t *testing.T) {
	analytics := &fakeAnalytics{}
	p, _ := New(&fakeClient{}, &fakeGuard{}, analytics, time.Minute, "warning", "kick", time.Now)
	want := errors.New("save failed")
	if err := p.ApplyPolicyTimezone(func() error { return want }, time.UTC); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
	if analytics.location != nil {
		t.Fatalf("location=%v", analytics.location)
	}
}

func TestRunOncePerformsAndRecordsDecisions(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	client := &fakeClient{players: []domain.Player{{UserID: "steam_1", Name: "Kevin"}}}
	order := []string{}
	analytics := &fakeAnalytics{order: &order}
	guardService := &fakeGuard{decisions: guard.Decisions{
		Warnings: []guard.WarningDecision{{UserID: "steam_1", PlayerName: "Kevin", Remaining: 5 * time.Minute}},
		Kicks:    []guard.KickDecision{{UserID: "steam_2", PlayerName: "Matt", ResetAt: now.Add(time.Hour)}},
	}, order: &order}
	p, err := New(client, guardService, analytics, time.Minute, "{{ .PlayerName }}: {{ .Remaining }}", "reset {{ .ResetAt }}", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(client.announces) != 1 || !strings.Contains(client.announces[0], "Kevin") || len(client.kicks) != 1 {
		t.Fatalf("announces=%v kicks=%v", client.announces, client.kicks)
	}
	if guardService.warnings != 1 || guardService.kicks != 1 {
		t.Fatalf("recorded warnings=%d kicks=%d", guardService.warnings, guardService.kicks)
	}
	if strings.Join(order, ",") != "analytics,guard" {
		t.Fatalf("observation order=%v", order)
	}
	status := p.Status()
	if status.OnlineCount != 1 || !status.LastSuccess.Equal(now) {
		t.Fatalf("status=%+v", status)
	}
}

func TestListFailureBreaksContinuityAndHasNoSideEffects(t *testing.T) {
	client := &fakeClient{listErr: errors.New("offline")}
	analytics := &fakeAnalytics{}
	guardService := &fakeGuard{decisions: guard.Decisions{Kicks: []guard.KickDecision{{UserID: "steam_1"}}}}
	p, _ := New(client, guardService, analytics, time.Minute, "warning", "kick", time.Now)
	if err := p.RunOnce(t.Context()); err == nil {
		t.Fatal("expected error")
	}
	if guardService.failed != 1 || len(client.kicks) != 0 || p.Status().LastError == "" {
		t.Fatalf("failed=%d kicks=%v status=%+v", guardService.failed, client.kicks, p.Status())
	}
	if analytics.observed != 0 || guardService.observed != 0 {
		t.Fatalf("analytics observed=%d guard observed=%d", analytics.observed, guardService.observed)
	}
}

func TestAnalyticsFailureBreaksContinuityAndSkipsGuard(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	client := &fakeClient{players: []domain.Player{{UserID: "steam_1", Name: "Kevin"}}}
	analytics := &fakeAnalytics{err: errors.New("analytics unavailable")}
	guardService := &fakeGuard{decisions: guard.Decisions{Kicks: []guard.KickDecision{{UserID: "steam_1"}}}}
	p, _ := New(client, guardService, analytics, time.Minute, "warning", "kick", func() time.Time { return now })

	if err := p.RunOnce(t.Context()); !errors.Is(err, analytics.err) {
		t.Fatalf("error=%v", err)
	}
	status := p.Status()
	if analytics.observed != 1 || guardService.observed != 0 || guardService.failed != 1 || len(client.kicks) != 0 {
		t.Fatalf("analytics=%d guard=%d failed=%d kicks=%v", analytics.observed, guardService.observed, guardService.failed, client.kicks)
	}
	if status.LastError == "" || !status.LastSuccess.IsZero() || status.OnlineCount != 0 {
		t.Fatalf("status=%+v", status)
	}
}

func TestObservationFailureHasNoSideEffects(t *testing.T) {
	client := &fakeClient{}
	guardService := &fakeGuard{observeErr: errors.New("sqlite unavailable"), decisions: guard.Decisions{Kicks: []guard.KickDecision{{UserID: "steam_1"}}}}
	p, _ := New(client, guardService, &fakeAnalytics{}, time.Minute, "warning", "kick", time.Now)
	if err := p.RunOnce(t.Context()); err == nil {
		t.Fatal("expected error")
	}
	if len(client.kicks) != 0 {
		t.Fatalf("kicks=%v", client.kicks)
	}
}

func TestRunNeverOverlapsSlowCycles(t *testing.T) {
	client := &blockingClient{entered: make(chan struct{}, 2), release: make(chan struct{}, 2)}
	guardService := &fakeGuard{}
	p, _ := New(client, guardService, &fakeAnalytics{}, 5*time.Millisecond, "warning", "kick", time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	<-client.entered
	time.Sleep(20 * time.Millisecond)
	client.mu.Lock()
	max := client.maxConcurrent
	client.mu.Unlock()
	if max != 1 {
		t.Fatalf("max concurrent=%d", max)
	}
	client.release <- struct{}{}
	cancel()
	<-done
}

func TestConcurrentRunOnceCallsAreSerialized(t *testing.T) {
	client := &blockingClient{entered: make(chan struct{}, 2), release: make(chan struct{}, 2)}
	p, err := New(client, &fakeGuard{}, &fakeAnalytics{}, time.Minute, "warning", "kick", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- p.RunOnce(t.Context()) }()
	<-client.entered

	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondDone <- p.RunOnce(t.Context())
	}()
	<-secondStarted
	select {
	case <-client.entered:
		t.Fatal("concurrent RunOnce entered the player request")
	case <-time.After(30 * time.Millisecond):
	}

	client.release <- struct{}{}
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	<-client.entered
	client.release <- struct{}{}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestRunAllowsActiveCycleToFinishAfterStop(t *testing.T) {
	client := &blockingClient{entered: make(chan struct{}, 1), release: make(chan struct{}, 1)}
	p, _ := New(client, &fakeGuard{}, &fakeAnalytics{}, time.Minute, "warning", "kick", time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	<-client.entered
	cancel()
	select {
	case <-done:
		t.Fatal("active cycle was canceled instead of finishing")
	case <-time.After(20 * time.Millisecond):
	}
	client.release <- struct{}{}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("poller did not stop after active cycle finished")
	}
}

func TestApplyConfigWaitsForActiveCycle(t *testing.T) {
	client := &blockingClient{entered: make(chan struct{}, 1), release: make(chan struct{}, 1)}
	p, _ := New(client, &fakeGuard{}, &fakeAnalytics{}, time.Minute, "warning", "kick", time.Now)
	cycleDone := make(chan error, 1)
	go func() { cycleDone <- p.RunOnce(context.Background()) }()
	<-client.entered
	applied := make(chan error, 1)
	go func() {
		applied <- p.ApplyConfig(func() error { return nil }, "new warning", "new kick")
	}()
	select {
	case <-applied:
		t.Fatal("configuration changed during active cycle")
	case <-time.After(20 * time.Millisecond):
	}
	client.release <- struct{}{}
	if err := <-cycleDone; err != nil {
		t.Fatal(err)
	}
	if err := <-applied; err != nil {
		t.Fatal(err)
	}
}

type blockingClient struct {
	mu            sync.Mutex
	concurrent    int
	maxConcurrent int
	entered       chan struct{}
	release       chan struct{}
}

func (b *blockingClient) ListPlayers(ctx context.Context) ([]domain.Player, error) {
	b.mu.Lock()
	b.concurrent++
	if b.concurrent > b.maxConcurrent {
		b.maxConcurrent = b.concurrent
	}
	b.mu.Unlock()
	b.entered <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	b.mu.Lock()
	b.concurrent--
	b.mu.Unlock()
	return nil, ctx.Err()
}
func (*blockingClient) Announce(context.Context, string) error     { return nil }
func (*blockingClient) Kick(context.Context, string, string) error { return nil }
