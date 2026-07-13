package observation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

const cleanupBatchSize = 500

// Recorder atomically persists one derived observation. RecordPlayerObservation
// is called while Service holds its mutex and therefore must not re-enter
// Service.Observe; the lock intentionally serializes persistence with baseline
// advancement.
type Recorder interface {
	RecordPlayerObservation(context.Context, store.PlayerObservationWrite) error
	CleanupRawObservations(context.Context, time.Time, int) (int, error)
}

type runtimeRecorder interface {
	CurrentServerRuntime(context.Context) (store.ServerRuntimeState, error)
}

type Service struct {
	mu                  sync.Mutex
	recorder            Recorder
	maxGap              time.Duration
	movementThreshold   float64
	pingChangeThreshold float64
	maxSampleInterval   time.Duration
	rawRetention        time.Duration
	idGenerator         func() string
	asOf                time.Time
	online              map[string]playerState
	lastCleanup         time.Time
	continuityValid     bool
	pending             *pendingObservation
	runtime             store.ServerRuntimeState
	runtimeKnown        bool
}

type pendingObservation struct {
	write           store.PlayerObservationWrite
	next            map[string]playerState
	current         map[string]domain.Player
	at              time.Time
	correlationID   string
	continuityValid bool
}

type playerState struct {
	player              domain.Player
	lastAt              time.Time
	segmentID           string
	lastSampleAt        time.Time
	lastX               float64
	lastY               float64
	lastSamplePing      float64
	lastSamplePingKnown bool
	lastPrivateAt       time.Time
}

type eventPayload struct {
	PlayerID      string `json:"player_id,omitempty"`
	Name          string `json:"name,omitempty"`
	AccountName   string `json:"account_name,omitempty"`
	Level         int    `json:"level,omitempty"`
	BuildingCount int    `json:"building_count,omitempty"`
}

type attributeChange struct {
	Old any `json:"old"`
	New any `json:"new"`
}

type attributeChangedPayload struct {
	Changes map[string]attributeChange `json:"changes"`
}

func New(recorder Recorder, maxGap time.Duration, movementThreshold, pingChangeThreshold float64, maxSampleInterval, rawRetention time.Duration, idGenerator func() string) *Service {
	if recorder == nil {
		panic("observation: nil recorder")
	}
	if maxGap <= 0 {
		panic("observation: max gap must be positive")
	}
	if movementThreshold < 0 || math.IsNaN(movementThreshold) || math.IsInf(movementThreshold, 0) {
		panic("observation: movement threshold must be nonnegative and finite")
	}
	if pingChangeThreshold <= 0 || math.IsNaN(pingChangeThreshold) || math.IsInf(pingChangeThreshold, 0) {
		panic("observation: ping change threshold must be positive and finite")
	}
	if maxSampleInterval <= 0 {
		panic("observation: max sample interval must be positive")
	}
	if rawRetention <= 0 {
		panic("observation: raw retention must be positive")
	}
	if idGenerator == nil {
		panic("observation: nil ID generator")
	}
	return &Service{
		recorder: recorder, maxGap: maxGap, movementThreshold: movementThreshold, pingChangeThreshold: pingChangeThreshold,
		maxSampleInterval: maxSampleInterval, rawRetention: rawRetention,
		idGenerator: idGenerator, online: make(map[string]playerState),
	}
}

func (s *Service) Observe(ctx context.Context, at time.Time, players []domain.Player, correlationID string) error {
	if at.IsZero() {
		return fmt.Errorf("observe players: observation time is zero")
	}
	if strings.TrimSpace(correlationID) == "" {
		return fmt.Errorf("observe players: correlation ID is empty")
	}
	current, ordered, err := normalizePlayers(players)
	if err != nil {
		return fmt.Errorf("observe players: %w", err)
	}
	return s.observeNormalized(ctx, at.UTC(), current, ordered, correlationID, 0)
}

