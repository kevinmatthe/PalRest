package observation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

const (
	DefaultMovementThreshold        = 100
	DefaultMaxSampleInterval        = 5 * time.Minute
	DefaultRawObservationRetention  = 90 * 24 * time.Hour
	DefaultServerDocumentInterval   = 5 * time.Minute
	DefaultServerObservationTimeout = 10 * time.Second
)

// ServerRepository is the narrow persistence surface needed to derive server
// activity events while retaining typed metric and canonical document storage.
type ServerRepository interface {
	RecordServerMetrics(context.Context, time.Time, domain.ServerMetrics) error
	RecordServerDocument(context.Context, string, time.Time, []byte, string) (bool, error)
	RecordPlayerObservation(context.Context, store.PlayerObservationWrite) error
}

type ServerService struct {
	repository  ServerRepository
	idGenerator func() string
	idMu        sync.Mutex

	metricsMu     sync.Mutex
	metrics       serverMetricBaseline
	pendingMetric *pendingMetricEvent

	infoMu      sync.Mutex
	info        serverDocumentBaseline
	pendingInfo *pendingDocumentEvent

	settingsMu      sync.Mutex
	settings        serverDocumentBaseline
	pendingSettings *pendingDocumentEvent
}

type serverMetricBaseline struct {
	valid  bool
	at     time.Time
	uptime int64
}

type pendingMetricEvent struct {
	at      time.Time
	metrics domain.ServerMetrics
	event   store.ActivityEvent
}

type serverDocumentBaseline struct {
	valid   bool
	at      time.Time
	hash    string
	version string
}

type pendingDocumentEvent struct {
	at        time.Time
	hash      string
	version   string
	eventType string
	event     store.ActivityEvent
}

func NewServer(repository ServerRepository, idGenerator func() string) *ServerService {
	if repository == nil {
		panic("observation: nil server repository")
	}
	if idGenerator == nil {
		idGenerator = NewID
	}
	return &ServerService{repository: repository, idGenerator: idGenerator}
}

// NewID returns a cryptographically random identifier suitable for immutable
// observation IDs and correlations. An empty result reports entropy failure to
// callers through their normal generated-ID validation path.
func NewID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return ""
	}
	return hex.EncodeToString(bytes)
}

func (s *ServerService) RecordMetrics(ctx context.Context, at time.Time, metrics domain.ServerMetrics) error {
	if at.IsZero() {
		return fmt.Errorf("record observed server metrics: observation time is zero")
	}
	at = at.UTC()
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()

	if s.pendingMetric != nil {
		pendingAt := s.pendingMetric.at
		if at.Before(pendingAt) {
			return fmt.Errorf("record observed server metrics: observation time %s is before pending event at %s", at.Format(time.RFC3339Nano), pendingAt.Format(time.RFC3339Nano))
		}
		if at.Equal(pendingAt) && !reflect.DeepEqual(metrics, s.pendingMetric.metrics) {
			return fmt.Errorf("record observed server metrics: retry at %s does not match persisted metrics", at.Format(time.RFC3339Nano))
		}
		if err := s.finishPendingMetric(ctx); err != nil {
			return err
		}
		if at.Equal(pendingAt) {
			return nil
		}
	}
	if s.metrics.valid && !at.After(s.metrics.at) {
		return fmt.Errorf("record observed server metrics: observation time %s must be after %s", at.Format(time.RFC3339Nano), s.metrics.at.Format(time.RFC3339Nano))
	}
	if err := s.repository.RecordServerMetrics(ctx, at, metrics); err != nil {
		return fmt.Errorf("record observed server metrics: %w", err)
	}
	if s.metrics.valid && metrics.UptimeSeconds < s.metrics.uptime {
		s.pendingMetric = &pendingMetricEvent{at: at, metrics: metrics}
		if err := s.finishPendingMetric(ctx); err != nil {
			return err
		}
		return nil
	}
	s.metrics = serverMetricBaseline{valid: true, at: at, uptime: metrics.UptimeSeconds}
	return nil
}

func (s *ServerService) finishPendingMetric(ctx context.Context) error {
	pending := s.pendingMetric
	if pending.event.ID == "" {
		event, err := s.newRestartEvent(pending.at, pending.metrics.UptimeSeconds, s.metrics.uptime)
		if err != nil {
			return err
		}
		pending.event = event
	}
	if err := s.recordEvent(ctx, pending.event); err != nil {
		return err
	}
	s.metrics = serverMetricBaseline{valid: true, at: pending.at, uptime: pending.metrics.UptimeSeconds}
	s.pendingMetric = nil
	return nil
}

