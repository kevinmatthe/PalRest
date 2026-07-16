package poller

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/guard"
)

type Client interface {
	ListPlayers(context.Context) ([]domain.Player, error)
	Announce(context.Context, string) error
	Kick(context.Context, string, string) error
}

type Guard interface {
	Observe(context.Context, time.Time, []domain.Player) (guard.Decisions, error)
	PollFailed()
	RecordWarningResult(context.Context, guard.WarningDecision, error, time.Time) error
	RecordKickResult(context.Context, guard.KickDecision, error, time.Time) error
}

type Analytics interface {
	Observe(context.Context, time.Time, []domain.Player) error
	SetLocation(*time.Location) error
}

type PlayerObserver interface {
	Observe(context.Context, time.Time, []domain.Player, string) error
	PollFailed()
}

type Option func(*Poller)

func WithPlayerObserver(observer PlayerObserver) Option {
	return func(p *Poller) { p.playerObserver = observer }
}

func WithCorrelationIDGenerator(generator func() string) Option {
	return func(p *Poller) {
		if generator != nil {
			p.correlationID = func() (string, error) { return generator(), nil }
		}
	}
}

type Poller struct {
	client                 Client
	guard                  Guard
	analytics              Analytics
	playerObserver         PlayerObserver
	serverReader           ServerReader
	serverRecorder         ServerObservationRecorder
	interval               time.Duration
	announceTemplate       *template.Template
	kickTemplate           *template.Template
	loginTemplate          *template.Template
	now                    func() time.Time
	correlationID          func() (string, error)
	cycleMu                sync.Mutex
	mu                     sync.RWMutex
	status                 domain.PollStatus
	serverSampleSignals    chan struct{}
	serverSampleDone       chan struct{}
	serverSampleTimeout    time.Duration
	serverMetadataInterval time.Duration
	serverSampleMu         sync.Mutex
	lastInfoAttempt        time.Time
	lastSettingsAttempt    time.Time
	serverWorkerMu         sync.Mutex
	serverWorkerRunning    bool
	serverSamplePending    bool
	latestServerSample     time.Time
}

func New(client Client, guardService Guard, analytics Analytics, interval time.Duration, announceText, kickText, loginText string, now func() time.Time, options ...Option) (*Poller, error) {
	announce, err := template.New("announce").Option("missingkey=error").Parse(announceText)
	if err != nil {
		return nil, fmt.Errorf("parse announce template: %w", err)
	}
	kick, err := template.New("kick").Option("missingkey=error").Parse(kickText)
	if err != nil {
		return nil, fmt.Errorf("parse kick template: %w", err)
	}
	login, err := template.New("login").Option("missingkey=error").Parse(loginText)
	if err != nil {
		return nil, fmt.Errorf("parse login template: %w", err)
	}
	p := &Poller{
		client: client, guard: guardService, analytics: analytics, interval: interval,
		announceTemplate: announce, kickTemplate: kick, loginTemplate: login, now: now,
		status:                 domain.PollStatus{StartedAt: now().UTC(), ConfigVersion: 1},
		serverSampleSignals:    make(chan struct{}, 1),
		serverSampleDone:       make(chan struct{}, 1),
		serverSampleTimeout:    DefaultServerObservationTimeout,
		serverMetadataInterval: DefaultServerMetadataInterval,
	}
	p.correlationID = newCorrelationID
	for _, option := range options {
		if option != nil {
			option(p)
		}
	}
	if (p.serverReader == nil) != (p.serverRecorder == nil) {
		return nil, fmt.Errorf("configure server observations: reader and recorder must both be provided")
	}
	return p, nil
}

// Run owns the optional server sampler lifecycle and must only be called once
// at a time for a Poller. It starts the sampler before the immediate player
// cycle and cancels and joins it before returning.
func (p *Poller) Run(ctx context.Context) {
	slog.Info("poller started", "interval_ms", p.interval.Milliseconds())
	select {
	case <-ctx.Done():
		return
	default:
	}
	_, stopSampler := p.startServerSampler(ctx)
	defer stopSampler()
	p.runScheduledCycle()
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("poller stopped")
			return
		case <-ticker.C:
			p.runScheduledCycle()
		}
	}
}

func (p *Poller) runScheduledCycle() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.RunOnce(ctx); err != nil {
		slog.Warn("scheduled poll cycle failed", "error", err)
	}
}

