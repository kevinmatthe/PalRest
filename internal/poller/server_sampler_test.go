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
