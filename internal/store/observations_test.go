package store

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

func observationEvent(id, eventType, userID string, occurredAt time.Time) ActivityEvent {
	return ActivityEvent{
		ID: id, EventType: eventType, SubjectType: "player", SubjectID: userID,
		OccurredAt: occurredAt, ObservedAt: occurredAt, Source: "palworld_rest",
		SourceRef: "poll-1", CorrelationID: "poll-1", Confidence: "observed",
		SchemaVersion: 1, PayloadJSON: `{}`,
	}
}

func observationTrajectory(userID string, observedAt time.Time) TrajectorySample {
	return TrajectorySample{
		UserID: userID, SegmentID: "segment-1", ObservedAt: observedAt,
		X: 123.25, Y: -99.5, Ping: 28.5, Level: 41, SourceRef: "poll-1",
	}
}

func serverObservationEvent(id, eventType string, occurredAt time.Time, payload string) ActivityEvent {
	return ActivityEvent{
		ID: id, EventType: eventType, SubjectType: "server", SubjectID: "server",
		OccurredAt: occurredAt, ObservedAt: occurredAt, Source: "palworld_rest",
		SourceRef: id + "-correlation", CorrelationID: id + "-correlation", Confidence: "observed",
		SchemaVersion: 1, PayloadJSON: payload,
	}
}

func serverMetrics(uptime int64) domain.ServerMetrics {
	return domain.ServerMetrics{
		ServerFPS: 60, CurrentPlayerNum: 2, ServerFrameTime: 16.5,
		MaxPlayerNum: 32, UptimeSeconds: uptime, BaseCampNum: 4, Days: 8,
	}
}

func TestServerMetricObservationIsAtomicAndIdempotent(t *testing.T) {
	repo, _ := openTemp(t)
	at := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	event := serverObservationEvent("restart-1", "server_restarted", at, `{"old_uptime_seconds":100,"new_uptime_seconds":1}`)
	write := ServerMetricObservation{At: at, Metrics: serverMetrics(1), Event: &event}
	if err := repo.RecordServerMetricObservation(t.Context(), write); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordServerMetricObservation(t.Context(), write); err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	for table, want := range map[string]int{"server_metric_samples": 1, "activity_events": 1} {
		var count int
		if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s count=%d want=%d", table, count, want)
		}
	}

	mismatchedMetric := write
	mismatchedMetric.Metrics.ServerFPS = 59
	if err := repo.RecordServerMetricObservation(t.Context(), mismatchedMetric); err == nil {
		t.Fatal("expected same-time metric mismatch to fail")
	}
	mismatchedEvent := event
	mismatchedEvent.PayloadJSON = `{"old_uptime_seconds":99,"new_uptime_seconds":1}`
	if err := repo.RecordServerMetricObservation(t.Context(), ServerMetricObservation{At: at, Metrics: write.Metrics, Event: &mismatchedEvent}); err == nil {
		t.Fatal("expected same-time event mismatch to fail")
	}
	if err := repo.RecordServerMetricObservation(t.Context(), ServerMetricObservation{At: at, Metrics: write.Metrics}); err == nil {
		t.Fatal("expected missing replay event to fail")
	}
	if err := repo.RecordServerMetricObservation(t.Context(), ServerMetricObservation{At: at.Add(-time.Second), Metrics: write.Metrics}); err == nil {
		t.Fatal("expected stale metric observation to fail")
	}
}