// RunOnce executes the critical player path. A standalone call does not start
// optional server sampling; it only dispatches a sample when Run (or the
// package-private test lifecycle) already owns an active sampler worker.
func (p *Poller) RunOnce(ctx context.Context) error {
	// cycleMu is exclusive so direct callers cannot overlap player observation,
	// Guard state transitions, or enforcement side effects.
	p.cycleMu.Lock()
	now := p.now().UTC()
	defer func() {
		p.cycleMu.Unlock()
		p.enqueueServerSample(now)
	}()
	p.mu.RLock()
	announceTemplate := p.announceTemplate
	kickTemplate := p.kickTemplate
	loginTemplate := p.loginTemplate
	p.mu.RUnlock()
	p.updateStatus(func(status *domain.PollStatus) { status.LastAttempt = now })
	slog.Info("poll cycle started", "at", now.Format(time.RFC3339))
	players, err := p.client.ListPlayers(ctx)
	if err != nil {
		p.guard.PollFailed()
		p.playerPollFailed()
		p.setError(err)
		slog.Error("poll list players failed", "error", err)
		return err
	}
	slog.Info("poll listed players", "online_count", len(players))
	correlationID := ""
	if p.playerObserver != nil {
		correlationID, err = p.correlationID()
		correlationID = strings.TrimSpace(correlationID)
		if err != nil || correlationID == "" {
			if err == nil {
				err = fmt.Errorf("empty ID")
			}
			err = fmt.Errorf("generate player poll correlation ID: %w", err)
			p.guard.PollFailed()
			p.playerPollFailed()
			p.setError(err)
			slog.Error("poll player observation failed", "online_count", len(players), "error", err)
			return err
		}
	}
	if err := p.analytics.Observe(ctx, now, players); err != nil {
		p.guard.PollFailed()
		p.playerPollFailed()
		p.setError(err)
		slog.Error("poll analytics observation failed", "online_count", len(players), "error", err)
		return err
	}
	// The correlation ID is validated before either persistence stream. After
	// that gate, Analytics persists independently, while the business player
	// observation gates Guard continuity and enforcement.
	if p.playerObserver != nil {
		if err := p.playerObserver.Observe(ctx, now, players, correlationID); err != nil {
			p.guard.PollFailed()
			p.playerPollFailed()
			p.setError(err)
			slog.Error("poll player observation failed", "online_count", len(players), "error", err)
			return err
		}
	}
	decisions, err := p.guard.Observe(ctx, now, players)
	if err != nil {
		p.setError(err)
		slog.Error("poll observation failed", "online_count", len(players), "error", err)
		return err
	}
	p.updateStatus(func(status *domain.PollStatus) {
		status.LastSuccess = now
		status.LastError = ""
		status.OnlineCount = len(players)
	})
	logObservations(decisions.Observations)
	slog.Info("poll cycle completed",
		"online_count", len(players),
		"observed_count", len(decisions.Observations),
		"login_notices", len(decisions.Logins),
		"warning_decisions", len(decisions.Warnings),
		"kick_decisions", len(decisions.Kicks),
	)

	var effectErrors []error
	// Login remaining-quota notices first (full-server announce; no private whisper API).
	for _, decision := range decisions.Logins {
		message, renderErr := render(loginTemplate, struct {
			PlayerName string
			Remaining  string
			Limit      string
			ResetAt    string
		}{
			decision.PlayerName,
			formatDurationZH(decision.Remaining),
			formatDurationZH(decision.Limit),
			formatTimeZH(decision.ResetAt),
		})
		resultErr := renderErr
		if resultErr == nil {
			resultErr = p.client.Announce(ctx, message)
		}
		logEffectResult("login_notice", decision.UserID, decision.PlayerName, resultErr,
			"remaining_ms", decision.Remaining.Milliseconds(),
			"limit_ms", decision.Limit.Milliseconds(),
			"period_key", decision.Period.Key,
		)
		if resultErr != nil {
			// Best-effort: do not block kick/warning handling.
			slog.Warn("login notice announce failed", "user_id", decision.UserID, "error", resultErr)
			effectErrors = append(effectErrors, resultErr)
		}
	}
	for _, decision := range decisions.Warnings {
		resetAt := decision.ResetAt
		if resetAt.IsZero() {
			resetAt = decision.Period.End
		}
		message, renderErr := render(announceTemplate, struct {
			PlayerName string
			Remaining  string
			Limit      string
			ResetAt    string
		}{
			decision.PlayerName,
			formatDurationZH(decision.Remaining),
			formatDurationZH(decision.Limit),
			formatTimeZH(resetAt),
		})
		resultErr := renderErr
		if resultErr == nil {
			resultErr = p.client.Announce(ctx, message)
		}
		logEffectResult("warning", decision.UserID, decision.PlayerName, resultErr,
			"threshold_ms", decision.Threshold.Milliseconds(),
			"remaining_ms", decision.Remaining.Milliseconds(),
			"period_key", decision.Period.Key,
		)
		if recordErr := p.guard.RecordWarningResult(ctx, decision, resultErr, now); recordErr != nil {
			effectErrors = append(effectErrors, recordErr)
			slog.Error("record warning result failed", "user_id", decision.UserID, "error", recordErr)
		}
		if resultErr != nil {
			effectErrors = append(effectErrors, resultErr)
		}
	}
	for _, decision := range decisions.Kicks {
		message, renderErr := render(kickTemplate, struct {
			PlayerName string
			Remaining  string
			Limit      string
			ResetAt    string
		}{decision.PlayerName, formatDurationZH(0), "", formatTimeZH(decision.ResetAt)})
		resultErr := renderErr
		if resultErr == nil {
			resultErr = p.client.Kick(ctx, decision.UserID, message)
		}
		logEffectResult("kick", decision.UserID, decision.PlayerName, resultErr,
			"generation", decision.Generation,
			"policy_revision", decision.PolicyRevision,
			"period_key", decision.Period.Key,
		)
		if recordErr := p.guard.RecordKickResult(ctx, decision, resultErr, now); recordErr != nil {
			effectErrors = append(effectErrors, recordErr)
			slog.Error("record kick result failed", "user_id", decision.UserID, "error", recordErr)
		}
		if resultErr != nil {
			effectErrors = append(effectErrors, resultErr)
		}
	}
	if err := errors.Join(effectErrors...); err != nil {
		p.setError(err)
		slog.Warn("poll side effects completed with errors", "error", err)
		return err
	}
	return nil
}

