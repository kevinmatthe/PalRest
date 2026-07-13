package poller

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/guard"
)

type blockingServerReader struct {
	metricsStarted  chan struct{}
	infoStarted     chan struct{}
	settingsStarted chan struct{}
	metricsStopped  chan struct{}
	metricsRelease  chan struct{}
	blockInfo       bool
	blockSettings   bool
	metricsCalls    atomic.Int32
}

func newBlockingServerReader() *blockingServerReader {
	return &blockingServerReader{
		metricsStarted: make(chan struct{}, 20), infoStarted: make(chan struct{}, 20),
		settingsStarted: make(chan struct{}, 20), metricsStopped: make(chan struct{}, 20),
		metricsRelease: make(chan struct{}),
	}
}

func (r *blockingServerReader) Metrics(ctx context.Context) (domain.ServerMetrics, error) {
	r.metricsCalls.Add(1)
	r.metricsStarted <- struct{}{}
	select {
	case <-r.metricsRelease:
		return domain.ServerMetrics{}, nil
	case <-ctx.Done():
		r.metricsStopped <- struct{}{}
		return domain.ServerMetrics{}, ctx.Err()
	}
}

func (r *blockingServerReader) Info(ctx context.Context) (domain.ServerInfo, error) {
	r.infoStarted <- struct{}{}
	if r.blockInfo {
		<-ctx.Done()
		return domain.ServerInfo{}, ctx.Err()
	}
	return domain.ServerInfo{}, nil
}

func (r *blockingServerReader) Settings(ctx context.Context) (domain.ServerSettings, error) {
	r.settingsStarted <- struct{}{}
	if r.blockSettings {
		<-ctx.Done()
		return domain.ServerSettings{}, ctx.Err()
	}
	return domain.ServerSettings{}, nil
}

type noOpServerRecorder struct{}

func (noOpServerRecorder) RecordMetrics(context.Context, time.Time, domain.ServerMetrics) error {
	return nil
}
func (noOpServerRecorder) RecordInfo(context.Context, time.Time, domain.ServerInfo) error {
	return nil
}
func (noOpServerRecorder) RecordSettings(context.Context, time.Time, domain.ServerSettings) error {
	return nil
}

type firstMetricsBlockingReader struct {
	firstStarted chan struct{}
	release      chan struct{}
	calls        atomic.Int32
}

func (r *firstMetricsBlockingReader) Metrics(context.Context) (domain.ServerMetrics, error) {
	if r.calls.Add(1) == 1 {
		close(r.firstStarted)
		<-r.release
	}
	return domain.ServerMetrics{}, nil
}

func (*firstMetricsBlockingReader) Info(context.Context) (domain.ServerInfo, error) {
	return domain.ServerInfo{}, nil
}

func (*firstMetricsBlockingReader) Settings(context.Context) (domain.ServerSettings, error) {
	return domain.ServerSettings{}, nil
}

type timestampRecorder struct {
	metrics              chan time.Time
	info                 chan time.Time
	settings             chan time.Time
	metricsCalls         atomic.Int32
	secondMetricsRelease chan struct{}
}

