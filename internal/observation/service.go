package observation

import (
	"context"
	"encoding/json"
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

type Service struct {
	mu                sync.Mutex
	recorder          Recorder
	maxGap            time.Duration
	movementThreshold float64
	maxSampleInterval time.Duration
	rawRetention      time.Duration
	idGenerator       func() string
	asOf              time.Time
	online            map[string]playerState
	lastCleanup       time.Time
}

type playerState struct {
	player        domain.Player
	lastAt        time.Time
	segmentID     string
	lastSampleAt  time.Time
	lastX         float64
	lastY         float64
	lastPrivateAt time.Time
}

type eventPayload struct {
	PlayerID      string `json:"player_id,omitempty"`
	Name          string `json:"name,omitempty"`
	AccountName   string `json:"account_name,omitempty"`
	Level         int    `json:"level,omitempty"`
	BuildingCount int    `json:"building_count,omitempty"`
}

func New(recorder Recorder, maxGap time.Duration, movementThreshold float64, maxSampleInterval, rawRetention time.Duration, idGenerator func() string) *Service {
	if recorder == nil {
		panic("observation: nil recorder")
	}
	if maxGap <= 0 {
		panic("observation: max gap must be positive")
	}
	if movementThreshold <= 0 || math.IsNaN(movementThreshold) || math.IsInf(movementThreshold, 0) {
		panic("observation: movement threshold must be positive and finite")
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
		recorder: recorder, maxGap: maxGap, movementThreshold: movementThreshold,
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
	at = at.UTC()

	s.mu.Lock()
	if !s.asOf.IsZero() && !at.After(s.asOf) {
		s.mu.Unlock()
		return fmt.Errorf("observe players: observation time %s must be after %s", at.Format(time.RFC3339Nano), s.asOf.Format(time.RFC3339Nano))
	}

	next := make(map[string]playerState, len(current))
	write := store.PlayerObservationWrite{}
	for _, player := range ordered {
		previous, existed := s.online[player.UserID]
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
		} else if at.Sub(previous.lastAt) > s.maxGap {
			state.segmentID = ""
			state.lastSampleAt = time.Time{}
			state.lastPrivateAt = time.Time{}
		}

		shouldPrivateSample := state.lastPrivateAt.IsZero() ||
			player.IP != previous.player.IP || player.Level != previous.player.Level ||
			player.BuildingCount != previous.player.BuildingCount || at.Sub(state.lastPrivateAt) >= s.maxSampleInterval
		if shouldPrivateSample {
			ping := player.Ping
			if math.IsNaN(ping) || math.IsInf(ping, 0) || ping < 0 {
				ping = 0
			}
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
				shouldSample = distance > s.movementThreshold || at.Sub(state.lastSampleAt) >= s.maxSampleInterval
			}
			if shouldSample {
				if state.segmentID == "" {
					state.segmentID, err = s.generateID("trajectory segment")
					if err != nil {
						s.mu.Unlock()
						return err
					}
				}
				ping := player.Ping
				if math.IsNaN(ping) || math.IsInf(ping, 0) {
					ping = 0
				}
				write.Trajectories = append(write.Trajectories, store.TrajectorySample{
					UserID: player.UserID, SegmentID: state.segmentID, ObservedAt: at,
					X: player.LocationX, Y: player.LocationY, Ping: ping, Level: player.Level,
					SourceRef: correlationID,
				})
				state.lastSampleAt = at
				state.lastX = player.LocationX
				state.lastY = player.LocationY
			}
		} else {
			state.segmentID = ""
			state.lastSampleAt = time.Time{}
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

	// Generated UUID-like values identify this persistence attempt. The recorder's
	// transaction prevents a failed attempt from partially committing; a retry may
	// use fresh IDs while deriving the same events and trajectory anchor.
	if err := s.recorder.RecordPlayerObservation(ctx, write); err != nil {
		s.mu.Unlock()
		return err
	}
	s.online = next
	s.asOf = at
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