func (s *ServerService) RecordInfo(ctx context.Context, at time.Time, info domain.ServerInfo) error {
	canonical, hash, err := canonicalDocument(info)
	if err != nil {
		return fmt.Errorf("record observed server info: %w", err)
	}
	s.infoMu.Lock()
	defer s.infoMu.Unlock()
	return s.recordDocument(ctx, "info", at, canonical, hash, info.Version, &s.info, &s.pendingInfo)
}

func (s *ServerService) RecordSettings(ctx context.Context, at time.Time, settings domain.ServerSettings) error {
	canonical, hash, err := canonicalSettings(settings.Values)
	if err != nil {
		return fmt.Errorf("record observed server settings: %w", err)
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	return s.recordDocument(ctx, "settings", at, canonical, hash, "", &s.settings, &s.pendingSettings)
}

func (s *ServerService) recordDocument(ctx context.Context, kind string, at time.Time, canonical []byte, hash, version string, baseline *serverDocumentBaseline, pending **pendingDocumentEvent) error {
	if at.IsZero() {
		return fmt.Errorf("record observed server %s: observation time is zero", kind)
	}
	at = at.UTC()
	if *pending != nil {
		attempt := *pending
		if at.Before(attempt.at) {
			return fmt.Errorf("record observed server %s: observation time %s is before pending event at %s", kind, at.Format(time.RFC3339Nano), attempt.at.Format(time.RFC3339Nano))
		}
		if at.Equal(attempt.at) && (hash != attempt.hash || version != attempt.version) {
			return fmt.Errorf("record observed server %s: retry at %s does not match persisted document", kind, at.Format(time.RFC3339Nano))
		}
		if err := s.finishPendingDocument(ctx, baseline, pending); err != nil {
			return err
		}
		if at.Equal(attempt.at) {
			return nil
		}
	}
	if baseline.valid && !at.After(baseline.at) {
		return fmt.Errorf("record observed server %s: observation time %s must be after %s", kind, at.Format(time.RFC3339Nano), baseline.at.Format(time.RFC3339Nano))
	}
	inserted, err := s.repository.RecordServerDocument(ctx, kind, at, canonical, hash)
	if err != nil {
		return fmt.Errorf("record observed server %s: %w", kind, err)
	}
	if !baseline.valid {
		*baseline = serverDocumentBaseline{valid: true, at: at, hash: hash, version: version}
		return nil
	}
	eventType := ""
	if inserted && hash != baseline.hash {
		switch {
		case kind == "settings":
			eventType = "server_settings_changed"
		case version != baseline.version:
			eventType = "server_version_changed"
		}
	}
	if eventType != "" {
		*pending = &pendingDocumentEvent{at: at, hash: hash, version: version, eventType: eventType}
		if err := s.finishPendingDocument(ctx, baseline, pending); err != nil {
			return err
		}
		return nil
	}
	*baseline = serverDocumentBaseline{valid: true, at: at, hash: hash, version: version}
	return nil
}

func (s *ServerService) finishPendingDocument(ctx context.Context, baseline *serverDocumentBaseline, pending **pendingDocumentEvent) error {
	attempt := *pending
	if attempt.event.ID == "" {
		event, err := s.newDocumentEvent(attempt.eventType, attempt.at, baseline.hash, attempt.hash, baseline.version, attempt.version)
		if err != nil {
			return err
		}
		attempt.event = event
	}
	if err := s.recordEvent(ctx, attempt.event); err != nil {
		return err
	}
	*baseline = serverDocumentBaseline{valid: true, at: attempt.at, hash: attempt.hash, version: attempt.version}
	*pending = nil
	return nil
}

func (s *ServerService) newRestartEvent(at time.Time, newUptime, oldUptime int64) (store.ActivityEvent, error) {
	payload, err := json.Marshal(struct {
		Old int64 `json:"old_uptime_seconds"`
		New int64 `json:"new_uptime_seconds"`
	}{Old: oldUptime, New: newUptime})
	if err != nil {
		return store.ActivityEvent{}, fmt.Errorf("record observed server restart: encode payload: %w", err)
	}
	return s.newEvent("server_restarted", at, payload)
}

func (s *ServerService) newDocumentEvent(eventType string, at time.Time, oldHash, newHash, oldVersion, newVersion string) (store.ActivityEvent, error) {
	var payload []byte
	var err error
	if eventType == "server_settings_changed" {
		payload, err = json.Marshal(struct {
			OldHash string `json:"old_hash"`
			NewHash string `json:"new_hash"`
			Summary string `json:"summary"`
		}{OldHash: oldHash, NewHash: newHash, Summary: "server settings changed"})
	} else {
		payload, err = json.Marshal(struct {
			OldHash    string `json:"old_hash"`
			NewHash    string `json:"new_hash"`
			OldVersion string `json:"old_version"`
			NewVersion string `json:"new_version"`
		}{OldHash: oldHash, NewHash: newHash, OldVersion: oldVersion, NewVersion: newVersion})
	}
	if err != nil {
		return store.ActivityEvent{}, fmt.Errorf("record observed server document event: encode payload: %w", err)
	}
	return s.newEvent(eventType, at, payload)
}

func (s *ServerService) newEvent(eventType string, at time.Time, payload []byte) (store.ActivityEvent, error) {
	eventID, err := s.generateID("event")
	if err != nil {
		return store.ActivityEvent{}, err
	}
	correlationID, err := s.generateID("correlation")
	if err != nil {
		return store.ActivityEvent{}, err
	}
	return store.ActivityEvent{
		ID: eventID, EventType: eventType, SubjectType: "server", SubjectID: "server",
		OccurredAt: at.UTC(), ObservedAt: at.UTC(), Source: "palworld_rest", SourceRef: correlationID,
		CorrelationID: correlationID, Confidence: "observed", SchemaVersion: 1, PayloadJSON: string(payload),
	}, nil
}

// Recorder errors are treated as definitive failures. The exact immutable
// event (including its ID) is retained for retry; the current repository
// contract does not expose a lookup that could prove an ambiguous commit.
func (s *ServerService) recordEvent(ctx context.Context, event store.ActivityEvent) error {
	if err := s.repository.RecordPlayerObservation(ctx, store.PlayerObservationWrite{Events: []store.ActivityEvent{event}}); err != nil {
		return fmt.Errorf("record observed server event %q: %w", event.EventType, err)
	}
	return nil
}

func (s *ServerService) generateID(kind string) (string, error) {
	s.idMu.Lock()
	id := strings.TrimSpace(s.idGenerator())
	s.idMu.Unlock()
	if id == "" {
		return "", fmt.Errorf("record observed server: generated %s ID is empty", kind)
	}
	return id, nil
}

func canonicalDocument(value any) ([]byte, string, error) {
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, "", fmt.Errorf("encode canonical document: %w", err)
	}
	return canonical, documentHash(canonical), nil
}