func (r *timestampRecorder) RecordMetrics(ctx context.Context, at time.Time, _ domain.ServerMetrics) error {
	r.metrics <- at
	if r.metricsCalls.Add(1) == 2 {
		select {
		case <-r.secondMetricsRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (r *timestampRecorder) RecordInfo(_ context.Context, at time.Time, _ domain.ServerInfo) error {
	r.info <- at
	return nil
}

func (r *timestampRecorder) RecordSettings(_ context.Context, at time.Time, _ domain.ServerSettings) error {
	r.settings <- at
	return nil
}

func newAsyncSamplerPoller(t *testing.T, reader ServerReader, interval time.Duration, guardService Guard) *Poller {
	t.Helper()
	p, err := New(&fakeClient{}, guardService, &fakeAnalytics{}, interval, "warning", "kick", time.Now,
		WithServerObservations(reader, noOpServerRecorder{}), WithServerObservationTimeout(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func awaitSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func awaitTimestamp(t *testing.T, timestamps <-chan time.Time, name string) time.Time {
	t.Helper()
	select {
	case at := <-timestamps:
		return at
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
		return time.Time{}
	}
}

func TestBlockedOptionalSamplingDoesNotDelayRunOnceOrApplyConfig(t *testing.T) {
	reader := newBlockingServerReader()
	reader.blockInfo = true
	reader.blockSettings = true
	p := newAsyncSamplerPoller(t, reader, time.Minute, &fakeGuard{})
	ctx, cancel := context.WithCancel(t.Context())
	_, stop := p.startServerSampler(ctx)
	defer stop()

	runDone := make(chan error, 1)
	go func() { runDone <- p.RunOnce(t.Context()) }()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("RunOnce waited for optional sampling")
	}
	awaitSignal(t, reader.metricsStarted, "metrics start")

	applyDone := make(chan error, 1)
	go func() { applyDone <- p.ApplyConfig(func() error { return nil }, "warning", "kick") }()
	select {
	case err := <-applyDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ApplyConfig waited for optional sampling")
	}
	policyDone := make(chan error, 1)
	go func() { policyDone <- p.ApplyPolicyTimezone(func() error { return nil }, time.UTC) }()
	select {
	case err := <-policyDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ApplyPolicyTimezone waited for optional sampling")
	}
	cancel()
}

func TestBlockedMetricsDoesNotStarveInfoOrSettings(t *testing.T) {
	reader := newBlockingServerReader()
	p := newAsyncSamplerPoller(t, reader, time.Minute, &fakeGuard{})
	ctx, cancel := context.WithCancel(t.Context())
	_, stop := p.startServerSampler(ctx)
	defer stop()
	defer cancel()

	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, reader.metricsStarted, "metrics start")
	awaitSignal(t, reader.infoStarted, "info start")
	awaitSignal(t, reader.settingsStarted, "settings start")
}

type notifyingGuard struct {
	fakeGuard
	observations chan struct{}
}

func (g *notifyingGuard) Observe(ctx context.Context, at time.Time, players []domain.Player) (guard.Decisions, error) {
	decisions, err := g.fakeGuard.Observe(ctx, at, players)
	g.observations <- struct{}{}
	return decisions, err
}

func TestBlockedOptionalSamplingDoesNotDelayNextScheduledPlayerCycle(t *testing.T) {
	reader := newBlockingServerReader()
	reader.blockInfo = true
	reader.blockSettings = true
	guardService := &notifyingGuard{observations: make(chan struct{}, 3)}
	p := newAsyncSamplerPoller(t, reader, 5*time.Millisecond, guardService)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()

	awaitSignal(t, guardService.observations, "first guard observation")
	awaitSignal(t, reader.metricsStarted, "metrics start")
	awaitSignal(t, guardService.observations, "second guard observation")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}

func TestSamplerCancellationStopsBlockedWorker(t *testing.T) {
	reader := newBlockingServerReader()
	reader.blockInfo = true
	reader.blockSettings = true
	p := newAsyncSamplerPoller(t, reader, time.Minute, &fakeGuard{})
	ctx, cancel := context.WithCancel(t.Context())
	_, stop := p.startServerSampler(ctx)

	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, reader.metricsStarted, "metrics start")
	cancel()
	stop()
	awaitSignal(t, reader.metricsStopped, "metrics cancellation")
}

func TestSamplerQueueCoalescesExtraPlayerTicks(t *testing.T) {
	reader := newBlockingServerReader()
	p := newAsyncSamplerPoller(t, reader, time.Minute, &fakeGuard{})
	ctx, cancel := context.WithCancel(t.Context())
	_, stop := p.startServerSampler(ctx)
	defer stop()
	defer cancel()

	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, reader.metricsStarted, "first metrics start")
	for i := 0; i < 10; i++ {
		if err := p.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if queued := len(p.serverSampleSignals); queued != 1 {
		t.Fatalf("queued samples=%d, want 1", queued)
	}
	close(reader.metricsRelease)
	awaitSignal(t, reader.metricsStarted, "second metrics start")
	if calls := reader.metricsCalls.Load(); calls != 2 {
		t.Fatalf("metrics calls=%d, want 2", calls)
	}
}

func TestSamplerCoalescesToNewestTimestampAndEvaluatesMetadataCadenceFromIt(t *testing.T) {
	t1 := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(4 * time.Minute)
	t3 := t1.Add(6 * time.Minute)
	now := t1
	reader := &firstMetricsBlockingReader{firstStarted: make(chan struct{}), release: make(chan struct{})}
	recorder := &timestampRecorder{
		metrics: make(chan time.Time, 3), info: make(chan time.Time, 3), settings: make(chan time.Time, 3),
		secondMetricsRelease: make(chan struct{}),
	}
	p, err := New(&fakeClient{}, &fakeGuard{}, &fakeAnalytics{}, time.Minute, "warning", "kick", func() time.Time { return now },
		WithServerObservations(reader, recorder), WithServerObservationTimeout(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	completed, stop := p.startServerSampler(ctx)
	defer stop()
	defer cancel()

	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, reader.firstStarted, "first metrics start")
	if at := awaitTimestamp(t, recorder.info, "first info record"); !at.Equal(t1) {
		t.Fatalf("first info timestamp=%s", at)
	}
	if at := awaitTimestamp(t, recorder.settings, "first settings record"); !at.Equal(t1) {
		t.Fatalf("first settings timestamp=%s", at)
	}
	now = t2
	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	now = t3
	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	close(reader.release)

	if at := awaitTimestamp(t, recorder.metrics, "first metrics record"); !at.Equal(t1) {
		t.Fatalf("first metrics timestamp=%s", at)
	}
	awaitSignal(t, completed, "first sample completion")
	if at := awaitTimestamp(t, recorder.metrics, "coalesced metrics record"); !at.Equal(t3) {
		t.Fatalf("coalesced metrics timestamp=%s, want %s", at, t3)
	}
	if at := awaitTimestamp(t, recorder.info, "coalesced info record"); !at.Equal(t3) {
		t.Fatalf("coalesced info timestamp=%s, want %s", at, t3)
	}
	if at := awaitTimestamp(t, recorder.settings, "coalesced settings record"); !at.Equal(t3) {
		t.Fatalf("coalesced settings timestamp=%s, want %s", at, t3)
	}
	p.serverWorkerMu.Lock()
	pending := p.serverSamplePending
	queued := len(p.serverSampleSignals)
	p.serverWorkerMu.Unlock()
	if pending || queued != 0 {
		t.Fatalf("unexpected third sample: pending=%t queued=%d", pending, queued)
	}
	close(recorder.secondMetricsRelease)
	awaitSignal(t, completed, "coalesced sample completion")
	if calls := reader.calls.Load(); calls != 2 {
		t.Fatalf("metrics calls=%d, want 2", calls)
	}
}

type contextBlockingRecorder struct {
	metricsStarted chan struct{}
	metricsStopped chan struct{}
}

func (r *contextBlockingRecorder) RecordMetrics(ctx context.Context, _ time.Time, _ domain.ServerMetrics) error {
	close(r.metricsStarted)
	<-ctx.Done()
	close(r.metricsStopped)
	return ctx.Err()
}

func (*contextBlockingRecorder) RecordInfo(context.Context, time.Time, domain.ServerInfo) error {
	return nil
}

func (*contextBlockingRecorder) RecordSettings(context.Context, time.Time, domain.ServerSettings) error {
	return nil
}

func TestSamplerCancellationJoinsContextBlockingRecorder(t *testing.T) {
	recorder := &contextBlockingRecorder{metricsStarted: make(chan struct{}), metricsStopped: make(chan struct{})}
	p, err := New(&fakeClient{}, &fakeGuard{}, &fakeAnalytics{}, time.Minute, "warning", "kick", time.Now,
		WithServerObservations(&fakeServerReader{}, recorder), WithServerObservationTimeout(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	_, stop := p.startServerSampler(ctx)
	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, recorder.metricsStarted, "metrics recorder start")
	cancel()
	stop()
	awaitSignal(t, recorder.metricsStopped, "metrics recorder cancellation")
}
