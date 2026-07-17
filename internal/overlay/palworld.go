package overlay

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

var (
	ErrGameNotSupported = errors.New("game not supported")
	ErrInvalidRequest   = errors.New("invalid overlay request")
)

type Provider interface {
	Snapshot(ctx context.Context, gameID, userID string) (Snapshot, error)
	Presentation(ctx context.Context, gameID, userID string) (Presentation, error)
}

type SnapshotSource interface {
	Snapshot(context.Context, string) (domain.PlayerSnapshot, error)
}

type DailySource interface {
	PlayerDailyActivity(context.Context, string, string, string) ([]store.DailyActivity, error)
}

type StatusSource interface {
	Status() domain.PollStatus
}

type PalworldProvider struct {
	snapshots  SnapshotSource
	daily      DailySource
	status     StatusSource
	locationMu sync.RWMutex
	location   *time.Location
	maxGap     time.Duration
	now        func() time.Time
}

func (p *PalworldProvider) SetLocation(location *time.Location) error {
	if location == nil {
		return errors.New("overlay: location is nil")
	}
	p.locationMu.Lock()
	p.location = location
	p.locationMu.Unlock()
	return nil
}

func NewPalworldProvider(s SnapshotSource, d DailySource, status StatusSource, location *time.Location, maxGap time.Duration) *PalworldProvider {
	if s == nil {
		panic("overlay: nil snapshot source")
	}
	if d == nil {
		panic("overlay: nil daily source")
	}
	if status == nil {
		panic("overlay: nil status source")
	}
	if maxGap <= 0 {
		panic("overlay: max gap must be positive")
	}
	if location == nil {
		panic("overlay: nil location")
	}
	return &PalworldProvider{
		snapshots: s,
		daily:     d,
		status:    status,
		location:  location,
		maxGap:    maxGap,
		now:       time.Now,
	}
}

func (p *PalworldProvider) Snapshot(ctx context.Context, gameID, userID string) (Snapshot, error) {
	view, err := p.palworldView(ctx, gameID, userID)
	if err != nil {
		return Snapshot{}, err
	}
	return view.snapshot(), nil
}

func (p *PalworldProvider) Presentation(ctx context.Context, gameID, userID string) (Presentation, error) {
	view, err := p.palworldView(ctx, gameID, userID)
	if err != nil {
		return Presentation{}, err
	}
	return view.presentation(), nil
}

type palworldView struct {
	userID        string
	now           time.Time
	guard         domain.PlayerSnapshot
	identity      Identity
	observedAt    time.Time
	freshUntil    time.Time
	sourceStatus  string
	todayObserved time.Duration
	weekObserved  time.Duration
	latency       *Latency
	mapPosition   *MapPosition
}

func (p *PalworldProvider) palworldView(ctx context.Context, gameID, userID string) (palworldView, error) {
	if gameID != "palworld" {
		return palworldView{}, fmt.Errorf("%w: %q", ErrGameNotSupported, gameID)
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return palworldView{}, fmt.Errorf("%w: user ID is empty", ErrInvalidRequest)
	}

	guardSnapshot, err := p.snapshots.Snapshot(ctx, userID)
	if err != nil {
		return palworldView{}, fmt.Errorf("palworld guard snapshot: %w", err)
	}

	now := p.now()
	p.locationMu.RLock()
	location := p.location
	p.locationMu.RUnlock()
	localNow := now.In(location)
	today := dateString(localNow)
	monday := localNow.AddDate(0, 0, -mondayOffset(localNow.Weekday()))
	tomorrow := localNow.AddDate(0, 0, 1)
	daily, err := p.daily.PlayerDailyActivity(ctx, userID, dateString(monday), dateString(tomorrow))
	if err != nil {
		return palworldView{}, fmt.Errorf("palworld daily activity: %w", err)
	}

	var todayObserved, weekObserved time.Duration
	for _, row := range daily {
		weekObserved += row.Observed
		if row.Date == today {
			todayObserved += row.Observed
		}
	}

	view := palworldView{
		userID:        userID,
		now:           now,
		guard:         guardSnapshot,
		identity:      playerIdentity(guardSnapshot.Player, userID),
		todayObserved: todayObserved,
		weekObserved:  weekObserved,
	}

	status := p.status.Status()
	view.observedAt = status.LastSuccess
	fresh := false
	if !status.LastSuccess.IsZero() {
		view.freshUntil = status.LastSuccess.Add(p.maxGap)
		fresh = !now.After(view.freshUntil)
	}
	if fresh {
		if guardSnapshot.Online {
			view.sourceStatus = "online"
		} else {
			view.sourceStatus = "offline"
		}
	} else {
		view.sourceStatus = "unknown"
	}

	if fresh && guardSnapshot.Online {
		player := guardSnapshot.Player
		if finite(player.Ping) && player.Ping >= 0 {
			view.latency = &Latency{Milliseconds: player.Ping}
		}
		if finite(player.LocationX) && finite(player.LocationY) {
			view.mapPosition = &MapPosition{
				X:          player.LocationX,
				Y:          player.LocationY,
				Projection: "palworld_world_v1",
				TileSet:    "palworld_default_v1",
				TileURL:    "/map/tiles/{z}/{x}/{y}.png",
			}
		}
	}
	return view, nil
}

