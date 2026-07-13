package observation_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/observation"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type serverRecorderFake struct {
	mu sync.Mutex

	metricsCalls  []metricCall
	documentCalls []documentCall
	writes        []store.PlayerObservationWrite

	metricErrors      []error
	documentErrors    []error
	eventErrors       []error
	inserted          []bool
	metricEntered     chan struct{}
	metricRelease     chan struct{}
	latestMetricAt    time.Time
	latestMetrics     domain.ServerMetrics
	latestMetricEvent *store.ActivityEvent
	latestDocuments   map[string]store.ServerDocumentSnapshot
}

type metricCall struct {
	at      time.Time
	metrics domain.ServerMetrics
}

type documentCall struct {
	kind      string
	at        time.Time
	canonical []byte
	hash      string
}

type atomicRepositorySpy struct {
	delegate          *store.Repository
	mu                sync.Mutex
	metrics           []store.ServerMetricObservation
	documents         []store.ServerDocumentObservation
	metricAmbiguous   bool
	documentAmbiguous map[string]bool
}

type interleavingRepository struct {
	delegate  *store.Repository
	once      sync.Once
	hook      func()
	mu        sync.Mutex
	metrics   []store.ServerMetricObservation
	documents []store.ServerDocumentObservation
}

func (r *interleavingRepository) RecordServerMetricObservation(ctx context.Context, write store.ServerMetricObservation) error {
	r.mu.Lock()
	r.metrics = append(r.metrics, write)
	r.mu.Unlock()
	r.once.Do(r.hook)
	return r.delegate.RecordServerMetricObservation(ctx, write)
}

func (r *interleavingRepository) LatestServerMetricObservation(ctx context.Context) (store.ServerMetricSnapshot, error) {
	return r.delegate.LatestServerMetricObservation(ctx)
}

func (r *interleavingRepository) RecordServerDocumentObservation(ctx context.Context, write store.ServerDocumentObservation) (bool, error) {
	r.mu.Lock()
	r.documents = append(r.documents, write)
	r.mu.Unlock()
	r.once.Do(r.hook)
	return r.delegate.RecordServerDocumentObservation(ctx, write)
}

func (r *interleavingRepository) LatestServerDocument(ctx context.Context, kind string) (store.ServerDocumentSnapshot, error) {
	return r.delegate.LatestServerDocument(ctx, kind)
}

func (r *atomicRepositorySpy) RecordServerMetricObservation(ctx context.Context, write store.ServerMetricObservation) error {
	r.mu.Lock()
	r.metrics = append(r.metrics, write)
	ambiguous := r.metricAmbiguous
	r.metricAmbiguous = false
	r.mu.Unlock()
	if err := r.delegate.RecordServerMetricObservation(ctx, write); err != nil {
		return err
	}
	if ambiguous {
		return errors.New("ambiguous metric commit")
	}
	return nil
}

func (r *atomicRepositorySpy) LatestServerMetricObservation(ctx context.Context) (store.ServerMetricSnapshot, error) {
	return r.delegate.LatestServerMetricObservation(ctx)
}

func (r *atomicRepositorySpy) RecordServerDocumentObservation(ctx context.Context, write store.ServerDocumentObservation) (bool, error) {
	r.mu.Lock()
	r.documents = append(r.documents, write)
	ambiguous := r.documentAmbiguous != nil && r.documentAmbiguous[write.Kind]
	if ambiguous {
		r.documentAmbiguous[write.Kind] = false
	}
	r.mu.Unlock()
	changed, err := r.delegate.RecordServerDocumentObservation(ctx, write)
	if err != nil {
		return false, err
	}
	if ambiguous {
		return false, errors.New("ambiguous document commit")
	}
	return changed, nil
}

func (r *atomicRepositorySpy) LatestServerDocument(ctx context.Context, kind string) (store.ServerDocumentSnapshot, error) {
	return r.delegate.LatestServerDocument(ctx, kind)
}

// Compatibility methods keep the RED test buildable against the pre-redesign
// service interface. The redesigned service must use the atomic methods above.
func (r *atomicRepositorySpy) RecordServerMetrics(ctx context.Context, at time.Time, metrics domain.ServerMetrics) error {
	return r.delegate.RecordServerMetrics(ctx, at, metrics)
}

func (r *atomicRepositorySpy) RecordServerDocument(ctx context.Context, kind string, at time.Time, canonical []byte, hash string) (bool, error) {
	return r.delegate.RecordServerDocument(ctx, kind, at, canonical, hash)
}

func (r *atomicRepositorySpy) RecordPlayerObservation(ctx context.Context, write store.PlayerObservationWrite) error {
	return r.delegate.RecordPlayerObservation(ctx, write)
}

func openServerRepository(t *testing.T) (*store.Repository, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "guard.db")
	repo, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	return repo, path
}

func (r *serverRecorderFake) RecordServerMetrics(_ context.Context, at time.Time, metrics domain.ServerMetrics) error {
	r.mu.Lock()
	r.metricsCalls = append(r.metricsCalls, metricCall{at: at, metrics: metrics})
	index := len(r.metricsCalls) - 1
	err := errorAt(r.metricErrors, index)
	entered, release := r.metricEntered, r.metricRelease
	r.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	return err
}

func (r *serverRecorderFake) RecordServerDocument(_ context.Context, kind string, at time.Time, canonical []byte, hash string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.documentCalls = append(r.documentCalls, documentCall{kind: kind, at: at, canonical: append([]byte(nil), canonical...), hash: hash})
	index := len(r.documentCalls) - 1
	if err := errorAt(r.documentErrors, index); err != nil {
		return false, err
	}
	if index < len(r.inserted) {
		return r.inserted[index], nil
	}
	return true, nil
}