func (s *Service) observeNormalized(ctx context.Context, at time.Time, current map[string]domain.Player, ordered []domain.Player, correlationID string, attempt int) error {
	runtime := store.ServerRuntimeState{}
	if provider, ok := s.recorder.(runtimeRecorder); ok {
		var err error
		runtime, err = provider.CurrentServerRuntime(ctx)
		if err != nil {
			return fmt.Errorf("observe players: read server runtime: %w", err)
		}
	}

	s.mu.Lock()
	if s.pending != nil {
		pending := s.pending
		if err := s.recorder.RecordPlayerObservation(ctx, pending.write); err != nil {
			if !errors.Is(err, store.ErrObservationConflict) {
				s.mu.Unlock()
				return err
			}
			// A runtime conflict after an ambiguous result proves the exact
			// pending write did not commit (committed replays are accepted by
			// the repository). Drop it and make the interval unknown before
			// deriving the caller's observation against the current epoch.
			s.pending = nil
			s.continuityValid = false
		} else {
			s.online = pending.next
			s.asOf = pending.at
			s.continuityValid = pending.continuityValid
			s.pending = nil
			if at.Equal(pending.at) {
				same := correlationID == pending.correlationID && playerMapsEqual(current, pending.current)
				s.mu.Unlock()
				if !same {
					return fmt.Errorf("observe players: observation at %s differs from pending replay", at.Format(time.RFC3339Nano))
				}
				return nil
			}
		}
	}
	if !s.asOf.IsZero() && !at.After(s.asOf) {
		s.mu.Unlock()
		return fmt.Errorf("observe players: observation time %s must be after %s", at.Format(time.RFC3339Nano), s.asOf.Format(time.RFC3339Nano))
	}
	if !s.runtimeKnown {
		s.runtime = runtime
		s.runtimeKnown = true
	} else if !serverRuntimeEqual(s.runtime, runtime) {
		for userID, state := range s.online {
			state.segmentID = ""
			state.lastSampleAt = time.Time{}
			state.lastX = 0
			state.lastY = 0
			state.lastSamplePing = 0
			state.lastSamplePingKnown = false
			s.online[userID] = state
		}
		s.runtime = runtime
	}

	next := make(map[string]playerState, len(current))
	write := store.PlayerObservationWrite{Runtime: &runtime}
	for _, player := range ordered {
		previous, existed := s.online[player.UserID]
		continuous := existed && s.continuityValid && at.Sub(previous.lastAt) <= s.maxGap
		state := previous
		state.player = player
		state.lastAt = at
		if !existed {
			event, eventErr := s.playerEvent("player_joined", player, at, correlationID)
			if eventErr != nil {
				s.mu.Unlock()
				return eventErr
			}
			write.Events = append(write.Events, event)
			state = playerState{player: player, lastAt: at}
		} else if !s.continuityValid || at.Sub(previous.lastAt) > s.maxGap {
			state.segmentID = ""
			state.lastSampleAt = time.Time{}
			state.lastSamplePing = 0
			state.lastSamplePingKnown = false
			state.lastPrivateAt = time.Time{}
		}
		if continuous {
			event, changed, eventErr := s.playerAttributeChangedEvent(previous.player, player, at, correlationID)
			if eventErr != nil {
				s.mu.Unlock()
				return eventErr
			}
			if changed {
				write.Events = append(write.Events, event)
			}
		}

		shouldPrivateSample := state.lastPrivateAt.IsZero() ||
			player.IP != previous.player.IP || player.Level != previous.player.Level ||
			player.BuildingCount != previous.player.BuildingCount || at.Sub(state.lastPrivateAt) >= s.maxSampleInterval
		if player.IP != "" && shouldPrivateSample {
			ping, _ := normalizedPing(player.Ping)
			write.PrivateSamples = append(write.PrivateSamples, store.PlayerPrivateSample{
				UserID: player.UserID, ObservedAt: at, IP: player.IP, Ping: ping,
				Level: player.Level, BuildingCount: player.BuildingCount, SourceRef: correlationID,
			})
			state.lastPrivateAt = at
		}

		if validCoordinates(player.LocationX, player.LocationY) {
			shouldSample := state.segmentID == ""
			if !shouldSample {
				distance := math.Hypot(player.LocationX-state.lastX, player.LocationY-state.lastY)
				levelChanged := knownLevel(previous.player.Level) && knownLevel(player.Level) && previous.player.Level != player.Level
				currentPing, currentPingKnown := normalizedPing(player.Ping)
				pingChanged := knownPing(previous.player.Ping) && currentPingKnown && state.lastSamplePingKnown &&
					pingThresholdReached(state.lastSamplePing, currentPing, s.pingChangeThreshold)
				shouldSample = distance > s.movementThreshold || at.Sub(state.lastSampleAt) >= s.maxSampleInterval || levelChanged || pingChanged
			}
			if shouldSample {
				if state.segmentID == "" {
					segmentID, err := s.generateID("trajectory segment")
					if err != nil {
						s.mu.Unlock()
						return err
					}
					state.segmentID = segmentID
				}
				ping, pingKnown := normalizedPing(player.Ping)
				write.Trajectories = append(write.Trajectories, store.TrajectorySample{
					UserID: player.UserID, SegmentID: state.segmentID, ObservedAt: at,
					X: player.LocationX, Y: player.LocationY, Ping: ping, Level: player.Level,
					SourceRef: correlationID, RuntimeEpoch: runtime.Epoch,
				})
				state.lastSampleAt = at
				state.lastX = player.LocationX
				state.lastY = player.LocationY
				if pingKnown {
					state.lastSamplePing = ping
					state.lastSamplePingKnown = true
				}
			}
		} else {
			state.segmentID = ""
			state.lastSampleAt = time.Time{}
			state.lastSamplePing = 0
			state.lastSamplePingKnown = false
		}
		next[player.UserID] = state
	}

	leftIDs := make([]string, 0)
	for userID := range s.online {
		if _, exists := current[userID]; !exists {
			leftIDs = append(leftIDs, userID)
		}
	}
	sort.Strings(leftIDs)
	for _, userID := range leftIDs {
		event, eventErr := s.playerEvent("player_left", s.online[userID].player, at, correlationID)
		if eventErr != nil {
			s.mu.Unlock()
			return eventErr
		}
		write.Events = append(write.Events, event)
	}

	// Keep the complete write pending until the recorder confirms it. This also
	// covers an ambiguous commit result: the next call replays these exact IDs,
	// source references, and segment anchors before advancing the baseline.
	if err := s.recorder.RecordPlayerObservation(ctx, write); err != nil {
		if errors.Is(err, store.ErrObservationConflict) {
			s.mu.Unlock()
			if attempt >= 2 {
				return fmt.Errorf("observe players: %w after 3 attempts", store.ErrObservationConflict)
			}
			return s.observeNormalized(ctx, at, current, ordered, correlationID, attempt+1)
		}
		s.pending = &pendingObservation{
			write: write, next: next, current: current, at: at,
			correlationID: correlationID, continuityValid: true,
		}
		s.mu.Unlock()
		return err
	}
	s.online = next
	s.asOf = at
	s.continuityValid = true
	doCleanup := s.lastCleanup.IsZero() || at.Sub(s.lastCleanup) >= 24*time.Hour
	if doCleanup {
		s.lastCleanup = at
	}
	cutoff := at.Add(-s.rawRetention)
	s.mu.Unlock()

	if doCleanup {
		if _, err := s.recorder.CleanupRawObservations(ctx, cutoff, cleanupBatchSize); err != nil {
			slog.Warn("cleanup raw observations failed", "error", err)
		}
	}
	return nil
}

