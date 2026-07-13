package poller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
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
}

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

type Option func(*Poller)

func WithPlayerObserver(observer PlayerObserver) Option {
	return func(p *Poller) { p.playerObserver = observer }
}

func WithServerObservations(reader ServerReader, recorder ServerObservationRecorder) Option {
	return func(p *Poller) {
		p.serverReader = reader
		p.serverRecorder = recorder
	}
}

func WithCorrelationIDGenerator(generator func() string) Option {
	return func(p *Poller) {
		if generator != nil {
			p.correlationID = generator
		}
	}
}

var pollIDSequence atomic.Uint64

type Poller struct {
	client              Client
	guard               Guard
	analytics           Analytics
	playerObserver      PlayerObserver
	serverReader        ServerReader
	serverRecorder      ServerObservationRecorder
	interval            time.Duration
	announceTemplate    *template.Template
	kickTemplate        *template.Template
	now                 func() time.Time
	correlationID       func() string
	cycleMu             sync.RWMutex
	mu                  sync.RWMutex
	status              domain.PollStatus
	lastInfoAttempt     time.Time
	lastSettingsAttempt time.Time
}

func New(client Client, guardService Guard, analytics Analytics, interval time.Duration, announceText, kickText string, now func() time.Time, options ...Option) (*Poller, error) {
	announce, err := template.New("announce").Option("missingkey=error").Parse(announceText)
	if err != nil {
		return nil, fmt.Errorf("parse announce template: %w", err)
	}
	kick, err := template.New("kick").Option("missingkey=error").Parse(kickText)
	if err != nil {
		return nil, fmt.Errorf("parse kick template: %w", err)
	}
	p := &Poller{
		client: client, guard: guardService, analytics: analytics, interval: interval,
		announceTemplate: announce, kickTemplate: kick, now: now,
		status: domain.PollStatus{StartedAt: now().UTC(), ConfigVersion: 1},
	}
	p.correlationID = func() string {
		return fmt.Sprintf("poll-%d-%d", now().UTC().UnixNano(), pollIDSequence.Add(1))
	}
	for _, option := range options {
		if option != nil {
			option(p)
		}
	}
	return p, nil
}

func (p *Poller) Run(ctx context.Context) {
	slog.Info("poller started", "interval_ms", p.interval.Milliseconds())
	select {
	case <-ctx.Done():
		return
	default:
	}
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

func (p *Poller) RunOnce(ctx context.Context) error {
	p.cycleMu.RLock()
	defer p.cycleMu.RUnlock()
	now := p.now().UTC()
	defer p.sampleServerObservations(ctx, now)
	p.mu.RLock()
	announceTemplate := p.announceTemplate
	kickTemplate := p.kickTemplate
	p.mu.RUnlock()
	p.updateStatus(func(status *domain.PollStatus) { status.LastAttempt = now })
	slog.Info("poll cycle started", "at", now.Format(time.RFC3339))
	players, err := p.client.ListPlayers(ctx)
	if err != nil {
		p.guard.PollFailed()
		p.setError(err)
		slog.Error("poll list players failed", "error", err)
		return err
	}
	slog.Info("poll listed players", "online_count", len(players))
	if err := p.analytics.Observe(ctx, now, players); err != nil {
		p.guard.PollFailed()
		p.setError(err)
		slog.Error("poll analytics observation failed", "online_count", len(players), "error", err)
		return err
	}
	// Analytics remains an independent persistence stream, while the business
	// player observation is a gate for Guard continuity and enforcement.
	if p.playerObserver != nil {
		correlationID := strings.TrimSpace(p.correlationID())
		if correlationID == "" {
			err := fmt.Errorf("generate player poll correlation ID: empty ID")
			p.guard.PollFailed()
			p.setError(err)
			slog.Error("poll player observation failed", "online_count", len(players), "error", err)
			return err
		}
		if err := p.playerObserver.Observe(ctx, now, players, correlationID); err != nil {
			p.guard.PollFailed()
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
		"warning_decisions", len(decisions.Warnings),
		"kick_decisions", len(decisions.Kicks),
	)

	var effectErrors []error
	for _, decision := range decisions.Warnings {
		message, renderErr := render(announceTemplate, struct {
			PlayerName string
			Remaining  string
			ResetAt    string
		}{decision.PlayerName, decision.Remaining.Round(time.Second).String(), decision.Period.End.Format(time.RFC3339)})
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
			ResetAt    string
		}{decision.PlayerName, "0s", decision.ResetAt.Format(time.RFC3339)})
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

const serverMetadataInterval = 5 * time.Minute

func (p *Poller) sampleServerObservations(ctx context.Context, at time.Time) {
	if p.serverReader == nil || p.serverRecorder == nil {
		return
	}
	metrics, err := p.serverReader.Metrics(ctx)
	if err != nil {
		logOptionalObservationError("metrics", "read", err)
	} else if err := p.serverRecorder.RecordMetrics(ctx, at, metrics); err != nil {
		logOptionalObservationError("metrics", "record", err)
	}

	infoDue, settingsDue := p.serverMetadataDue(at)
	if infoDue {
		info, err := p.serverReader.Info(ctx)
		if err != nil {
			logOptionalObservationError("info", "read", err)
		} else if err := p.serverRecorder.RecordInfo(ctx, at, info); err != nil {
			logOptionalObservationError("info", "record", err)
		}
	}
	if settingsDue {
		settings, err := p.serverReader.Settings(ctx)
		if err != nil {
			logOptionalObservationError("settings", "read", err)
		} else if err := p.serverRecorder.RecordSettings(ctx, at, settings); err != nil {
			logOptionalObservationError("settings", "record", err)
		}
	}
}

func (p *Poller) serverMetadataDue(at time.Time) (info, settings bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
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

func (p *Poller) UpdateTemplates(announceText, kickText string) error {
	return p.ApplyConfig(func() error { return nil }, announceText, kickText)
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

func (p *Poller) ApplyConfig(update func() error, announceText, kickText string) error {
	announce, err := template.New("announce").Option("missingkey=error").Parse(announceText)
	if err != nil {
		return fmt.Errorf("parse announce template: %w", err)
	}
	kick, err := template.New("kick").Option("missingkey=error").Parse(kickText)
	if err != nil {
		return fmt.Errorf("parse kick template: %w", err)
	}
	p.cycleMu.Lock()
	defer p.cycleMu.Unlock()
	if err := update(); err != nil {
		return err
	}
	p.mu.Lock()
	p.announceTemplate = announce
	p.kickTemplate = kick
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
