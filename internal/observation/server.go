package observation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

const (
	DefaultMovementThreshold       = 100
	DefaultMaxSampleInterval       = 5 * time.Minute
	DefaultRawObservationRetention = 90 * 24 * time.Hour
)

// ServerRepository atomically persists derived server facts with their
// optional immutable event and exposes the durable baseline used for the next
// transition. Implementations must accept exact committed replays.
type ServerRepository interface {
	RecordServerMetricObservation(context.Context, store.ServerMetricObservation) error
	LatestServerMetrics(context.Context) (time.Time, domain.ServerMetrics, error)
	RecordServerDocumentObservation(context.Context, store.ServerDocumentObservation) (bool, error)
	LatestServerDocument(context.Context, string) (store.ServerDocumentSnapshot, error)
}

type ServerService struct {
	repository  ServerRepository
	idGenerator func() string
	idMu        sync.Mutex

	metricsMu sync.Mutex
	metrics   serverMetricBaseline

	infoMu sync.Mutex
	info   serverDocumentBaseline

	settingsMu sync.Mutex
	settings   serverDocumentBaseline
}

type serverMetricBaseline struct {
	valid  bool
	at     time.Time
	uptime int64
}

type serverDocumentBaseline struct {
	valid   bool
	at      time.Time
	hash    string
	version string
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

// Restore warms process-local ordering caches from durable state. Record
// methods still reconcile with the repository before every transition, so an
// ambiguous prior commit or another writer cannot make this cache authoritative.
func (s *ServerService) Restore(ctx context.Context) error {
	metricAt, metrics, metricErr := s.repository.LatestServerMetrics(ctx)
	if metricErr != nil && !errors.Is(metricErr, store.ErrNotFound) {
		return fmt.Errorf("restore observed server metrics: %w", metricErr)
	}
	info, infoErr := s.repository.LatestServerDocument(ctx, "info")
	if infoErr != nil && !errors.Is(infoErr, store.ErrNotFound) {
		return fmt.Errorf("restore observed server info: %w", infoErr)
	}
	settings, settingsErr := s.repository.LatestServerDocument(ctx, "settings")
	if settingsErr != nil && !errors.Is(settingsErr, store.ErrNotFound) {
		return fmt.Errorf("restore observed server settings: %w", settingsErr)
	}

	var restoredInfo domain.ServerInfo
	if infoErr == nil {
		if err := json.Unmarshal(info.Canonical, &restoredInfo); err != nil {
			return fmt.Errorf("restore observed server info: decode canonical document: %w", err)
		}
	}

	s.metricsMu.Lock()
	s.infoMu.Lock()
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	defer s.infoMu.Unlock()
	defer s.metricsMu.Unlock()
	if metricErr == nil {
		s.metrics = serverMetricBaseline{valid: true, at: metricAt, uptime: metrics.UptimeSeconds}
	} else {
		s.metrics = serverMetricBaseline{}
	}
	if infoErr == nil {
		s.info = serverDocumentBaseline{valid: true, at: info.At, hash: info.Hash, version: restoredInfo.Version}
	} else {
		s.info = serverDocumentBaseline{}
	}
	if settingsErr == nil {
		s.settings = serverDocumentBaseline{valid: true, at: settings.At, hash: settings.Hash}
	} else {
		s.settings = serverDocumentBaseline{}
	}
	return nil
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

func (s *ServerService) generateID(kind string) (string, error) {
	s.idMu.Lock()
	id := strings.TrimSpace(s.idGenerator())
	s.idMu.Unlock()
	if id == "" {
		return "", fmt.Errorf("record observed server: generated %s ID is empty", kind)
	}
	return id, nil
}