func TestServerMetricObservationRollsBackEventAndMetricTogether(t *testing.T) {
	repo, _ := openTemp(t)
	at := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	conflict := serverObservationEvent("same-id", "other", at, `{}`)
	if err := repo.RecordPlayerObservation(t.Context(), PlayerObservationWrite{Events: []ActivityEvent{conflict}}); err != nil {
		t.Fatal(err)
	}
	event := serverObservationEvent("same-id", "server_restarted", at, `{}`)
	if err := repo.RecordServerMetricObservation(t.Context(), ServerMetricObservation{At: at, Metrics: serverMetrics(1), Event: &event}); err == nil {
		t.Fatal("expected event ID conflict")
	}
	var metrics int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM server_metric_samples`).Scan(&metrics); err != nil {
		t.Fatal(err)
	}
	if metrics != 0 {
		t.Fatalf("metric committed without event: count=%d", metrics)
	}
}

func TestServerMetricObservationValidation(t *testing.T) {
	valid := serverMetrics(1)
	tests := map[string]func(*domain.ServerMetrics){
		"negative fps":         func(m *domain.ServerMetrics) { m.ServerFPS = -1 },
		"negative players":     func(m *domain.ServerMetrics) { m.CurrentPlayerNum = -1 },
		"negative frame time":  func(m *domain.ServerMetrics) { m.ServerFrameTime = -1 },
		"nan frame time":       func(m *domain.ServerMetrics) { m.ServerFrameTime = math.NaN() },
		"infinite frame time":  func(m *domain.ServerMetrics) { m.ServerFrameTime = math.Inf(1) },
		"negative max players": func(m *domain.ServerMetrics) { m.MaxPlayerNum = -1 },
		"negative uptime":      func(m *domain.ServerMetrics) { m.UptimeSeconds = -1 },
		"negative base camps":  func(m *domain.ServerMetrics) { m.BaseCampNum = -1 },
		"negative days":        func(m *domain.ServerMetrics) { m.Days = -1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			repo, _ := openTemp(t)
			metrics := valid
			mutate(&metrics)
			if err := repo.RecordServerMetricObservation(t.Context(), ServerMetricObservation{At: time.Now(), Metrics: metrics}); err == nil {
				t.Fatal("expected validation error")
			}
			if _, _, err := repo.LatestServerMetrics(t.Context()); !errors.Is(err, ErrNotFound) {
				t.Fatalf("invalid metric changed durable baseline: %v", err)
			}
		})
	}
}

func TestLatestServerMetricsSurvivesReopen(t *testing.T) {
	repo, path := openTemp(t)
	at := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	metrics := serverMetrics(42)
	event := serverObservationEvent("restart-reopen", "server_restarted", at, `{"old_uptime_seconds":100,"new_uptime_seconds":42}`)
	if err := repo.RecordServerMetricObservation(t.Context(), ServerMetricObservation{At: at, Metrics: metrics, Event: &event}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	gotAt, got, err := reopened.LatestServerMetrics(t.Context())
	if err != nil || gotAt != at || got != metrics {
		t.Fatalf("at=%s metrics=%+v err=%v", gotAt, got, err)
	}
	snapshot, err := reopened.LatestServerMetricObservation(t.Context())
	if err != nil || snapshot.At != at || snapshot.Metrics != metrics || snapshot.Event == nil || *snapshot.Event != event {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
}

func TestServerDocumentObservationRecordsRecurrentTransitions(t *testing.T) {
	repo, _ := openTemp(t)
	base := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	a := ServerDocumentObservation{Kind: "settings", At: base, Canonical: []byte(`{"value":"A"}`), Hash: "hash-a"}
	changed, err := repo.RecordServerDocumentObservation(t.Context(), a)
	if err != nil || !changed {
		t.Fatalf("first changed=%v err=%v", changed, err)
	}
	bEvent := serverObservationEvent("settings-b", "server_settings_changed", base.Add(time.Minute), `{"old_hash":"hash-a","new_hash":"hash-b","summary":"server settings changed"}`)
	b := ServerDocumentObservation{Kind: "settings", At: base.Add(time.Minute), Canonical: []byte(`{"value":"B"}`), Hash: "hash-b", Event: &bEvent}
	changed, err = repo.RecordServerDocumentObservation(t.Context(), b)
	if err != nil || !changed {
		t.Fatalf("B changed=%v err=%v", changed, err)
	}
	aEvent := serverObservationEvent("settings-a", "server_settings_changed", base.Add(2*time.Minute), `{"old_hash":"hash-b","new_hash":"hash-a","summary":"server settings changed"}`)
	aAgain := ServerDocumentObservation{Kind: "settings", At: base.Add(2 * time.Minute), Canonical: []byte(`{"value":"A"}`), Hash: "hash-a", Event: &aEvent}
	changed, err = repo.RecordServerDocumentObservation(t.Context(), aAgain)
	if err != nil || !changed {
		t.Fatalf("A again changed=%v err=%v", changed, err)
	}
	if changed, err = repo.RecordServerDocumentObservation(t.Context(), aAgain); err != nil || !changed {
		t.Fatalf("exact replay changed=%v err=%v", changed, err)
	}
	unchanged := aAgain
	unchanged.At = base.Add(3 * time.Minute)
	unchanged.Event = nil
	if changed, err = repo.RecordServerDocumentObservation(t.Context(), unchanged); err != nil || changed {
		t.Fatalf("unchanged changed=%v err=%v", changed, err)
	}
	for table, want := range map[string]int{"server_documents": 2, "server_document_observations": 3, "activity_events": 2} {
		var count int
		if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s count=%d want=%d", table, count, want)
		}
	}
	latest, err := repo.LatestServerDocument(t.Context(), "settings")
	if err != nil || latest.At != aAgain.At || latest.Hash != "hash-a" || string(latest.Canonical) != `{"value":"A"}` || latest.Event == nil || *latest.Event != aEvent {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
}

func TestServerDocumentObservationRejectsReplayMismatchAndStale(t *testing.T) {
	repo, _ := openTemp(t)
	at := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	event := serverObservationEvent("info-change", "server_version_changed", at, `{}`)
	write := ServerDocumentObservation{Kind: "info", At: at, Canonical: []byte(`{"version":"B"}`), Hash: "hash-b", Event: &event}
	if _, err := repo.RecordServerDocumentObservation(t.Context(), write); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*ServerDocumentObservation){
		"hash":          func(w *ServerDocumentObservation) { w.Hash = "other" },
		"canonical":     func(w *ServerDocumentObservation) { w.Canonical = []byte(`{"version":"other"}`) },
		"event":         func(w *ServerDocumentObservation) { w.Event.PayloadJSON = `{"changed":true}` },
		"missing event": func(w *ServerDocumentObservation) { w.Event = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			mismatch := write
			eventCopy := event
			mismatch.Event = &eventCopy
			mutate(&mismatch)
			if _, err := repo.RecordServerDocumentObservation(t.Context(), mismatch); err == nil {
				t.Fatal("expected replay mismatch")
			}
		})
	}
	stale := write
	stale.At = at.Add(-time.Second)
	stale.Event = nil
	if _, err := repo.RecordServerDocumentObservation(t.Context(), stale); err == nil {
		t.Fatal("expected stale observation error")
	}
}

func TestLatestServerDocumentSurvivesReopen(t *testing.T) {
	repo, path := openTemp(t)
	at := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	event := serverObservationEvent("info-reopen", "server_version_changed", at, `{"old_version":"v0","new_version":"v1"}`)
	write := ServerDocumentObservation{Kind: "info", At: at, Canonical: []byte(`{"version":"v1"}`), Hash: "hash-v1", Event: &event}
	if _, err := repo.RecordServerDocumentObservation(t.Context(), write); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	latest, err := reopened.LatestServerDocument(t.Context(), "info")
	if err != nil || latest.At != at || latest.Hash != write.Hash || string(latest.Canonical) != string(write.Canonical) || latest.Event == nil || *latest.Event != event {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
}

func TestObservationCleanupPreservesEventsLinkedToDurableServerObservations(t *testing.T) {
	repo, _ := openTemp(t)
	at := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	metricEvent := serverObservationEvent("metric-linked", "server_restarted", at, `{}`)
	if err := repo.RecordServerMetricObservation(t.Context(), ServerMetricObservation{
		At: at, Metrics: serverMetrics(1), Event: &metricEvent,
	}); err != nil {
		t.Fatal(err)
	}
	documentEvent := serverObservationEvent("document-linked", "server_settings_changed", at, `{}`)
	if _, err := repo.RecordServerDocumentObservation(t.Context(), ServerDocumentObservation{
		Kind: "settings", At: at, Canonical: []byte(`{"value":"B"}`), Hash: "hash-b", Event: &documentEvent,
	}); err != nil {
		t.Fatal(err)
	}
	ordinary := observationEvent("ordinary-old", "joined", "u", at)
	if err := repo.RecordPlayerObservation(t.Context(), PlayerObservationWrite{Events: []ActivityEvent{ordinary}}); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.CleanupRawObservations(t.Context(), at.Add(time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{metricEvent.ID, documentEvent.ID} {
		var count int
		if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM activity_events WHERE id=?`, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("linked event %q count=%d", id, count)
		}
	}
	var ordinaryCount int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM activity_events WHERE id=?`, ordinary.ID).Scan(&ordinaryCount); err != nil {
		t.Fatal(err)
	}
	if ordinaryCount != 0 {
		t.Fatalf("ordinary retained event count=%d", ordinaryCount)
	}
	latest, err := repo.LatestServerDocument(t.Context(), "settings")
	if err != nil || latest.Event == nil || *latest.Event != documentEvent {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
}

func TestObservationRecordIsAtomic(t *testing.T) {
	repo, _ := openTemp(t)
	now := time.Date(2026, 7, 13, 8, 0, 0, 123, time.UTC)
	write := PlayerObservationWrite{
		Events: []ActivityEvent{
			observationEvent("evt-1", "player_joined", "steam_1", now),
			observationEvent("evt-2", "player_attribute_changed", "steam_1", now.Add(time.Second)),
		},
		Trajectories: []TrajectorySample{observationTrajectory("steam_1", now)},
	}
	if err := repo.RecordPlayerObservation(t.Context(), write); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"activity_events": 2, "trajectory_samples": 1} {
		var got int
		if err := repo.db.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s count=%d want=%d", table, got, want)
		}
	}

	bad := PlayerObservationWrite{
		Events: []ActivityEvent{
			observationEvent("evt-3", "player_joined", "steam_2", now),
			observationEvent("evt-1", "player_left", "steam_2", now.Add(time.Second)),
		},
		Trajectories: []TrajectorySample{observationTrajectory("steam_2", now)},
	}
	if err := repo.RecordPlayerObservation(t.Context(), bad); err == nil {
		t.Fatal("expected duplicate event ID to fail")
	}
	var events, trajectories int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT count(*) FROM activity_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRowContext(t.Context(), `SELECT count(*) FROM trajectory_samples`).Scan(&trajectories); err != nil {
		t.Fatal(err)
	}
	if events != 2 || trajectories != 1 {
		t.Fatalf("failed observation was partially committed: events=%d trajectories=%d", events, trajectories)
	}
}

func TestTimelineReadOrdersRowsDeterministically(t *testing.T) {
	repo, _ := openTemp(t)
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	write := PlayerObservationWrite{
		Events: []ActivityEvent{
			observationEvent("evt-b", "second", "steam_1", now.Add(time.Minute)),
			observationEvent("evt-c", "third", "steam_1", now.Add(2*time.Minute)),
			observationEvent("evt-a", "first", "steam_1", now.Add(time.Minute)),
		},
		Trajectories: []TrajectorySample{
			{UserID: "steam_1", SegmentID: "s", ObservedAt: now.Add(2 * time.Minute), X: 2, Y: 2, Ping: 2, Level: 2, SourceRef: "p2"},
			{UserID: "steam_1", SegmentID: "s", ObservedAt: now.Add(time.Minute), X: 1, Y: 1, Ping: 1, Level: 1, SourceRef: "p1"},
		},
	}
	if err := repo.RecordPlayerObservation(t.Context(), write); err != nil {
		t.Fatal(err)
	}
	events, samples, err := repo.ReadSensitivePlayerTimeline(t.Context(), "admin", "steam_1", now, now.Add(time.Hour), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].ID != "evt-a" || events[1].ID != "evt-b" || events[2].ID != "evt-c" {
		t.Fatalf("events=%#v", events)
	}
	if len(samples) != 2 || !samples[0].ObservedAt.Equal(now.Add(time.Minute)) || !samples[1].ObservedAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("samples=%#v", samples)
	}
	var outcome string
	if err := repo.db.QueryRowContext(t.Context(), `SELECT outcome FROM sensitive_access_audit`).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if outcome != "success" {
		t.Fatalf("audit outcome=%q", outcome)
	}
}

func TestObservationRecordServerMetricsRejectsStaleWrites(t *testing.T) {
	repo, _ := openTemp(t)
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	metrics := domain.ServerMetrics{ServerFPS: 60, CurrentPlayerNum: 2, ServerFrameTime: 16.5, MaxPlayerNum: 32, UptimeSeconds: 100, BaseCampNum: 4, Days: 8}
	if err := repo.RecordServerMetrics(t.Context(), now, metrics); err != nil {
		t.Fatal(err)
	}
	older := metrics
	older.ServerFPS = 1
	if err := repo.RecordServerMetrics(t.Context(), now.Add(-time.Second), older); err == nil {
		t.Fatal("expected older metric sample to be rejected")
	}
	if err := repo.RecordServerMetrics(t.Context(), now, older); err == nil {
		t.Fatal("expected same-time metric sample to be rejected")
	}
	var count, fps int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT count(*),server_fps FROM server_metric_samples`).Scan(&count, &fps); err != nil {
		t.Fatal(err)
	}
	if count != 1 || fps != 60 {
		t.Fatalf("count=%d fps=%d", count, fps)
	}
}

