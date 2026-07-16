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
	if gameID != "palworld" {
		return Snapshot{}, fmt.Errorf("%w: %q", ErrGameNotSupported, gameID)
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return Snapshot{}, fmt.Errorf("%w: user ID is empty", ErrInvalidRequest)
	}

	guardSnapshot, err := p.snapshots.Snapshot(ctx, userID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("palworld guard snapshot: %w", err)
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
		return Snapshot{}, fmt.Errorf("palworld daily activity: %w", err)
	}

	var todayObserved, weekObserved time.Duration
	for _, row := range daily {
		weekObserved += row.Observed
		if row.Date == today {
			todayObserved += row.Observed
		}
	}

	snapshot := Snapshot{
		Schema:       SchemaV1,
		GameID:       "palworld",
		UserID:       userID,
		Capabilities: []string{"identity", "timers"},
		Identity:     playerIdentity(guardSnapshot.Player, userID),
		Timers: []Timer{
			durationTimer("today_observed", "Today observed", todayObserved, "normal", nil),
			durationTimer("week_observed", "Week observed", weekObserved, "normal", nil),
		},
	}

	status := p.status.Status()
	snapshot.ObservedAt = status.LastSuccess
	fresh := false
	if !status.LastSuccess.IsZero() {
		snapshot.FreshUntil = status.LastSuccess.Add(p.maxGap)
		fresh = !now.After(snapshot.FreshUntil)
	}
	if fresh {
		if guardSnapshot.Online {
			snapshot.SourceStatus = "online"
		} else {
			snapshot.SourceStatus = "offline"
		}
	} else {
		snapshot.SourceStatus = "unknown"
	}

	if guardSnapshot.Policy.Enabled && !guardSnapshot.Policy.Exempt {
		snapshot.Timers = append(snapshot.Timers, policyTimers(guardSnapshot, now)...)
	}

	if fresh && guardSnapshot.Online {
		player := guardSnapshot.Player
		if finite(player.Ping) && player.Ping >= 0 {
			snapshot.Latency = &Latency{Milliseconds: player.Ping}
		}
		if finite(player.LocationX) && finite(player.LocationY) {
			snapshot.Map = &MapPosition{
				X:          player.LocationX,
				Y:          player.LocationY,
				Projection: "palworld_world_v1",
				TileSet:    "palworld_default_v1",
				TileURL:    "/map/tiles/{z}/{x}/{y}.png",
			}
		}
	}
	snapshot.Capabilities = snapshotCapabilities(snapshot)
	return snapshot, nil
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
