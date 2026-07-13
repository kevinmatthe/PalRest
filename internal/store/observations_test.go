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