func (view palworldView) snapshot() Snapshot {
	snapshot := Snapshot{
		Schema:       SchemaV1,
		GameID:       "palworld",
		UserID:       view.userID,
		ObservedAt:   view.observedAt,
		FreshUntil:   view.freshUntil,
		SourceStatus: view.sourceStatus,
		Identity:     view.identity,
		Latency:      view.latency,
		Timers: []Timer{
			durationTimer("today_observed", "Today observed", view.todayObserved, "normal", nil),
			durationTimer("week_observed", "Week observed", view.weekObserved, "normal", nil),
		},
		Map: view.mapPosition,
	}
	if view.guard.Policy.Enabled && !view.guard.Policy.Exempt {
		snapshot.Timers = append(snapshot.Timers, policyTimers(view.guard, view.now)...)
	}
	snapshot.Capabilities = snapshotCapabilities(snapshot)
	return snapshot
}

func (view palworldView) presentation() Presentation {
	player := view.guard.Player
	accountName := strings.TrimSpace(player.AccountName)
	fields := make([]DisplayField, 0, 14)
	if accountName == "" {
		fields = append(fields, UnavailableDisplayField("identity.account", "Account", "text", "muted"))
	} else {
		fields = append(fields, StringDisplayField("identity.account", "Account", "text", accountName, "normal", nil))
	}
	fields = append(fields, StringDisplayField("identity.uid", "UID", "text", view.userID, "normal", nil))
	if player.Level > 0 {
		fields = append(fields, NumberDisplayField("identity.level", "Level", "integer", float64(player.Level), "normal", nil))
	} else {
		fields = append(fields, UnavailableDisplayField("identity.level", "Level", "integer", "muted"))
	}
	presenceTone := "normal"
	if view.sourceStatus == "unknown" {
		presenceTone = "muted"
	}
	fields = append(fields, StringDisplayField("presence.status", "Status", "status", view.sourceStatus, presenceTone, nil))
	if player.LastOnline.IsZero() {
		fields = append(fields, UnavailableDisplayField("presence.last_online", "Last online", "timestamp", "muted"))
	} else {
		fields = append(fields, TimestampDisplayField("presence.last_online", "Last online", player.LastOnline, "muted"))
	}
	if view.latency == nil {
		fields = append(fields, UnavailableDisplayField("network.latency", "Latency", "latency_ms", "muted"))
	} else {
		fields = append(fields, NumberDisplayField("network.latency", "Latency", "latency_ms", view.latency.Milliseconds, "normal", nil))
	}
	if view.mapPosition == nil {
		fields = append(fields, UnavailableDisplayField("location.coordinates", "Coordinates", "coordinates", "muted"))
	} else {
		fields = append(fields, CoordinatesDisplayField("location.coordinates", "Coordinates", view.mapPosition.X, view.mapPosition.Y, "normal"))
	}
	fields = append(fields,
		NumberDisplayField("activity.today", "Today", "duration_ms", float64(view.todayObserved.Milliseconds()), "normal", nil),
		NumberDisplayField("activity.week", "This week", "duration_ms", float64(view.weekObserved.Milliseconds()), "normal", nil),
	)
	fields = append(fields, view.policyDisplayFields()...)

	return Presentation{
		Schema:       PresentationSchemaV1,
		GameID:       "palworld",
		UserID:       view.userID,
		ObservedAt:   view.observedAt,
		FreshUntil:   view.freshUntil,
		SourceStatus: view.sourceStatus,
		Identity:     view.identity,
		Map:          view.mapPosition,
		Fields:       fields,
	}
}