func (p *Poller) playerPollFailed() {
	if p.playerObserver != nil {
		p.playerObserver.PollFailed()
	}
}

func newCorrelationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("read crypto random bytes: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func (p *Poller) UpdateTemplates(announceText, kickText, loginText string) error {
	return p.ApplyConfig(func() error { return nil }, announceText, kickText, loginText)
}

// ApplyPolicyTimezone serializes policy persistence and its analytics calendar
// transition against poll cycles. Lock ordering is cycleMu before any policy or
// analytics locks; callbacks must not call ApplyConfig or this method again.
func (p *Poller) ApplyPolicyTimezone(update func() error, location *time.Location) error {
	if location == nil {
		return fmt.Errorf("apply policy timezone: location is nil")
	}
	p.cycleMu.Lock()
	defer p.cycleMu.Unlock()
	if err := update(); err != nil {
		return err
	}
	return p.analytics.SetLocation(location)
}

func (p *Poller) ApplyConfig(update func() error, announceText, kickText, loginText string) error {
	announce, err := template.New("announce").Option("missingkey=error").Parse(announceText)
	if err != nil {
		return fmt.Errorf("parse announce template: %w", err)
	}
	kick, err := template.New("kick").Option("missingkey=error").Parse(kickText)
	if err != nil {
		return fmt.Errorf("parse kick template: %w", err)
	}
	login, err := template.New("login").Option("missingkey=error").Parse(loginText)
	if err != nil {
		return fmt.Errorf("parse login template: %w", err)
	}
	p.cycleMu.Lock()
	defer p.cycleMu.Unlock()
	if err := update(); err != nil {
		return err
	}
	p.mu.Lock()
	p.announceTemplate = announce
	p.kickTemplate = kick
	p.loginTemplate = login
	p.mu.Unlock()
	slog.Info("poller templates updated")
	return nil
}

func (p *Poller) Status() domain.PollStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}

func (p *Poller) SetConfigReloadError(message string) {
	p.updateStatus(func(status *domain.PollStatus) { status.ConfigReloadErr = bounded(message) })
	if message != "" {
		slog.Warn("config reload error", "error", bounded(message))
	}
}

func (p *Poller) updateStatus(fn func(*domain.PollStatus)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(&p.status)
}

func (p *Poller) setError(err error) {
	p.updateStatus(func(status *domain.PollStatus) { status.LastError = bounded(err.Error()) })
}

func render(tpl *template.Template, data any) (string, error) {
	var output bytes.Buffer
	if err := tpl.Execute(&output, data); err != nil {
		return "", fmt.Errorf("render message: %w", err)
	}
	return output.String(), nil
}

func bounded(message string) string {
	if len(message) > 500 {
		return message[:500]
	}
	return message
}

func logObservations(observations []guard.ObservationResult) {
	for _, observation := range observations {
		attrs := []any{
			"user_id", observation.UserID,
			"player_name", observation.PlayerName,
			"policy_enabled", observation.PolicyEnabled,
			"exempt", observation.Exempt,
			"continuous", observation.Continuous,
			"accounting", observation.Accounting,
			"skip_reason", observation.SkipReason,
			"gap_ms", observation.Gap.Milliseconds(),
			"max_gap_ms", observation.MaxGap.Milliseconds(),
			"added_ms", observation.Added.Milliseconds(),
			"used_ms", observation.Used.Milliseconds(),
			"remaining_ms", observation.Remaining.Milliseconds(),
			"limit_ms", observation.Limit.Milliseconds(),
			"period_key", observation.Period.Key,
			"period_start", observation.Period.Start.Format(time.RFC3339),
			"period_end", observation.Period.End.Format(time.RFC3339),
			"generation", observation.Generation,
		}
		if observation.Added > 0 {
			slog.Info("player usage updated", attrs...)
			continue
		}
		slog.Info("player usage unchanged", attrs...)
	}
}

func logEffectResult(action, userID, playerName string, resultErr error, attrs ...any) {
	all := []any{"action", action, "user_id", userID, "player_name", playerName}
	all = append(all, attrs...)
	if resultErr != nil {
		all = append(all, "error", resultErr)
		slog.Warn("poll side effect failed", all...)
		return
	}
	slog.Info("poll side effect succeeded", all...)
}
