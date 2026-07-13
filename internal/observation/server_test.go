package observation_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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

	metricErrors   []error
	documentErrors []error
	eventErrors    []error
	inserted       []bool
	metricEntered  chan struct{}
	metricRelease  chan struct{}
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
	if len(recorder.metricsCalls) != 3 || len(recorder.writes) != 2 {
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
	if len(recorder.metricsCalls) != 3 {
		t.Fatal("older sample reached repository")
	}
}

func TestServerMetricsRetryPendingRestartBeforeProcessingNewerSample(t *testing.T) {
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
	if failed.ID != retried.ID || failed.CorrelationID != retried.CorrelationID || failed.OccurredAt != base.Add(time.Minute) || retried.OccurredAt != base.Add(time.Minute) {
		t.Fatalf("failed=%+v retried=%+v", failed, retried)
	}
}

func TestServerMetricsKeepPendingRestartWhenRepeatedRetryFails(t *testing.T) {
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
	if len(recorder.metricsCalls) != 2 {
		t.Fatalf("new sample persisted while pending retry failed: %#v", recorder.metricsCalls)
	}
	if err := service.RecordMetrics(t.Context(), base.Add(3*time.Minute), domain.ServerMetrics{UptimeSeconds: 3, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.metricsCalls) != 3 || recorder.metricsCalls[2].at != base.Add(3*time.Minute) {
		t.Fatalf("metric calls=%#v", recorder.metricsCalls)
	}
	for index := 1; index < len(recorder.writes); index++ {
		if recorder.writes[index].Events[0].ID != recorder.writes[0].Events[0].ID {
			t.Fatalf("pending event ID changed across retries: %#v", recorder.writes)
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
	if len(recorder.documentCalls) != 2 {
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

func TestServerInfoRetriesPendingVersionEventBeforeProcessingNewerDocument(t *testing.T) {
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
	if len(recorder.writes) != 3 {
		t.Fatalf("event attempts=%#v", recorder.writes)
	}
	failed, retried, newer := recorder.writes[0].Events[0], recorder.writes[1].Events[0], recorder.writes[2].Events[0]
	if failed.ID != retried.ID || failed.OccurredAt != base.Add(time.Minute) || retried.OccurredAt != base.Add(time.Minute) || newer.OccurredAt != base.Add(2*time.Minute) {
		t.Fatalf("failed=%+v retried=%+v newer=%+v", failed, retried, newer)
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
	if len(recorder.documentCalls) != 2 || len(recorder.writes) != 2 {
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

func TestServerSettingsRetriesPendingEventBeforeProcessingNewerDocument(t *testing.T) {
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
	if len(recorder.writes) != 3 {
		t.Fatalf("event attempts=%#v", recorder.writes)
	}
	failed, retried, newer := recorder.writes[0].Events[0], recorder.writes[1].Events[0], recorder.writes[2].Events[0]
	if failed.ID != retried.ID || failed.OccurredAt != base.Add(time.Minute) || retried.OccurredAt != base.Add(time.Minute) || newer.OccurredAt != base.Add(2*time.Minute) {
		t.Fatalf("failed=%+v retried=%+v newer=%+v", failed, retried, newer)
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