func serverRuntimeEqual(a, b store.ServerRuntimeState) bool {
	return a.Epoch == b.Epoch && a.RestartedAt.Equal(b.RestartedAt)
}

func knownLevel(level int) bool { return level > 0 }

func knownPing(ping float64) bool {
	return !math.IsNaN(ping) && !math.IsInf(ping, 0) && ping >= 0
}

func normalizedPing(ping float64) (float64, bool) {
	if !knownPing(ping) {
		return 0, false
	}
	return ping, true
}

func pingThresholdReached(previous, current, threshold float64) bool {
	return math.Abs(current-previous) >= threshold
}

// PollFailed marks the interval since the last successful player observation
// unknown without inventing leave/join events or discarding the last known
// player values. The next success starts fresh trajectory evidence.
func (s *Service) PollFailed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.continuityValid = false
	if s.pending != nil {
		s.pending.continuityValid = false
	}
}

func playerMapsEqual(a, b map[string]domain.Player) bool {
	if len(a) != len(b) {
		return false
	}
	for id, player := range a {
		if other, ok := b[id]; !ok || player != other {
			return false
		}
	}
	return true
}

func (s *Service) playerAttributeChangedEvent(previous, current domain.Player, at time.Time, correlationID string) (store.ActivityEvent, bool, error) {
	changes := make(map[string]attributeChange, 4)
	if previous.PlayerID != "" && current.PlayerID != "" && previous.PlayerID != current.PlayerID {
		changes["player_id"] = attributeChange{Old: previous.PlayerID, New: current.PlayerID}
	}
	if previous.Name != "" && current.Name != "" && previous.Name != current.Name {
		changes["name"] = attributeChange{Old: previous.Name, New: current.Name}
	}
	if knownLevel(previous.Level) && knownLevel(current.Level) && previous.Level != current.Level {
		changes["level"] = attributeChange{Old: previous.Level, New: current.Level}
	}
	if previous.BuildingCount != current.BuildingCount {
		changes["building_count"] = attributeChange{Old: previous.BuildingCount, New: current.BuildingCount}
	}
	if len(changes) == 0 {
		return store.ActivityEvent{}, false, nil
	}
	payload, err := json.Marshal(attributeChangedPayload{Changes: changes})
	if err != nil {
		return store.ActivityEvent{}, false, fmt.Errorf("observe players: encode player_attribute_changed payload: %w", err)
	}
	digest := sha256.Sum256([]byte(current.UserID + "\x00" + at.UTC().Format(time.RFC3339Nano) + "\x00" + correlationID + "\x00" + string(payload)))
	id := "player_attribute_changed_" + hex.EncodeToString(digest[:])
	return store.ActivityEvent{
		ID: id, EventType: "player_attribute_changed", SubjectType: "player", SubjectID: current.UserID,
		OccurredAt: at, ObservedAt: at, Source: "palworld_rest", SourceRef: correlationID,
		CorrelationID: correlationID, Confidence: "observed", SchemaVersion: 1, PayloadJSON: string(payload),
	}, true, nil
}