func (view palworldView) policyDisplayFields() []DisplayField {
	policy := view.guard.Policy
	if !policy.Enabled || policy.Exempt {
		return []DisplayField{
			UnavailableDisplayField("policy.strategy", "Strategy", "text", "muted"),
			UnavailableDisplayField("policy.cycle_used", "Cycle used", "duration_ms", "muted"),
			UnavailableDisplayField("policy.remaining", "Remaining", "duration_ms", "muted"),
			UnavailableDisplayField("policy.period_end", "Period end", "timestamp", "muted"),
			UnavailableDisplayField("policy.enforcement", "Enforcement", "status", "muted"),
		}
	}

	remainingLabel, remainingValue := policyRemaining(policy.Strategy, view.guard.Remaining, view.guard.Period.End, view.now)
	tone := policyTone(view.guard.Remaining, policy.WarningBefore)
	usedProgress, remainingProgress := policyProgress(view.guard.Used, view.guard.Remaining)
	fields := []DisplayField{
		StringDisplayField("policy.strategy", "Strategy", "text", policyStrategyLabel(policy.Strategy), "normal", nil),
		NumberDisplayField("policy.cycle_used", "Cycle used", "duration_ms", float64(view.guard.Used.Milliseconds()), tone, usedProgress),
		NumberDisplayField("policy.remaining", remainingLabel, "duration_ms", float64(remainingValue.Milliseconds()), tone, remainingProgress),
	}
	if view.guard.Period.End.IsZero() {
		fields = append(fields, UnavailableDisplayField("policy.period_end", "Period end", "timestamp", "muted"))
	} else {
		fields = append(fields, TimestampDisplayField("policy.period_end", "Period end", view.guard.Period.End, "normal"))
	}
	enforcement, enforcementTone := policyEnforcement(view.guard.Enforcement.Status)
	fields = append(fields, StringDisplayField("policy.enforcement", "Enforcement", "status", enforcement, enforcementTone, nil))
	return fields
}

func policyStrategyLabel(strategy string) string {
	switch strategy {
	case "fixed_window":
		return "Fixed window"
	case "credit":
		return "Credit"
	case "cooldown":
		return "Cooldown"
	default:
		return "Unknown"
	}
}

func policyEnforcement(status string) (string, string) {
	switch status {
	case "":
		return "Inactive", "normal"
	case "pending":
		return "Pending", "warning"
	case "success":
		return "Enforced", "danger"
	case "failure":
		return "Failed", "danger"
	default:
		return "Unknown", "muted"
	}
}

func policyRemaining(strategy string, remaining time.Duration, periodEnd, now time.Time) (string, time.Duration) {
	label := "Remaining"
	value := remaining
	switch strategy {
	case "credit":
		label = "Available credit"
	case "cooldown":
		if remaining <= 0 && periodEnd.After(now) {
			label = "Rest remaining"
			value = periodEnd.Sub(now)
		}
	}
	return label, value
}

func playerIdentity(player domain.Player, fallback string) Identity {
	displayName := strings.TrimSpace(player.Name)
	accountName := strings.TrimSpace(player.AccountName)
	if displayName == "" {
		displayName = accountName
	}
	if displayName == "" {
		displayName = fallback
	}
	identity := Identity{DisplayName: displayName, AccountName: accountName}
	if player.Level > 0 {
		level := player.Level
		identity.Level = &level
	}
	return identity
}

func policyTimers(snapshot domain.PlayerSnapshot, now time.Time) []Timer {
	remainingValue := snapshot.Remaining
	remainingLabel := "频控剩余"
	switch snapshot.Policy.Strategy {
	case "credit":
		remainingLabel = "可用额度"
	case "cooldown":
		if snapshot.Remaining <= 0 && snapshot.Period.End.After(now) {
			remainingLabel = "休息剩余"
			remainingValue = snapshot.Period.End.Sub(now)
		}
	}

	tone := policyTone(snapshot.Remaining, snapshot.Policy.WarningBefore)
	usedProgress, remainingProgress := policyProgress(snapshot.Used, snapshot.Remaining)
	return []Timer{
		durationTimer("policy_cycle_used", "Policy cycle used", snapshot.Used, tone, usedProgress),
		durationTimer("policy_remaining", remainingLabel, remainingValue, tone, remainingProgress),
	}
}

func policyTone(remaining time.Duration, warnings []time.Duration) string {
	if remaining <= 0 {
		return "danger"
	}
	var largest time.Duration
	for _, warning := range warnings {
		if warning > largest {
			largest = warning
		}
	}
	if largest > 0 && remaining <= largest {
		return "warning"
	}
	return "normal"
}

func policyProgress(used, remaining time.Duration) (*float64, *float64) {
	denominator := used + remaining
	if denominator == 0 {
		return nil, nil
	}
	usedValue := clamp(float64(used) / float64(denominator))
	remainingValue := clamp(float64(remaining) / float64(denominator))
	return &usedValue, &remainingValue
}

func durationTimer(id, label string, value time.Duration, tone string, progress *float64) Timer {
	return Timer{ID: id, Label: label, ValueMS: value.Milliseconds(), Semantic: "duration", Tone: tone, Progress: progress}
}

func snapshotCapabilities(snapshot Snapshot) []string {
	capabilities := []string{"identity"}
	if snapshot.Latency != nil {
		capabilities = append(capabilities, "latency")
	}
	if len(snapshot.Timers) > 0 {
		capabilities = append(capabilities, "timers")
	}
	if snapshot.Map != nil {
		capabilities = append(capabilities, "map")
	}
	return capabilities
}

func dateString(t time.Time) string { return t.Format("2006-01-02") }

func mondayOffset(weekday time.Weekday) int { return (int(weekday) + 6) % 7 }

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func clamp(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
