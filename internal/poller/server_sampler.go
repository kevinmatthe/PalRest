package poller

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

const (
	serverMetadataInterval          = 5 * time.Minute
	defaultServerObservationTimeout = 10 * time.Second
)

type ServerReader interface {
	Metrics(context.Context) (domain.ServerMetrics, error)
	Info(context.Context) (domain.ServerInfo, error)
	Settings(context.Context) (domain.ServerSettings, error)
}

type ServerObservationRecorder interface {
	RecordMetrics(context.Context, time.Time, domain.ServerMetrics) error
	RecordInfo(context.Context, time.Time, domain.ServerInfo) error
	RecordSettings(context.Context, time.Time, domain.ServerSettings) error
}

func WithServerObservations(reader ServerReader, recorder ServerObservationRecorder) Option {
	return func(p *Poller) {
		p.serverReader = reader
		p.serverRecorder = recorder
	}
}

func WithServerObservationTimeout(timeout time.Duration) Option {
	return func(p *Poller) {
		if timeout > 0 {
			p.serverSampleTimeout = timeout
		}
	}
}

// startServerSampler owns the only optional sampling worker. A one-slot input
// queue coalesces player ticks while its current bounded sampling cycle runs.
func (p *Poller) startServerSampler(ctx context.Context) (<-chan struct{}, func()) {
	if p.serverReader == nil || p.serverRecorder == nil {
		return p.serverSampleDone, func() {}
	}

	p.serverWorkerMu.Lock()
	if p.serverWorkerRunning {
		p.serverWorkerMu.Unlock()
		return p.serverSampleDone, func() {}
	}
	p.serverWorkerRunning = true
	p.serverWorkerMu.Unlock()

	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-workerCtx.Done():
				return
			case at := <-p.serverSampleSignals:
				p.sampleServerObservations(workerCtx, at)
				select {
				case p.serverSampleDone <- struct{}{}:
				default:
				}
			}
		}
	}()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			<-done
			p.serverWorkerMu.Lock()
			p.serverWorkerRunning = false
			p.serverWorkerMu.Unlock()
			for len(p.serverSampleSignals) > 0 {
				<-p.serverSampleSignals
			}
			for len(p.serverSampleDone) > 0 {
				<-p.serverSampleDone
			}
		})
	}
	return p.serverSampleDone, stop
}

func (p *Poller) enqueueServerSample(at time.Time) {
	if p.serverReader == nil || p.serverRecorder == nil {
		return
	}
	p.serverWorkerMu.Lock()
	defer p.serverWorkerMu.Unlock()
	if !p.serverWorkerRunning {
		return
	}
	select {
	case p.serverSampleSignals <- at:
	default:
	}
}

func (p *Poller) sampleServerObservations(ctx context.Context, at time.Time) {
	infoDue, settingsDue := p.serverMetadataDue(at)
	var attempts sync.WaitGroup
	attemptCount := 1
	if infoDue {
		attemptCount++
	}
	if settingsDue {
		attemptCount++
	}
	attempts.Add(attemptCount)
	go func() {
		defer attempts.Done()
		p.sampleMetrics(ctx, at)
	}()
	if infoDue {
		go func() {
			defer attempts.Done()
			p.sampleInfo(ctx, at)
		}()
	}
	if settingsDue {
		go func() {
			defer attempts.Done()
			p.sampleSettings(ctx, at)
		}()
	}
	attempts.Wait()
}

func (p *Poller) sampleMetrics(parent context.Context, at time.Time) {
	ctx, cancel := context.WithTimeout(parent, p.serverSampleTimeout)
	defer cancel()
	metrics, err := p.serverReader.Metrics(ctx)
	if err != nil {
		logOptionalObservationError("metrics", "read", err)
		return
	}
	if err := p.serverRecorder.RecordMetrics(ctx, at, metrics); err != nil {
		logOptionalObservationError("metrics", "record", err)
	}
}

func (p *Poller) sampleInfo(parent context.Context, at time.Time) {
	ctx, cancel := context.WithTimeout(parent, p.serverSampleTimeout)
	defer cancel()
	info, err := p.serverReader.Info(ctx)
	if err != nil {
		logOptionalObservationError("info", "read", err)
		return
	}
	if err := p.serverRecorder.RecordInfo(ctx, at, info); err != nil {
		logOptionalObservationError("info", "record", err)
	}
}

func (p *Poller) sampleSettings(parent context.Context, at time.Time) {
	ctx, cancel := context.WithTimeout(parent, p.serverSampleTimeout)
	defer cancel()
	settings, err := p.serverReader.Settings(ctx)
	if err != nil {
		logOptionalObservationError("settings", "read", err)
		return
	}
	if err := p.serverRecorder.RecordSettings(ctx, at, settings); err != nil {
		logOptionalObservationError("settings", "record", err)
	}
}

func (p *Poller) serverMetadataDue(at time.Time) (info, settings bool) {
	p.serverSampleMu.Lock()
	defer p.serverSampleMu.Unlock()
	info = p.lastInfoAttempt.IsZero() || at.Sub(p.lastInfoAttempt) >= serverMetadataInterval
	settings = p.lastSettingsAttempt.IsZero() || at.Sub(p.lastSettingsAttempt) >= serverMetadataInterval
	if info {
		p.lastInfoAttempt = at
	}
	if settings {
		p.lastSettingsAttempt = at
	}
	return info, settings
}

func logOptionalObservationError(stream, operation string, err error) {
	slog.Warn("optional server observation failed", "stream", stream, "operation", operation, "error", err)
}