func canonicalSettings(values map[string]any) ([]byte, string, error) {
	normalized, err := canonicalJSONValue(values)
	if err != nil {
		return nil, "", err
	}
	object, ok := normalized.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("settings must be a top-level object")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, "", fmt.Errorf("encode canonical settings: %w", err)
	}
	return canonical, documentHash(canonical), nil
}

func canonicalJSONValue(value any) (any, error) {
	switch value := value.(type) {
	case nil, bool, string:
		return value, nil
	case json.Number:
		number, err := value.Float64()
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fmt.Errorf("settings contain an invalid number")
		}
		return number, nil
	case float32:
		return canonicalFloat(float64(value))
	case float64:
		return canonicalFloat(value)
	case int:
		return float64(value), nil
	case int8:
		return float64(value), nil
	case int16:
		return float64(value), nil
	case int32:
		return float64(value), nil
	case int64:
		return float64(value), nil
	case uint:
		return float64(value), nil
	case uint8:
		return float64(value), nil
	case uint16:
		return float64(value), nil
	case uint32:
		return float64(value), nil
	case uint64:
		return float64(value), nil
	case []any:
		result := make([]any, len(value))
		for index, child := range value {
			normalized, err := canonicalJSONValue(child)
			if err != nil {
				return nil, err
			}
			result[index] = normalized
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, child := range value {
			normalized, err := canonicalJSONValue(child)
			if err != nil {
				return nil, err
			}
			result[key] = normalized
		}
		return result, nil
	default:
		return nil, fmt.Errorf("settings contain unsupported value %T", value)
	}
}

func canonicalFloat(value float64) (float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("settings contain a non-finite number")
	}
	return value, nil
}

func documentHash(canonical []byte) string {
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:])
}