func (r *serverRecorderFake) RecordPlayerObservation(_ context.Context, write store.PlayerObservationWrite) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := len(r.writes)
	r.writes = append(r.writes, write)
	return errorAt(r.eventErrors, index)
}

func (r *serverRecorderFake) RecordServerMetricObservation(ctx context.Context, write store.ServerMetricObservation) error {
	if err := r.RecordServerMetrics(ctx, write.At, write.Metrics); err != nil {
		return err
	}
	if write.Event != nil {
		if err := r.RecordPlayerObservation(ctx, store.PlayerObservationWrite{Events: []store.ActivityEvent{*write.Event}}); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.latestMetricAt = write.At.UTC()
	r.latestMetrics = write.Metrics
	r.latestMetricEvent = cloneActivityEvent(write.Event)
	r.mu.Unlock()
	return nil
}

func (r *serverRecorderFake) LatestServerMetrics(context.Context) (time.Time, domain.ServerMetrics, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.latestMetricAt.IsZero() {
		return time.Time{}, domain.ServerMetrics{}, store.ErrNotFound
	}
	return r.latestMetricAt, r.latestMetrics, nil
}

func (r *serverRecorderFake) LatestServerMetricObservation(context.Context) (store.ServerMetricSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.latestMetricAt.IsZero() {
		return store.ServerMetricSnapshot{}, store.ErrNotFound
	}
	return store.ServerMetricSnapshot{At: r.latestMetricAt, Metrics: r.latestMetrics, Event: cloneActivityEvent(r.latestMetricEvent)}, nil
}

func (r *serverRecorderFake) RecordServerDocumentObservation(ctx context.Context, write store.ServerDocumentObservation) (bool, error) {
	if _, err := r.RecordServerDocument(ctx, write.Kind, write.At, write.Canonical, write.Hash); err != nil {
		return false, err
	}
	if write.Event != nil {
		if err := r.RecordPlayerObservation(ctx, store.PlayerObservationWrite{Events: []store.ActivityEvent{*write.Event}}); err != nil {
			return false, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.latestDocuments == nil {
		r.latestDocuments = make(map[string]store.ServerDocumentSnapshot)
	}
	previous, exists := r.latestDocuments[write.Kind]
	if exists && previous.Hash == write.Hash {
		previous.At = write.At.UTC()
		previous.Event = nil
		r.latestDocuments[write.Kind] = previous
		return false, nil
	}
	r.latestDocuments[write.Kind] = store.ServerDocumentSnapshot{
		Kind: write.Kind, At: write.At.UTC(), Hash: write.Hash, Canonical: append([]byte(nil), write.Canonical...), Event: cloneActivityEvent(write.Event),
	}
	return true, nil
}

func (r *serverRecorderFake) LatestServerDocument(_ context.Context, kind string) (store.ServerDocumentSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	document, ok := r.latestDocuments[kind]
	if !ok {
		return store.ServerDocumentSnapshot{}, store.ErrNotFound
	}
	document.Canonical = append([]byte(nil), document.Canonical...)
	document.Event = cloneActivityEvent(document.Event)
	return document, nil
}

func cloneActivityEvent(event *store.ActivityEvent) *store.ActivityEvent {
	if event == nil {
		return nil
	}
	cloned := *event
	return &cloned
}

func errorAt(errors []error, index int) error {
	if index < len(errors) {
		return errors[index]
	}
	return nil
}

func newServerService(recorder *serverRecorderFake) *observation.ServerService {
	sequence := 0
	return observation.NewServer(recorder, func() string {
		sequence++
		return "server-id-" + string(rune('0'+sequence))
	})
}

func TestServerMetricsDetectRestartOnlyAfterSuccessfulNewerSample(t *testing.T) {
	recorder := &serverRecorderFake{}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))

	if err := service.RecordMetrics(t.Context(), base, domain.ServerMetrics{UptimeSeconds: 100, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordMetrics(t.Context(), base.Add(time.Minute), domain.ServerMetrics{UptimeSeconds: 100, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordMetrics(t.Context(), base.Add(2*time.Minute), domain.ServerMetrics{UptimeSeconds: 4, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.writes) != 1 || len(recorder.writes[0].Events) != 1 {
		t.Fatalf("event writes=%#v", recorder.writes)
	}
	event := recorder.writes[0].Events[0]
	if event.ID != "server-id-1" || event.EventType != "server_restarted" || event.SubjectType != "server" || event.SubjectID != "server" {
		t.Fatalf("event=%+v", event)
	}
	if event.OccurredAt != base.Add(2*time.Minute).UTC() || event.ObservedAt != base.Add(2*time.Minute).UTC() || event.Source != "palworld_rest" || event.Confidence != "observed" || event.SchemaVersion != 1 {
		t.Fatalf("metadata=%+v", event)
	}
	if event.SourceRef != "server-id-2" || event.CorrelationID != "server-id-2" {
		t.Fatalf("correlation metadata=%+v", event)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["old_uptime_seconds"] != float64(100) || payload["new_uptime_seconds"] != float64(4) {
		t.Fatalf("payload=%v", payload)
	}
}

func TestServerMetricFailuresAndEventFailuresDoNotAdvanceBaseline(t *testing.T) {
	metricErr := errors.New("metric failed")
	eventErr := errors.New("event failed")
	recorder := &serverRecorderFake{metricErrors: []error{nil, metricErr, nil, nil}, eventErrors: []error{eventErr, nil}}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)

	if err := service.RecordMetrics(t.Context(), base, domain.ServerMetrics{UptimeSeconds: 100, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordMetrics(t.Context(), base.Add(time.Minute), domain.ServerMetrics{UptimeSeconds: 1, ServerFrameTime: 1}); !errors.Is(err, metricErr) {
		t.Fatalf("metric err=%v", err)
	}
	if err := service.RecordMetrics(t.Context(), base.Add(time.Minute), domain.ServerMetrics{UptimeSeconds: 1, ServerFrameTime: 1}); !errors.Is(err, eventErr) {
		t.Fatalf("event err=%v", err)
	}
	// The metric is already durable after the event error. Retrying the same call
	// must retry the derived event without asking the repository to insert the
	// same timestamp again.
	if err := service.RecordMetrics(t.Context(), base.Add(time.Minute), domain.ServerMetrics{UptimeSeconds: 1, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.metricsCalls) != 4 || len(recorder.writes) != 2 {
		t.Fatalf("metrics=%d events=%d", len(recorder.metricsCalls), len(recorder.writes))
	}
	for _, write := range recorder.writes {
		if len(write.Events) != 1 || write.Events[0].EventType != "server_restarted" {
			t.Fatalf("write=%+v", write)
		}
	}
	if err := service.RecordMetrics(t.Context(), base.Add(30*time.Second), domain.ServerMetrics{UptimeSeconds: 200, ServerFrameTime: 1}); err == nil {
		t.Fatal("expected older metric sample to be rejected")
	}
	if len(recorder.metricsCalls) != 4 {
		t.Fatal("older sample reached repository")
	}
}

func TestServerMetricsFailedAtomicRestartDoesNotBlockNewerSample(t *testing.T) {
	eventErr := errors.New("event failed")
	recorder := &serverRecorderFake{eventErrors: []error{eventErr, nil}}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)

	if err := service.RecordMetrics(t.Context(), base, domain.ServerMetrics{UptimeSeconds: 100, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordMetrics(t.Context(), base.Add(time.Minute), domain.ServerMetrics{UptimeSeconds: 1, ServerFrameTime: 1}); !errors.Is(err, eventErr) {
		t.Fatalf("restart err=%v", err)
	}
	if err := service.RecordMetrics(t.Context(), base.Add(2*time.Minute), domain.ServerMetrics{UptimeSeconds: 2, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordMetrics(t.Context(), base.Add(3*time.Minute), domain.ServerMetrics{UptimeSeconds: 3, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.metricsCalls) != 4 {
		t.Fatalf("metric calls=%#v", recorder.metricsCalls)
	}
	for index, at := range []time.Time{base, base.Add(time.Minute), base.Add(2 * time.Minute), base.Add(3 * time.Minute)} {
		if recorder.metricsCalls[index].at != at {
			t.Fatalf("metric call %d at=%s want=%s", index, recorder.metricsCalls[index].at, at)
		}
	}
	if len(recorder.writes) != 2 {
		t.Fatalf("event attempts=%#v", recorder.writes)
	}
	failed, retried := recorder.writes[0].Events[0], recorder.writes[1].Events[0]
	if failed.ID == retried.ID || failed.OccurredAt != base.Add(time.Minute) || retried.OccurredAt != base.Add(2*time.Minute) {
		t.Fatalf("failed=%+v retried=%+v", failed, retried)
	}
}

func TestServerMetricsRepeatedAtomicFailuresDoNotAdvanceDurableBaseline(t *testing.T) {
	firstErr := errors.New("first event failure")
	secondErr := errors.New("second event failure")
	recorder := &serverRecorderFake{eventErrors: []error{firstErr, secondErr, nil}}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)

	if err := service.RecordMetrics(t.Context(), base, domain.ServerMetrics{UptimeSeconds: 100, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordMetrics(t.Context(), base.Add(time.Minute), domain.ServerMetrics{UptimeSeconds: 1, ServerFrameTime: 1}); !errors.Is(err, firstErr) {
		t.Fatalf("first err=%v", err)
	}
	if err := service.RecordMetrics(t.Context(), base.Add(2*time.Minute), domain.ServerMetrics{UptimeSeconds: 2, ServerFrameTime: 1}); !errors.Is(err, secondErr) {
		t.Fatalf("second err=%v", err)
	}
	if len(recorder.metricsCalls) != 3 {
		t.Fatalf("new sample persisted while pending retry failed: %#v", recorder.metricsCalls)
	}
	if err := service.RecordMetrics(t.Context(), base.Add(3*time.Minute), domain.ServerMetrics{UptimeSeconds: 3, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.metricsCalls) != 4 || recorder.metricsCalls[3].at != base.Add(3*time.Minute) {
		t.Fatalf("metric calls=%#v", recorder.metricsCalls)
	}
	for index, at := range []time.Time{base.Add(time.Minute), base.Add(2 * time.Minute), base.Add(3 * time.Minute)} {
		if recorder.writes[index].Events[0].OccurredAt != at {
			t.Fatalf("event attempts=%#v", recorder.writes)
		}
	}
}

func TestServerInfoCanonicalDocumentAndVersionChangeRetry(t *testing.T) {
	eventErr := errors.New("event failed")
	recorder := &serverRecorderFake{eventErrors: []error{eventErr, nil}, inserted: []bool{true, true, false}}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	old := domain.ServerInfo{Version: "v1", ServerName: "server", Description: "hello", WorldGUID: "world"}
	next := old
	next.Version = "v2"

	if err := service.RecordInfo(t.Context(), base, old); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordInfo(t.Context(), base.Add(time.Minute), next); !errors.Is(err, eventErr) {
		t.Fatalf("err=%v", err)
	}
	if err := service.RecordInfo(t.Context(), base.Add(time.Minute), next); err != nil {
		t.Fatal(err)
	}
	if len(recorder.documentCalls) != 3 {
		t.Fatalf("documents=%d", len(recorder.documentCalls))
	}
	if got := string(recorder.documentCalls[0].canonical); got != `{"version":"v1","servername":"server","description":"hello","worldguid":"world"}` {
		t.Fatalf("canonical=%s", got)
	}
	if len(recorder.documentCalls[0].hash) != 64 || recorder.documentCalls[0].hash == recorder.documentCalls[1].hash {
		t.Fatalf("hashes=%q %q", recorder.documentCalls[0].hash, recorder.documentCalls[1].hash)
	}
	if len(recorder.writes) != 2 {
		t.Fatalf("writes=%#v", recorder.writes)
	}
	for _, write := range recorder.writes {
		event := write.Events[0]
		if event.EventType != "server_version_changed" || strings.Contains(event.PayloadJSON, "hello") || strings.Contains(event.PayloadJSON, "world") {
			t.Fatalf("event=%+v", event)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["old_version"] != "v1" || payload["new_version"] != "v2" {
			t.Fatalf("payload=%v", payload)
		}
	}
}

func TestServerInfoDocumentFailureDoesNotEstablishBaseline(t *testing.T) {
	docErr := errors.New("document failed")
	recorder := &serverRecorderFake{documentErrors: []error{docErr, nil, nil}}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	info := domain.ServerInfo{Version: "v1"}
	if err := service.RecordInfo(t.Context(), base, info); !errors.Is(err, docErr) {
		t.Fatalf("err=%v", err)
	}
	if err := service.RecordInfo(t.Context(), base, info); err != nil {
		t.Fatal(err)
	}
	info.Version = "v2"
	if err := service.RecordInfo(t.Context(), base.Add(time.Minute), info); err != nil {
		t.Fatal(err)
	}
	if len(recorder.writes) != 1 || recorder.writes[0].Events[0].EventType != "server_version_changed" {
		t.Fatalf("writes=%#v", recorder.writes)
	}
}

func TestServerInfoFailedAtomicChangeDoesNotBlockNewerDocument(t *testing.T) {
	eventErr := errors.New("event failed")
	recorder := &serverRecorderFake{eventErrors: []error{eventErr, nil, nil}}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)

	for index, version := range []string{"v1", "v2"} {
		err := service.RecordInfo(t.Context(), base.Add(time.Duration(index)*time.Minute), domain.ServerInfo{Version: version})
		if index == 1 {
			if !errors.Is(err, eventErr) {
				t.Fatalf("change err=%v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if err := service.RecordInfo(t.Context(), base.Add(2*time.Minute), domain.ServerInfo{Version: "v3"}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordInfo(t.Context(), base.Add(3*time.Minute), domain.ServerInfo{Version: "v3", ServerName: "renamed"}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.documentCalls) != 4 {
		t.Fatalf("document calls=%#v", recorder.documentCalls)
	}
	if len(recorder.writes) != 2 {
		t.Fatalf("event attempts=%#v", recorder.writes)
	}
	failed, newer := recorder.writes[0].Events[0], recorder.writes[1].Events[0]
	if failed.ID == newer.ID || failed.OccurredAt != base.Add(time.Minute) || newer.OccurredAt != base.Add(2*time.Minute) || !strings.Contains(newer.PayloadJSON, `"old_version":"v1"`) {
		t.Fatalf("failed=%+v newer=%+v", failed, newer)
	}
}

func TestServerSettingsCanonicalizationIsStableAndDoesNotMutateInput(t *testing.T) {
	recorder := &serverRecorderFake{}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	first := map[string]any{
		"z": []any{json.Number("1.0"), map[string]any{"b": json.Number("2"), "a": float64(1)}},
		"a": map[string]any{"n": int64(3)},
	}
	second := map[string]any{
		"a": map[string]any{"n": float64(3)},
		"z": []any{float64(1), map[string]any{"a": json.Number("1.00"), "b": float64(2)}},
	}
	before := first["z"].([]any)[0]
	if err := service.RecordSettings(t.Context(), base, domain.ServerSettings{Values: first}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordSettings(t.Context(), base.Add(time.Minute), domain.ServerSettings{Values: second}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.documentCalls) != 2 {
		t.Fatalf("calls=%d", len(recorder.documentCalls))
	}
	if !reflect.DeepEqual(recorder.documentCalls[0].canonical, recorder.documentCalls[1].canonical) || recorder.documentCalls[0].hash != recorder.documentCalls[1].hash {
		t.Fatalf("first=%s/%s second=%s/%s", recorder.documentCalls[0].canonical, recorder.documentCalls[0].hash, recorder.documentCalls[1].canonical, recorder.documentCalls[1].hash)
	}
	if first["z"].([]any)[0] != before {
		t.Fatalf("input mutated: %#v", first)
	}
	if len(recorder.writes) != 0 {
		t.Fatalf("unchanged settings emitted event: %#v", recorder.writes)
	}
}

func TestServerSettingsCanonicalizesEquivalentExponentsAndNegativeZero(t *testing.T) {
	recorder := &serverRecorderFake{}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	first := domain.ServerSettings{Values: map[string]any{
		"integer": json.Number("1e3"), "decimal": json.Number("1.25e1"), "zero": json.Number("-0.000e9"),
	}}
	second := domain.ServerSettings{Values: map[string]any{
		"zero": float64(0), "decimal": float64(12.5), "integer": int64(1000),
	}}
	if err := service.RecordSettings(t.Context(), base, first); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordSettings(t.Context(), base.Add(time.Minute), second); err != nil {
		t.Fatal(err)
	}
	if len(recorder.documentCalls) != 2 || string(recorder.documentCalls[0].canonical) != string(recorder.documentCalls[1].canonical) || recorder.documentCalls[0].hash != recorder.documentCalls[1].hash {
		t.Fatalf("calls=%#v", recorder.documentCalls)
	}
	if got := string(recorder.documentCalls[0].canonical); got != `{"decimal":12.5,"integer":1000,"zero":0}` {
		t.Fatalf("canonical=%s", got)
	}
}

func TestServerSettingsRejectsPathologicalNumberLexemes(t *testing.T) {
	for name, number := range map[string]json.Number{
		"too many digits": json.Number(strings.Repeat("9", 257)),
		"huge exponent":   json.Number("1e1025"),
		"tiny exponent":   json.Number("1e-1025"),
		"long lexeme":     json.Number("0." + strings.Repeat("0", 600) + "1"),
	} {
		t.Run(name, func(t *testing.T) {
			recorder := &serverRecorderFake{}
			err := newServerService(recorder).RecordSettings(t.Context(), time.Now(), domain.ServerSettings{Values: map[string]any{"value": number}})
			if err == nil {
				t.Fatal("expected bounded numeric validation error")
			}
			if len(recorder.documentCalls) != 0 {
				t.Fatalf("invalid number persisted: %#v", recorder.documentCalls)
			}
		})
	}
}

func TestServerSettingsKeepsTinyAndLargeFractionalChangesDistinct(t *testing.T) {
	recorder := &serverRecorderFake{}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	for index, number := range []json.Number{"0", "1e-400", "9007199254740992.1", "9007199254740992.2"} {
		if err := service.RecordSettings(t.Context(), base.Add(time.Duration(index)*time.Minute), domain.ServerSettings{Values: map[string]any{"value": number}}); err != nil {
			t.Fatalf("%s: %v", number, err)
		}
	}
	if len(recorder.documentCalls) != 4 {
		t.Fatalf("calls=%#v", recorder.documentCalls)
	}
	for index := 1; index < len(recorder.documentCalls); index++ {
		if recorder.documentCalls[index-1].hash == recorder.documentCalls[index].hash {
			t.Fatalf("numbers collapsed at %d: %#v", index, recorder.documentCalls)
		}
	}
}

func TestServerSettingsKeepsDistinctLargeIntegerHashes(t *testing.T) {
	recorder := &serverRecorderFake{}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	if err := service.RecordSettings(t.Context(), base, domain.ServerSettings{Values: map[string]any{"value": json.Number("9007199254740992")}}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordSettings(t.Context(), base.Add(time.Minute), domain.ServerSettings{Values: map[string]any{"value": json.Number("9007199254740993")}}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.documentCalls) != 2 || recorder.documentCalls[0].hash == recorder.documentCalls[1].hash || string(recorder.documentCalls[0].canonical) == string(recorder.documentCalls[1].canonical) {
		t.Fatalf("large integer collision: %#v", recorder.documentCalls)
	}
}

func TestServerSettingsChangeEventContainsOnlyHashesAndSafeSummary(t *testing.T) {
	eventErr := errors.New("event failed")
	recorder := &serverRecorderFake{eventErrors: []error{eventErr, nil}}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	if err := service.RecordSettings(t.Context(), base, domain.ServerSettings{Values: map[string]any{"password": "secret-one"}}); err != nil {
		t.Fatal(err)
	}
	changed := domain.ServerSettings{Values: map[string]any{"password": "secret-two", "Nested": map[string]any{"token": "private"}}}
	if err := service.RecordSettings(t.Context(), base.Add(time.Minute), changed); !errors.Is(err, eventErr) {
		t.Fatalf("err=%v", err)
	}
	if err := service.RecordSettings(t.Context(), base.Add(time.Minute), changed); err != nil {
		t.Fatal(err)
	}
	if len(recorder.documentCalls) != 3 || len(recorder.writes) != 2 {
		t.Fatalf("docs=%d writes=%d", len(recorder.documentCalls), len(recorder.writes))
	}
	for _, write := range recorder.writes {
		event := write.Events[0]
		if event.EventType != "server_settings_changed" || strings.Contains(event.PayloadJSON, "secret") || strings.Contains(event.PayloadJSON, "password") || strings.Contains(event.PayloadJSON, "token") {
			t.Fatalf("unsafe event payload=%s", event.PayloadJSON)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["old_hash"] == "" || payload["new_hash"] == "" || payload["summary"] != "server settings changed" {
			t.Fatalf("payload=%v", payload)
		}
	}
}

func TestServerSettingsFailedAtomicChangeDoesNotBlockNewerDocument(t *testing.T) {
	eventErr := errors.New("event failed")
	recorder := &serverRecorderFake{eventErrors: []error{eventErr, nil, nil}}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)

	settings := func(value string) domain.ServerSettings {
		return domain.ServerSettings{Values: map[string]any{"value": value}}
	}
	if err := service.RecordSettings(t.Context(), base, settings("one")); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordSettings(t.Context(), base.Add(time.Minute), settings("two")); !errors.Is(err, eventErr) {
		t.Fatalf("change err=%v", err)
	}
	if err := service.RecordSettings(t.Context(), base.Add(2*time.Minute), settings("three")); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordSettings(t.Context(), base.Add(3*time.Minute), settings("three")); err != nil {
		t.Fatal(err)
	}
	if len(recorder.documentCalls) != 4 {
		t.Fatalf("document calls=%#v", recorder.documentCalls)
	}
	if len(recorder.writes) != 2 {
		t.Fatalf("event attempts=%#v", recorder.writes)
	}
	failed, newer := recorder.writes[0].Events[0], recorder.writes[1].Events[0]
	if failed.ID == newer.ID || failed.OccurredAt != base.Add(time.Minute) || newer.OccurredAt != base.Add(2*time.Minute) {
		t.Fatalf("failed=%+v newer=%+v", failed, newer)
	}
}

func TestServerSettingsRejectsNonFiniteValuesWithoutPersistence(t *testing.T) {
	for name, value := range map[string]any{"nan": math.NaN(), "positive infinity": math.Inf(1), "negative infinity": math.Inf(-1)} {
		t.Run(name, func(t *testing.T) {
			recorder := &serverRecorderFake{}
			service := newServerService(recorder)
			err := service.RecordSettings(t.Context(), time.Now(), domain.ServerSettings{Values: map[string]any{"nested": []any{value}}})
			if err == nil {
				t.Fatal("expected error")
			}
			if len(recorder.documentCalls) != 0 {
				t.Fatal("invalid settings persisted")
			}
		})
	}
}

func TestServerDocumentProcessBaselineRejectsOlderUnchangedSample(t *testing.T) {
	recorder := &serverRecorderFake{}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	settings := domain.ServerSettings{Values: map[string]any{"value": "A"}}
	if err := service.RecordSettings(t.Context(), base, settings); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordSettings(t.Context(), base.Add(2*time.Minute), settings); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordSettings(t.Context(), base.Add(time.Minute), settings); err == nil {
		t.Fatal("expected sample older than process baseline to fail")
	}
	if len(recorder.documentCalls) != 2 {
		t.Fatalf("older sample reached repository: %#v", recorder.documentCalls)
	}
}

func TestServerInfoAndSettingsEmitRecurrentTransitionsWithoutSecrets(t *testing.T) {
	recorder := &serverRecorderFake{}
	service := newServerService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	infoA := domain.ServerInfo{Version: "A", Description: "secret-a"}
	infoB := domain.ServerInfo{Version: "B", Description: "secret-b"}
	if err := service.RecordInfo(t.Context(), base, infoA); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordInfo(t.Context(), base.Add(time.Minute), infoB); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordInfo(t.Context(), base.Add(2*time.Minute), infoA); err != nil {
		t.Fatal(err)
	}
	settingsA := domain.ServerSettings{Values: map[string]any{"password": "secret-a"}}
	settingsB := domain.ServerSettings{Values: map[string]any{"password": "secret-b"}}
	if err := service.RecordSettings(t.Context(), base, settingsA); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordSettings(t.Context(), base.Add(time.Minute), settingsB); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordSettings(t.Context(), base.Add(2*time.Minute), settingsA); err != nil {
		t.Fatal(err)
	}
	if len(recorder.writes) != 4 {
		t.Fatalf("event writes=%#v", recorder.writes)
	}
	for index, write := range recorder.writes {
		event := write.Events[0]
		wantType := "server_version_changed"
		if index >= 2 {
			wantType = "server_settings_changed"
		}
		if event.EventType != wantType || strings.Contains(event.PayloadJSON, "secret") || strings.Contains(event.PayloadJSON, "password") || strings.Contains(event.PayloadJSON, "description") {
			t.Fatalf("unsafe recurrent event=%+v", event)
		}
	}
	if recorder.latestDocuments["info"].Hash != recorder.documentCalls[0].hash || recorder.latestDocuments["settings"].Hash != recorder.documentCalls[3].hash {
		t.Fatalf("latest documents=%#v calls=%#v", recorder.latestDocuments, recorder.documentCalls)
	}
}

func TestServerRecordersSerializeConcurrentCallsAndRejectOlderTimestamps(t *testing.T) {
	recorder := &serverRecorderFake{metricEntered: make(chan struct{}, 1), metricRelease: make(chan struct{})}
	service := newServerService(recorder)
	newer := time.Date(2026, 7, 13, 0, 1, 0, 0, time.UTC)
	newerDone := make(chan error, 1)
	go func() {
		newerDone <- service.RecordMetrics(t.Context(), newer, domain.ServerMetrics{UptimeSeconds: 10, ServerFrameTime: 1})
	}()
	<-recorder.metricEntered
	olderDone := make(chan error, 1)
	go func() {
		olderDone <- service.RecordMetrics(t.Context(), newer.Add(-time.Minute), domain.ServerMetrics{UptimeSeconds: 20, ServerFrameTime: 1})
	}()
	close(recorder.metricRelease)
	if err := <-newerDone; err != nil {
		t.Fatal(err)
	}
	if err := <-olderDone; err == nil {
		t.Fatal("expected older concurrent call to fail")
	}
	if len(recorder.metricsCalls) != 1 || recorder.metricsCalls[0].at != newer {
		t.Fatalf("calls=%#v", recorder.metricsCalls)
	}
}

func TestServerServiceAcceptsExactRetryAfterAmbiguousAtomicMetricCommit(t *testing.T) {
	repo, _ := openServerRepository(t)
	defer repo.Close()
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	service := observation.NewServer(repo, observation.NewID)
	if err := service.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordMetrics(t.Context(), base, domain.ServerMetrics{UptimeSeconds: 100, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	spy := &atomicRepositorySpy{delegate: repo, metricAmbiguous: true}
	service = observation.NewServer(spy, func() string { return "stable-id" })
	if err := service.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	restart := domain.ServerMetrics{UptimeSeconds: 1, ServerFrameTime: 1}
	if err := service.RecordMetrics(t.Context(), base.Add(time.Minute), restart); err == nil {
		t.Fatal("expected simulated ambiguous commit error")
	}
	if err := service.RecordMetrics(t.Context(), base.Add(time.Minute), restart); err != nil {
		t.Fatalf("exact retry after ambiguous commit: %v", err)
	}
	if len(spy.metrics) != 2 {
		t.Fatalf("atomic writes=%#v", spy.metrics)
	}
	firstMetric, replayedMetric := spy.metrics[0], spy.metrics[1]
	firstMetric.Expected, replayedMetric.Expected = nil, nil
	if spy.metrics[0].Event == nil || spy.metrics[0].Event.EventType != "server_restarted" || !reflect.DeepEqual(firstMetric, replayedMetric) {
		t.Fatalf("atomic writes=%#v", spy.metrics)
	}
	if err := repo.RecordServerMetricObservation(t.Context(), spy.metrics[0]); err != nil {
		t.Fatalf("repository could not prove exact committed replay: %v", err)
	}
}

func TestServerServiceAcceptsNewerCallAfterAmbiguousDocumentCommits(t *testing.T) {
	repo, _ := openServerRepository(t)
	defer repo.Close()
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	service := observation.NewServer(repo, observation.NewID)
	if err := service.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordInfo(t.Context(), base, domain.ServerInfo{Version: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordSettings(t.Context(), base, domain.ServerSettings{Values: map[string]any{"value": "A"}}); err != nil {
		t.Fatal(err)
	}
	spy := &atomicRepositorySpy{delegate: repo, documentAmbiguous: map[string]bool{"info": true, "settings": true}}
	service = observation.NewServer(spy, observation.NewID)
	if err := service.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordInfo(t.Context(), base.Add(time.Minute), domain.ServerInfo{Version: "v2"}); err == nil {
		t.Fatal("expected ambiguous info commit")
	}
	if err := service.RecordInfo(t.Context(), base.Add(time.Minute), domain.ServerInfo{Version: "v2"}); err != nil {
		t.Fatalf("exact info retry after ambiguous commit: %v", err)
	}
	if err := service.RecordInfo(t.Context(), base.Add(2*time.Minute), domain.ServerInfo{Version: "v3"}); err != nil {
		t.Fatalf("newer info after ambiguous commit: %v", err)
	}
	if err := service.RecordSettings(t.Context(), base.Add(time.Minute), domain.ServerSettings{Values: map[string]any{"value": "B"}}); err == nil {
		t.Fatal("expected ambiguous settings commit")
	}
	if err := service.RecordSettings(t.Context(), base.Add(time.Minute), domain.ServerSettings{Values: map[string]any{"value": "B"}}); err != nil {
		t.Fatalf("exact settings retry after ambiguous commit: %v", err)
	}
	if err := service.RecordSettings(t.Context(), base.Add(2*time.Minute), domain.ServerSettings{Values: map[string]any{"value": "C"}}); err != nil {
		t.Fatalf("newer settings after ambiguous commit: %v", err)
	}
	if len(spy.documents) != 6 {
		t.Fatalf("document writes=%#v", spy.documents)
	}
	firstInfo, replayedInfo := spy.documents[0], spy.documents[1]
	firstInfo.Expected, replayedInfo.Expected = nil, nil
	firstSettings, replayedSettings := spy.documents[3], spy.documents[4]
	firstSettings.Expected, replayedSettings.Expected = nil, nil
	if !reflect.DeepEqual(firstInfo, replayedInfo) || !reflect.DeepEqual(firstSettings, replayedSettings) {
		t.Fatalf("document writes=%#v", spy.documents)
	}
	for _, write := range spy.documents {
		if write.Event == nil {
			t.Fatalf("transition missing atomic event: %+v", write)
		}
	}
}

func TestServerMetricCASRetryRederivesRestartAgainstWinningWriter(t *testing.T) {
	repo, _ := openServerRepository(t)
	defer repo.Close()
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	seed := observation.NewServer(repo, func() string { return "seed" })
	if err := seed.RecordMetrics(t.Context(), base, domain.ServerMetrics{UptimeSeconds: 100, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	winner := observation.NewServer(repo, func() string { return "winner-restart" })
	interleaved := &interleavingRepository{delegate: repo}
	interleaved.hook = func() {
		if err := winner.RecordMetrics(t.Context(), base.Add(time.Minute), domain.ServerMetrics{UptimeSeconds: 80, ServerFrameTime: 1}); err != nil {
			t.Errorf("winning writer: %v", err)
		}
	}
	loser := observation.NewServer(interleaved, func() string { return "retried-restart" })
	if err := loser.RecordMetrics(t.Context(), base.Add(2*time.Minute), domain.ServerMetrics{UptimeSeconds: 50, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if len(interleaved.metrics) != 2 || interleaved.metrics[0].Expected == nil || interleaved.metrics[1].Expected == nil {
		t.Fatalf("writes=%#v", interleaved.metrics)
	}
	if interleaved.metrics[1].Event == nil || !strings.Contains(interleaved.metrics[1].Event.PayloadJSON, `"old_uptime_seconds":80`) {
		t.Fatalf("retried event=%#v", interleaved.metrics[1].Event)
	}
}

func TestServerDocumentCASRetryRederivesChangeAgainstWinningWriter(t *testing.T) {
	repo, _ := openServerRepository(t)
	defer repo.Close()
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	seed := observation.NewServer(repo, func() string { return "seed" })
	if err := seed.RecordSettings(t.Context(), base, domain.ServerSettings{Values: map[string]any{"value": "A"}}); err != nil {
		t.Fatal(err)
	}
	winner := observation.NewServer(repo, func() string { return "winner-settings" })
	interleaved := &interleavingRepository{delegate: repo}
	interleaved.hook = func() {
		if err := winner.RecordSettings(t.Context(), base.Add(time.Minute), domain.ServerSettings{Values: map[string]any{"value": "B"}}); err != nil {
			t.Errorf("winning writer: %v", err)
		}
	}
	loser := observation.NewServer(interleaved, func() string { return "retried-settings" })
	if err := loser.RecordSettings(t.Context(), base.Add(2*time.Minute), domain.ServerSettings{Values: map[string]any{"value": "C"}}); err != nil {
		t.Fatal(err)
	}
	if len(interleaved.documents) != 2 || interleaved.documents[0].Expected == nil || interleaved.documents[1].Expected == nil {
		t.Fatalf("writes=%#v", interleaved.documents)
	}
	winnerSnapshot, err := repo.LatestServerDocument(t.Context(), "settings")
	if err != nil {
		t.Fatal(err)
	}
	if interleaved.documents[1].Event == nil || !strings.Contains(interleaved.documents[1].Event.PayloadJSON, `"old_hash":"`+interleaved.documents[1].Expected.Hash+`"`) {
		t.Fatalf("retried event=%#v expected=%#v latest=%#v", interleaved.documents[1].Event, interleaved.documents[1].Expected, winnerSnapshot)
	}
}

func TestServerCASRetriesAreBounded(t *testing.T) {
	t.Run("metrics", func(t *testing.T) {
		recorder := &serverRecorderFake{metricErrors: []error{store.ErrObservationConflict, store.ErrObservationConflict, store.ErrObservationConflict, nil}}
		err := newServerService(recorder).RecordMetrics(t.Context(), time.Now(), domain.ServerMetrics{ServerFrameTime: 1})
		if !errors.Is(err, store.ErrObservationConflict) || len(recorder.metricsCalls) != 3 {
			t.Fatalf("err=%v calls=%d", err, len(recorder.metricsCalls))
		}
	})
	t.Run("documents", func(t *testing.T) {
		recorder := &serverRecorderFake{documentErrors: []error{store.ErrObservationConflict, store.ErrObservationConflict, store.ErrObservationConflict, nil}}
		err := newServerService(recorder).RecordSettings(t.Context(), time.Now(), domain.ServerSettings{Values: map[string]any{"value": "A"}})
		if !errors.Is(err, store.ErrObservationConflict) || len(recorder.documentCalls) != 3 {
			t.Fatalf("err=%v calls=%d", err, len(recorder.documentCalls))
		}
	})
}

func TestServerRestartDetectionSurvivesRawRetentionAndReopen(t *testing.T) {
	repo, path := openServerRepository(t)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	service := observation.NewServer(repo, observation.NewID)
	if err := service.RecordMetrics(t.Context(), old, domain.ServerMetrics{UptimeSeconds: 100, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := repo.CleanupRawObservations(t.Context(), old.Add(100*24*time.Hour), 100); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	service = observation.NewServer(reopened, func() string { return "restart-after-retention" })
	if err := service.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordMetrics(t.Context(), old.Add(101*24*time.Hour), domain.ServerMetrics{UptimeSeconds: 1, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	latest, err := reopened.LatestServerMetricObservation(t.Context())
	if err != nil || latest.Event == nil || latest.Event.EventType != "server_restarted" || !strings.Contains(latest.Event.PayloadJSON, `"old_uptime_seconds":100`) {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
}

func TestServerServiceRestoresAllBaselinesAcrossReopen(t *testing.T) {
	repo, path := openServerRepository(t)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	first := observation.NewServer(repo, observation.NewID)
	if err := first.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := first.RecordMetrics(t.Context(), base, domain.ServerMetrics{UptimeSeconds: 100, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if err := first.RecordInfo(t.Context(), base, domain.ServerInfo{Version: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := first.RecordSettings(t.Context(), base, domain.ServerSettings{Values: map[string]any{"value": "A"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	spy := &atomicRepositorySpy{delegate: reopened}
	second := observation.NewServer(spy, observation.NewID)
	if err := second.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := second.RecordMetrics(t.Context(), base.Add(time.Minute), domain.ServerMetrics{UptimeSeconds: 1, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if err := second.RecordInfo(t.Context(), base.Add(time.Minute), domain.ServerInfo{Version: "v2"}); err != nil {
		t.Fatal(err)
	}
	if err := second.RecordSettings(t.Context(), base.Add(time.Minute), domain.ServerSettings{Values: map[string]any{"value": "B"}}); err != nil {
		t.Fatal(err)
	}
	if len(spy.metrics) != 1 || spy.metrics[0].Event == nil || spy.metrics[0].Event.EventType != "server_restarted" {
		t.Fatalf("metric writes=%#v", spy.metrics)
	}
	wantTypes := map[string]string{"info": "server_version_changed", "settings": "server_settings_changed"}
	for _, write := range spy.documents {
		if write.Event == nil || write.Event.EventType != wantTypes[write.Kind] {
			t.Fatalf("document write=%+v", write)
		}
	}
}