func (s *Service) playerEvent(eventType string, player domain.Player, at time.Time, correlationID string) (store.ActivityEvent, error) {
	id, err := s.generateID("activity event")
	if err != nil {
		return store.ActivityEvent{}, err
	}
	payload, err := json.Marshal(eventPayload{
		PlayerID: player.PlayerID, Name: player.Name, AccountName: player.AccountName,
		Level: player.Level, BuildingCount: player.BuildingCount,
	})
	if err != nil {
		return store.ActivityEvent{}, fmt.Errorf("observe players: encode %s payload: %w", eventType, err)
	}
	return store.ActivityEvent{
		ID: id, EventType: eventType, SubjectType: "player", SubjectID: player.UserID,
		OccurredAt: at, ObservedAt: at, Source: "palworld_rest", SourceRef: correlationID,
		CorrelationID: correlationID, Confidence: "observed", SchemaVersion: 1,
		PayloadJSON: string(payload),
	}, nil
}

func (s *Service) generateID(kind string) (string, error) {
	id := strings.TrimSpace(s.idGenerator())
	if id == "" {
		return "", fmt.Errorf("observe players: generated %s ID is empty", kind)
	}
	return id, nil
}

func normalizePlayers(players []domain.Player) (map[string]domain.Player, []domain.Player, error) {
	current := make(map[string]domain.Player, len(players))
	ordered := make([]domain.Player, 0, len(players))
	for _, player := range players {
		player.UserID = strings.TrimSpace(player.UserID)
		if player.UserID == "" {
			return nil, nil, fmt.Errorf("player user ID cannot be empty")
		}
		if _, exists := current[player.UserID]; exists {
			return nil, nil, fmt.Errorf("player user ID %q is duplicated", player.UserID)
		}
		current[player.UserID] = player
		ordered = append(ordered, player)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].UserID < ordered[j].UserID })
	return current, ordered, nil
}

func validCoordinates(x, y float64) bool {
	return !math.IsNaN(x) && !math.IsNaN(y) && !math.IsInf(x, 0) && !math.IsInf(y, 0) && (x != 0 || y != 0)
}