func TestObservationFixedWidthTimesPreserveTimelineOrderAndHalfOpenRange(t *testing.T) {
	repo, _ := openTemp(t)
	base := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	end := base.Add(time.Second)
	write := PlayerObservationWrite{
		Events: []ActivityEvent{
			observationEvent("whole", "whole", "steam_1", base),
			observationEvent("fraction", "fraction", "steam_1", base.Add(100*time.Millisecond)),
			observationEvent("before-end", "before", "steam_1", end.Add(-100*time.Millisecond)),
			observationEvent("at-end", "boundary", "steam_1", end),
			observationEvent("after-end", "after", "steam_1", end.Add(100*time.Millisecond)),
		},
		Trajectories: []TrajectorySample{
			observationTrajectory("steam_1", base),
			observationTrajectory("steam_1", base.Add(100*time.Millisecond)),
		},
	}
	if err := repo.RecordPlayerObservation(t.Context(), write); err != nil {
		t.Fatal(err)
	}
	events, samples, err := repo.ReadSensitivePlayerTimeline(t.Context(), "admin", "steam_1", base, end, 20)
	if err != nil {
		t.Fatal(err)
	}
	var eventIDs []string
	for _, event := range events {
		eventIDs = append(eventIDs, event.ID)
	}
	if got := strings.Join(eventIDs, ","); got != "whole,fraction,before-end" {
		t.Fatalf("event order/range=%q", got)
	}
	if len(samples) != 2 || !samples[0].ObservedAt.Equal(base) || !samples[1].ObservedAt.Equal(base.Add(100*time.Millisecond)) {
		t.Fatalf("trajectory order=%#v", samples)
	}
}

func TestObservationFixedWidthTimesProtectCleanupBoundary(t *testing.T) {
	repo, _ := openTemp(t)
	base := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	cutoff := base.Add(time.Second)
	before, after := cutoff.Add(-100*time.Millisecond), cutoff.Add(100*time.Millisecond)
	if err := repo.RecordPlayerObservation(t.Context(), PlayerObservationWrite{
		Events: []ActivityEvent{
			observationEvent("before", "sample", "u", before),
			observationEvent("after", "sample", "u", after),
		},
		Trajectories: []TrajectorySample{
			observationTrajectory("before", before),
			observationTrajectory("after", after),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordServerMetrics(t.Context(), before, domain.ServerMetrics{ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordServerMetrics(t.Context(), after, domain.ServerMetrics{ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	deleted, err := repo.CleanupRawObservations(t.Context(), cutoff, 10)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("deleted=%d want=3", deleted)
	}
	for table, column := range map[string]string{
		"activity_events": "occurred_at", "trajectory_samples": "observed_at", "server_metric_samples": "observed_at",
	} {
		var count int
		if err := repo.db.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table+` WHERE `+column+`=?`, "2026-07-13T08:00:01.100000000Z").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("post-cutoff %s count=%d", table, count)
		}
	}
}

func TestObservationFixedWidthMetricTimesRejectOutOfOrderFraction(t *testing.T) {
	repo, _ := openTemp(t)
	base := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	metrics := domain.ServerMetrics{ServerFPS: 60, ServerFrameTime: 1}
	if err := repo.RecordServerMetrics(t.Context(), base, metrics); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordServerMetrics(t.Context(), base.Add(200*time.Millisecond), metrics); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordServerMetrics(t.Context(), base.Add(100*time.Millisecond), metrics); err == nil {
		t.Fatal("expected sample older than latest fractional sample to fail")
	}
	var count int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT count(*) FROM server_metric_samples`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("metric count=%d", count)
	}
}

func TestObservationDocumentsAndAuditUseFixedWidthTimes(t *testing.T) {
	repo, _ := openTemp(t)
	base := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	if _, err := repo.RecordServerDocument(t.Context(), "info", base.Add(100*time.Millisecond), []byte(`{}`), "h"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.ReadSensitivePlayerTimeline(t.Context(), "admin", "missing", base, base.Add(time.Second), 10); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	var documentAt, rangeStart, rangeEnd, requestedAt string
	if err := repo.db.QueryRowContext(t.Context(), `SELECT observed_at FROM server_documents`).Scan(&documentAt); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRowContext(t.Context(), `SELECT range_start,range_end,requested_at FROM sensitive_access_audit`).Scan(&rangeStart, &rangeEnd, &requestedAt); err != nil {
		t.Fatal(err)
	}
	if documentAt != "2026-07-13T08:00:00.100000000Z" || rangeStart != "2026-07-13T08:00:00.000000000Z" || rangeEnd != "2026-07-13T08:00:01.000000000Z" {
		t.Fatalf("document=%q range=%q..%q", documentAt, rangeStart, rangeEnd)
	}
	if len(requestedAt) != len("2026-07-13T08:00:00.000000000Z") || !strings.HasSuffix(requestedAt, "Z") {
		t.Fatalf("requested_at is not fixed width: %q", requestedAt)
	}
}

func TestObservationRecordServerDocumentDeduplicatesCanonicalHash(t *testing.T) {
	repo, _ := openTemp(t)
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	inserted, err := repo.RecordServerDocument(t.Context(), "info", now, []byte(`{"name":"server"}`), "sha256:one")
	if err != nil || !inserted {
		t.Fatalf("inserted=%v err=%v", inserted, err)
	}
	inserted, err = repo.RecordServerDocument(t.Context(), "info", now.Add(time.Minute), []byte(`{"name":"server"}`), "sha256:one")
	if err != nil || inserted {
		t.Fatalf("duplicate inserted=%v err=%v", inserted, err)
	}
	var count int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT count(*) FROM server_documents`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count=%d", count)
	}
}

func TestSensitiveTimelineAuditsNotFoundAndQueryError(t *testing.T) {
	repo, _ := openTemp(t)
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	_, _, err := repo.ReadSensitivePlayerTimeline(t.Context(), "admin", "missing", now, now.Add(time.Hour), 20)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("not-found err=%v", err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `DROP TABLE trajectory_samples`); err != nil {
		t.Fatal(err)
	}
	_, _, err = repo.ReadSensitivePlayerTimeline(t.Context(), "admin", "steam_1", now, now.Add(time.Hour), 20)
	if err == nil {
		t.Fatal("expected query error")
	}
	rows, err := repo.db.QueryContext(t.Context(), `SELECT outcome FROM sensitive_access_audit ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var outcome string
		if err := rows.Scan(&outcome); err != nil {
			t.Fatal(err)
		}
		got = append(got, outcome)
	}
	if strings.Join(got, ",") != "not_found,error" {
		t.Fatalf("audit outcomes=%v", got)
	}
}

func TestSensitiveTimelineRejectsInvalidInputWithoutAudit(t *testing.T) {
	repo, _ := openTemp(t)
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, actor, userID string
		start, end          time.Time
		limit               int
	}{
		{"actor", "", "u", now, now.Add(time.Hour), 1},
		{"user", "admin", "", now, now.Add(time.Hour), 1},
		{"start", "admin", "u", time.Time{}, now, 1},
		{"range", "admin", "u", now, now, 1},
		{"low limit", "admin", "u", now, now.Add(time.Hour), 0},
		{"high limit", "admin", "u", now, now.Add(time.Hour), 2001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := repo.ReadSensitivePlayerTimeline(t.Context(), tc.actor, tc.userID, tc.start, tc.end, tc.limit); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	var count int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sensitive_access_audit`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("validation attempts were audited: count=%d", count)
	}
}

func TestObservationValidationRejectsMalformedWrites(t *testing.T) {
	repo, _ := openTemp(t)
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	valid := observationEvent("evt", "joined", "u", now)
	for name, mutate := range map[string]func(*ActivityEvent){
		"empty ID":       func(e *ActivityEvent) { e.ID = "" },
		"empty type":     func(e *ActivityEvent) { e.EventType = "" },
		"empty subject":  func(e *ActivityEvent) { e.SubjectID = "" },
		"zero timestamp": func(e *ActivityEvent) { e.OccurredAt = time.Time{} },
		"unknown source": func(e *ActivityEvent) { e.Source = "unknown" },
		"confidence":     func(e *ActivityEvent) { e.Confidence = "guessed" },
		"schema version": func(e *ActivityEvent) { e.SchemaVersion = 0 },
		"payload array":  func(e *ActivityEvent) { e.PayloadJSON = `[]` },
		"invalid JSON":   func(e *ActivityEvent) { e.PayloadJSON = `{` },
	} {
		t.Run(name, func(t *testing.T) {
			e := valid
			mutate(&e)
			if err := repo.RecordPlayerObservation(t.Context(), PlayerObservationWrite{Events: []ActivityEvent{e}}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	for name, mutate := range map[string]func(*TrajectorySample){
		"empty user":       func(s *TrajectorySample) { s.UserID = "" },
		"empty segment":    func(s *TrajectorySample) { s.SegmentID = "" },
		"empty source ref": func(s *TrajectorySample) { s.SourceRef = "" },
		"zero timestamp":   func(s *TrajectorySample) { s.ObservedAt = time.Time{} },
		"nan x":            func(s *TrajectorySample) { s.X = math.NaN() },
		"infinite y":       func(s *TrajectorySample) { s.Y = math.Inf(1) },
		"infinite ping":    func(s *TrajectorySample) { s.Ping = math.Inf(-1) },
	} {
		t.Run(name, func(t *testing.T) {
			s := observationTrajectory("u", now)
			mutate(&s)
			if err := repo.RecordPlayerObservation(t.Context(), PlayerObservationWrite{Trajectories: []TrajectorySample{s}}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if err := repo.RecordServerMetrics(t.Context(), now, domain.ServerMetrics{ServerFrameTime: math.NaN()}); err == nil {
		t.Fatal("expected non-finite frame time to fail")
	}
	for _, tc := range []struct {
		kind string
		at   time.Time
		body []byte
		hash string
	}{{"other", now, []byte(`{}`), "h"}, {"info", time.Time{}, []byte(`{}`), "h"}, {"info", now, []byte(`[]`), "h"}, {"settings", now, []byte(`{`), "h"}, {"settings", now, []byte(`{}`), ""}} {
		if _, err := repo.RecordServerDocument(t.Context(), tc.kind, tc.at, tc.body, tc.hash); err == nil {
			t.Fatalf("expected invalid document to fail: %#v", tc)
		}
	}
}

func TestObservationCleanupIsBoundedAndPreservesProtectedRows(t *testing.T) {
	repo, _ := openTemp(t)
	ctx := t.Context()
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	cutoff := now.Add(time.Hour)
	for i := 0; i < 3; i++ {
		at := now.Add(time.Duration(i) * time.Minute)
		e := observationEvent("old-"+string(rune('a'+i)), "joined", "u", at)
		s := observationTrajectory("u"+string(rune('a'+i)), at)
		if err := repo.RecordPlayerObservation(ctx, PlayerObservationWrite{Events: []ActivityEvent{e}, Trajectories: []TrajectorySample{s}}); err != nil {
			t.Fatal(err)
		}
		if err := repo.RecordServerMetrics(ctx, at, domain.ServerMetrics{ServerFrameTime: 1}); err != nil {
			t.Fatal(err)
		}
	}
	newAt := cutoff.Add(time.Minute)
	if err := repo.RecordPlayerObservation(ctx, PlayerObservationWrite{Events: []ActivityEvent{observationEvent("new", "joined", "u", newAt)}, Trajectories: []TrajectorySample{observationTrajectory("new", newAt)}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordServerMetrics(ctx, newAt, domain.ServerMetrics{ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RecordServerDocument(ctx, "info", now, []byte(`{}`), "h"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.ReadSensitivePlayerTimeline(ctx, "admin", "missing", now, cutoff, 10); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	deleted, err := repo.CleanupRawObservations(ctx, cutoff, 1)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("deleted=%d want=3", deleted)
	}
	for table, want := range map[string]int{"activity_events": 3, "trajectory_samples": 3, "server_metric_samples": 3, "server_documents": 1, "sensitive_access_audit": 1} {
		var got int
		if err := repo.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s count=%d want=%d", table, got, want)
		}
	}
	if deleted, err = repo.CleanupRawObservations(ctx, cutoff, 0); err == nil || deleted != 0 {
		t.Fatalf("invalid cleanup deleted=%d err=%v", deleted, err)
	}
	if deleted, err = repo.CleanupRawObservations(ctx, cutoff, 2001); err == nil || deleted != 0 {
		t.Fatalf("oversized cleanup deleted=%d err=%v", deleted, err)
	}
	for _, table := range []string{"activity_events", "trajectory_samples", "server_metric_samples"} {
		var oldRows int
		if err := repo.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&oldRows); err != nil {
			t.Fatal(err)
		}
		if oldRows != 3 {
			t.Fatalf("oversized cleanup changed %s: count=%d", table, oldRows)
		}
	}
}
