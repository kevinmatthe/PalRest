package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

func openTemp(t *testing.T) (*Repository, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "guard.db")
	repo, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo, path
}

func samplePeriod() domain.Period {
	return domain.Period{
		Key:   "daily-2026-07-10",
		Start: time.Date(2026, 7, 9, 20, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 7, 10, 20, 0, 0, 0, time.UTC),
	}
}

func TestOpenEnablesSQLiteSafetyPragmas(t *testing.T) {
	repo, _ := openTemp(t)
	var foreignKeys int
	if err := repo.db.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	var journalMode string
	if err := repo.db.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 || journalMode != "wal" {
		t.Fatalf("foreign_keys=%d journal_mode=%s", foreignKeys, journalMode)
	}
}

func TestOpenMigrationCreatesAnalyticsSchema(t *testing.T) {
	repo, _ := openTemp(t)

	var version int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 12 {
		t.Fatalf("migration version=%d", version)
	}

	for _, table := range []string{
		"player_sessions",
		"concurrency_buckets",
		"player_daily_stats",
		"analytics_observation_state",
		"activity_events",
		"trajectory_samples",
		"player_private_samples",
		"server_metric_samples",
		"server_documents",
		"server_document_observations",
		"server_observation_state",
		"server_runtime_state",
		"sensitive_access_audit",
	} {
		var count int
		if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("table %s count=%d", table, count)
		}
	}
	var cleanupIndex int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='player_sessions_ended_at'`).Scan(&cleanupIndex); err != nil {
		t.Fatal(err)
	}
	if cleanupIndex != 1 {
		t.Fatalf("player_sessions_ended_at count=%d", cleanupIndex)
	}
	for _, index := range []string{"player_sessions_user_last_observed", "activity_events_subject_time", "activity_events_retention", "trajectory_user_time", "trajectory_samples_retention", "trajectory_user_runtime_time", "player_private_samples_user_time", "player_private_samples_retention", "server_metric_samples_event", "server_document_observations_kind_time", "server_document_observations_event", "sensitive_audit_actor_time"} {
		var count int
		if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("index %s count=%d", index, count)
		}
	}
	var metricEventColumn int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pragma_table_info('server_metric_samples') WHERE name='event_id'`).Scan(&metricEventColumn); err != nil {
		t.Fatal(err)
	}
	if metricEventColumn != 1 {
		t.Fatalf("server_metric_samples.event_id count=%d", metricEventColumn)
	}
	assertServerObservationForeignKeys(t, repo)

	now := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	if _, err := repo.db.ExecContext(t.Context(), `
INSERT INTO players(user_id, name, first_seen, last_online) VALUES(?, ?, ?, ?)`,
		"steam_1", "Kevin", formatTime(now), formatTime(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `
INSERT INTO player_sessions(user_id, started_at, last_observed_at) VALUES(?, ?, ?)`,
		"steam_1", formatTime(now), formatTime(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `
INSERT INTO player_sessions(user_id, started_at, last_observed_at) VALUES(?, ?, ?)`,
		"steam_1", formatTime(now.Add(time.Minute)), formatTime(now.Add(time.Minute))); err == nil {
		t.Fatal("expected duplicate open session to fail")
	}
	if _, err := repo.db.ExecContext(t.Context(), `
INSERT INTO player_sessions(user_id, started_at, ended_at, last_observed_at, close_reason) VALUES(?, ?, ?, ?, ?)`,
		"steam_1", formatTime(now.Add(time.Minute)), formatTime(now.Add(2*time.Minute)), formatTime(now.Add(2*time.Minute)), "offline"); err != nil {
		t.Fatalf("insert closed session: %v", err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `
INSERT INTO trajectory_samples(user_id, segment_id, observed_at, x, y, ping, level, source_ref)
VALUES('steam_1', 'segment_1', ?, 1, 2, 3, 4, 'sample_1'),
      ('steam_1', 'segment_2', ?, 5, 6, 7, 8, 'sample_2')`, formatTime(now), formatTime(now)); err == nil {
		t.Fatal("expected duplicate trajectory sample to fail")
	}
	if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO activity_events(id) VALUES('event_1')`); err == nil {
		t.Fatal("expected incomplete activity event to fail")
	}
}

func TestOpenMigrationUpgradesVersionEightToNineWithoutLosingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guard.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schemaV1 + schemaV2 + schemaV3 + schemaV4 + schemaV5 + schemaV6 + schemaV7 + schemaV8 + `
DELETE FROM schema_migrations;
INSERT INTO schema_migrations(version,applied_at) VALUES
(1,'2026-01-01T00:00:00Z'),(2,'2026-01-01T00:00:00Z'),(3,'2026-01-01T00:00:00Z'),
(4,'2026-01-01T00:00:00Z'),(5,'2026-01-01T00:00:00Z'),(6,'2026-01-01T00:00:00Z'),
(7,'2026-01-01T00:00:00Z'),(8,'2026-01-01T00:00:00Z');
INSERT INTO players(user_id,name,first_seen,last_online)
VALUES('u1','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	repo, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	var version, players int
	if err := repo.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM players WHERE user_id='u1' AND name='One'`).Scan(&players); err != nil {
		t.Fatal(err)
	}
	if version != 12 || players != 1 {
		t.Fatalf("version=%d players=%d", version, players)
	}
	for _, table := range []string{"activity_events", "trajectory_samples", "player_private_samples", "server_metric_samples", "server_documents", "server_document_observations", "server_observation_state", "server_runtime_state", "sensitive_access_audit"} {
		var count int
		if err := repo.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("table %s count=%d", table, count)
		}
	}
	for _, index := range []string{"player_sessions_user_last_observed", "activity_events_subject_time", "activity_events_retention", "trajectory_user_time", "trajectory_samples_retention", "trajectory_user_runtime_time", "player_private_samples_user_time", "player_private_samples_retention", "server_metric_samples_event", "server_document_observations_kind_time", "server_document_observations_event", "sensitive_audit_actor_time"} {
		var count int
		if err := repo.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("index %s count=%d", index, count)
		}
	}
	var metricEventColumn int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('server_metric_samples') WHERE name='event_id'`).Scan(&metricEventColumn); err != nil {
		t.Fatal(err)
	}
	if metricEventColumn != 1 {
		t.Fatalf("server_metric_samples.event_id count=%d", metricEventColumn)
	}
	assertServerObservationForeignKeys(t, repo)

	if _, err := repo.db.Exec(`
INSERT INTO activity_events(
    id, event_type, subject_type, subject_id, occurred_at, observed_at,
    source, source_ref, correlation_id, confidence, schema_version, payload_json
) VALUES('event_1', 'joined', 'player', 'u1', '2026-01-01T00:00:00Z', '2026-01-01T00:00:01Z',
         'rest', 'status_1', 'correlation_1', 'observed', 1, '{}')`); err != nil {
		t.Fatalf("insert activity event: %v", err)
	}
	if _, err := repo.db.Exec(`
INSERT INTO activity_events(
    id, event_type, subject_type, subject_id, occurred_at, observed_at,
    source, source_ref, correlation_id, confidence, schema_version, payload_json
) VALUES('event_1', 'left', 'player', 'u1', '2026-01-01T01:00:00Z', '2026-01-01T01:00:01Z',
         'rest', 'status_2', 'correlation_2', 'observed', 1, '{}')`); err == nil {
		t.Fatal("expected duplicate activity event id to fail")
	}
	if _, err := repo.db.Exec(`INSERT INTO activity_events(id) VALUES('event_2')`); err == nil {
		t.Fatal("expected activity event NOT NULL constraint to fail")
	}
	if _, err := repo.db.Exec(`
INSERT INTO activity_events(
    id, event_type, subject_type, subject_id, occurred_at, observed_at,
    source, source_ref, correlation_id, confidence, schema_version, payload_json
) VALUES(NULL, 'joined', 'player', 'u1', '2026-01-01T02:00:00Z', '2026-01-01T02:00:01Z',
         'rest', 'status_3', 'correlation_3', 'observed', 1, '{}')`); err == nil {
		t.Error("expected NULL activity event id to fail")
	}

	if _, err := repo.db.Exec(`
INSERT INTO trajectory_samples(user_id, segment_id, observed_at, x, y, ping, level, source_ref)
VALUES('u1', 'segment_1', '2026-01-01T00:00:00Z', 1, 2, 3, 4, 'sample_1')`); err != nil {
		t.Fatalf("insert trajectory sample: %v", err)
	}
	if _, err := repo.db.Exec(`
INSERT INTO trajectory_samples(user_id, segment_id, observed_at, x, y, ping, level, source_ref)
VALUES('u1', 'segment_2', '2026-01-01T00:00:00Z', 5, 6, 7, 8, 'sample_2')`); err == nil {
		t.Fatal("expected duplicate trajectory user/time to fail")
	}

	if _, err := repo.db.Exec(`
INSERT INTO server_documents(kind, content_hash, observed_at, canonical_json)
VALUES('settings', 'hash_1', '2026-01-01T00:00:00Z', '{}')`); err != nil {
		t.Fatalf("insert server document: %v", err)
	}
	if _, err := repo.db.Exec(`
INSERT INTO server_documents(kind, content_hash, observed_at, canonical_json)
VALUES('settings', 'hash_1', '2026-01-01T01:00:00Z', '{"changed":true}')`); err == nil {
		t.Fatal("expected duplicate server document kind/hash to fail")
	}
	if _, err := repo.db.Exec(`
INSERT INTO server_documents(kind, content_hash, observed_at)
VALUES('settings', 'hash_2', '2026-01-01T01:00:00Z')`); err == nil {
		t.Fatal("expected server document NOT NULL constraint to fail")
	}
	if _, err := repo.db.Exec(`
INSERT INTO server_metric_samples(
    observed_at, server_fps, current_player_num, server_frame_time,
    max_player_num, uptime_seconds, base_camp_num, game_days
) VALUES(NULL, 60, 1, 16.67, 32, 3600, 2, 10)`); err == nil {
		t.Error("expected NULL server metric observed_at to fail")
	}

	if _, err := repo.db.Exec(`
INSERT INTO sensitive_access_audit(actor, action, subject_type, subject_id, outcome, requested_at)
VALUES('admin', 'read_timeline', 'player', 'u1', 'success', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert audit with nullable ranges: %v", err)
	}
}

func assertServerObservationForeignKeys(t *testing.T, repo *Repository) {
	t.Helper()
	for table, want := range map[string]int{"server_metric_samples": 1, "server_document_observations": 3, "server_observation_state": 2} {
		var got int
		if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pragma_foreign_key_list(?)`, table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s foreign keys=%d want=%d", table, got, want)
		}
	}
}

func TestObservationCleanupSelectorsUseTimeLeadingIndexes(t *testing.T) {
	repo, _ := openTemp(t)
	for _, tc := range []struct {
		name, query, index string
	}{
		{"activity events", `SELECT e.rowid FROM activity_events e
WHERE e.occurred_at<?
  AND NOT EXISTS (SELECT 1 FROM server_metric_samples m WHERE m.event_id=e.id)
  AND NOT EXISTS (SELECT 1 FROM server_document_observations d WHERE d.event_id=e.id)
ORDER BY e.occurred_at,e.id LIMIT ?`, "activity_events_retention"},
		{"trajectory samples", `SELECT id FROM trajectory_samples WHERE observed_at<? ORDER BY observed_at,id LIMIT ?`, "trajectory_samples_retention"},
		{"server metrics", `SELECT rowid FROM server_metric_samples WHERE observed_at<? ORDER BY observed_at LIMIT ?`, "sqlite_autoindex_server_metric_samples_1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := repo.db.QueryContext(t.Context(), `EXPLAIN QUERY PLAN `+tc.query, "2026-07-13T08:00:00.000000000Z", 100)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			plan := strings.ToLower(strings.Join(details, " | "))
			if !strings.Contains(plan, strings.ToLower(tc.index)) {
				t.Fatalf("plan does not use %q: %s", tc.index, plan)
			}
			if strings.Contains(plan, "use temp b-tree") || strings.Contains(plan, "scan activity_events") || strings.Contains(plan, "scan trajectory_samples") || strings.Contains(plan, "scan server_metric_samples") {
				t.Fatalf("cleanup selector uses scan or temp sort: %s", plan)
			}
		})
	}
}

func TestOpenMigratesVersionSevenToEightWithoutLosingAnalytics(t *testing.T) {
	repo, path := openTemp(t)
	at := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	if err := repo.WithTx(t.Context(), func(tx *Tx) error { return tx.UpsertPlayer(domain.Player{UserID: "u1", Name: "One"}, at) }); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`INSERT INTO player_sessions(user_id, started_at, last_observed_at) VALUES('u1', ?, ?)`, formatTime(at.Add(-time.Minute)), formatTime(at)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`
DROP TABLE analytics_observation_state;
DROP INDEX player_sessions_user_last_observed;
DROP TABLE activity_events;
DROP TABLE trajectory_samples;
DROP TABLE player_private_samples;
DROP TABLE server_metric_samples;
DROP TABLE server_document_observations;
DROP TABLE server_observation_state;
DROP TABLE server_documents;
DROP TABLE sensitive_access_audit;
DELETE FROM schema_migrations WHERE version>=8`); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	var version int
	if err := repo.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	players, watermark, err := repo.OpenAnalyticsPlayers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if version != 12 || len(players) != 1 || players[0].UserID != "u1" || !watermark.Equal(at) {
		t.Fatalf("version=%d players=%+v watermark=%v", version, players, watermark)
	}
}

func TestOpenMigratesVersionNineToTenPreservingObservationData(t *testing.T) {
	repo, path := openTemp(t)
	at := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	event := observationEvent("preserved-v9", "player_joined", "u1", at)
	if err := repo.RecordPlayerObservation(t.Context(), PlayerObservationWrite{Events: []ActivityEvent{event}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `DROP TABLE player_private_samples; DROP INDEX player_sessions_user_last_observed; DELETE FROM schema_migrations WHERE version>=10`); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	var version, events, indexes int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRowContext(t.Context(), `SELECT count(*) FROM activity_events WHERE id='preserved-v9'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('player_private_samples_user_time','player_private_samples_retention')`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if version != 12 || events != 1 || indexes != 2 {
		t.Fatalf("version=%d events=%d indexes=%d", version, events, indexes)
	}
	valid := PlayerPrivateSample{UserID: "u1", ObservedAt: at, IP: "192.0.2.1", SourceRef: "poll"}
	if err := repo.RecordPlayerObservation(t.Context(), PlayerObservationWrite{PrivateSamples: []PlayerPrivateSample{valid}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordPlayerObservation(t.Context(), PlayerObservationWrite{PrivateSamples: []PlayerPrivateSample{valid}}); err != nil {
		t.Fatalf("exact private sample replay: %v", err)
	}
	conflict := valid
	conflict.IP = "192.0.2.2"
	if err := repo.RecordPlayerObservation(t.Context(), PlayerObservationWrite{PrivateSamples: []PlayerPrivateSample{conflict}}); err == nil {
		t.Fatal("expected same-time private sample conflict")
	}
	if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO player_private_samples(user_id,observed_at,ip,ping,level,source_ref) VALUES('null-ip','2026-07-13T08:00:01.000000000Z',NULL,0,0,'poll')`); err == nil {
		t.Fatal("expected NOT NULL IP constraint")
	}
}

func TestOpenMigratesVersionTenToElevenWithPlayerSessionLookupIndex(t *testing.T) {
	repo, path := openTemp(t)
	at := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	if err := repo.WithTx(t.Context(), func(tx *Tx) error { return tx.UpsertPlayer(domain.Player{UserID: "v10-player"}, at) }); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO player_sessions(user_id,started_at,last_observed_at) VALUES('v10-player',?,?)`, formatTime(at), formatTime(at)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `DROP INDEX IF EXISTS player_sessions_user_last_observed; DELETE FROM schema_migrations WHERE version>=11`); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	var version, indexes, sessions int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE type='index' AND name='player_sessions_user_last_observed'`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRowContext(t.Context(), `SELECT count(*) FROM player_sessions WHERE user_id='v10-player'`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if version != 12 || indexes != 1 || sessions != 1 {
		t.Fatalf("version=%d indexes=%d sessions=%d", version, indexes, sessions)
	}
}

func TestOpenMigratesVersionElevenToTwelvePreservingTrajectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guard.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schemaV1 + schemaV2 + schemaV3 + schemaV4 + schemaV5 + schemaV6 + schemaV7 + schemaV8 + schemaV9 + schemaV10 + schemaV11 + `
DELETE FROM schema_migrations;
INSERT INTO schema_migrations(version,applied_at) VALUES
(1,'2026-01-01T00:00:00Z'),(2,'2026-01-01T00:00:00Z'),(3,'2026-01-01T00:00:00Z'),
(4,'2026-01-01T00:00:00Z'),(5,'2026-01-01T00:00:00Z'),(6,'2026-01-01T00:00:00Z'),
(7,'2026-01-01T00:00:00Z'),(8,'2026-01-01T00:00:00Z'),(9,'2026-01-01T00:00:00Z'),
(10,'2026-01-01T00:00:00Z'),(11,'2026-01-01T00:00:00Z');
INSERT INTO trajectory_samples(user_id,segment_id,observed_at,x,y,ping,level,source_ref)
VALUES('u1','legacy-segment','2026-01-01T00:00:00Z',1,2,3,4,'legacy-poll');`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	var version, runtimeColumn, runtimeIndex, trajectories int
	for query, dest := range map[string]*int{
		`SELECT MAX(version) FROM schema_migrations`:                                                                     &version,
		`SELECT COUNT(*) FROM pragma_table_info('trajectory_samples') WHERE name='runtime_epoch'`:                        &runtimeColumn,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='trajectory_user_runtime_time'`:                  &runtimeIndex,
		`SELECT COUNT(*) FROM trajectory_samples WHERE user_id='u1' AND segment_id='legacy-segment' AND runtime_epoch=0`: &trajectories,
	} {
		if err := repo.db.QueryRowContext(t.Context(), query).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	state, err := repo.CurrentServerRuntime(t.Context())
	if err != nil || version != 12 || runtimeColumn != 1 || runtimeIndex != 1 || trajectories != 1 || state.Epoch != 0 || !state.RestartedAt.IsZero() {
		t.Fatalf("version=%d column=%d index=%d trajectories=%d state=%+v err=%v", version, runtimeColumn, runtimeIndex, trajectories, state, err)
	}
}

func TestOpenMigratesVersionSevenClosedSessionWatermark(t *testing.T) {
	repo, path := openTemp(t)
	at := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	if err := repo.WithTx(t.Context(), func(tx *Tx) error { return tx.UpsertPlayer(domain.Player{UserID: "u1"}, at) }); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`INSERT INTO player_sessions(user_id, started_at, ended_at, last_observed_at) VALUES('u1', ?, ?, ?)`, formatTime(at.Add(-time.Minute)), formatTime(at), formatTime(at)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`
DROP TABLE analytics_observation_state;
DROP INDEX player_sessions_user_last_observed;
DROP TABLE activity_events;
DROP TABLE trajectory_samples;
DROP TABLE player_private_samples;
DROP TABLE server_metric_samples;
DROP TABLE server_document_observations;
DROP TABLE server_observation_state;
DROP TABLE server_documents;
DROP TABLE sensitive_access_audit;
DELETE FROM schema_migrations WHERE version>=8`); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	players, watermark, err := repo.OpenAnalyticsPlayers(t.Context())
	if err != nil || len(players) != 0 || !watermark.Equal(at) {
		t.Fatalf("players=%+v watermark=%v err=%v", players, watermark, err)
	}
}

func TestOpenUpgradesVersionSixAnalyticsSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guard.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schemaV1 + schemaV2 + schemaV3 + schemaV4 + schemaV5 + schemaV6 + `
DELETE FROM schema_migrations;
INSERT INTO schema_migrations(version,applied_at) VALUES(1,'2026-01-01T00:00:00Z'),(2,'2026-01-01T00:00:00Z'),(3,'2026-01-01T00:00:00Z'),(4,'2026-01-01T00:00:00Z'),(5,'2026-01-01T00:00:00Z'),(6,'2026-01-01T00:00:00Z');
INSERT INTO players(user_id,name,first_seen,last_online) VALUES('u1','One','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
INSERT INTO player_sessions(user_id,started_at,ended_at,last_observed_at) VALUES('u1','2026-01-01T00:00:00Z','2026-01-01T01:00:00Z','2026-01-01T01:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	var version, indexes, players, sessions int
	for query, dest := range map[string]*int{`SELECT MAX(version) FROM schema_migrations`: &version, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='player_sessions_ended_at'`: &indexes, `SELECT COUNT(*) FROM players WHERE user_id='u1' AND name='One'`: &players, `SELECT COUNT(*) FROM player_sessions WHERE user_id='u1' AND ended_at='2026-01-01T01:00:00Z'`: &sessions} {
		if err := repo.db.QueryRowContext(t.Context(), query).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	if version != 12 || indexes != 1 || players != 1 || sessions != 1 {
		t.Fatalf("version=%d index=%d players=%d sessions=%d", version, indexes, players, sessions)
	}
}

func TestUsagePersistsAcrossReopen(t *testing.T) {
	repo, path := openTemp(t)
	now := time.Date(2026, 7, 10, 0, 0, 30, 0, time.UTC)
	err := repo.WithTx(t.Context(), func(tx *Tx) error {
		if err := tx.UpsertPlayer(domain.Player{UserID: "steam_1", Name: "Kevin"}, now); err != nil {
			return err
		}
		_, err := tx.AddUsage("steam_1", samplePeriod(), 30*time.Second, now)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := repo.Usage(t.Context(), "steam_1", samplePeriod().Key)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Used != 30*time.Second || usage.UserID != "steam_1" {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestWithTxRollsBackUsageAndPlayerTogether(t *testing.T) {
	repo, _ := openTemp(t)
	err := repo.WithTx(t.Context(), func(tx *Tx) error {
		now := time.Now().UTC()
		if err := tx.UpsertPlayer(domain.Player{UserID: "steam_1", Name: "Kevin"}, now); err != nil {
			return err
		}
		if _, err := tx.AddUsage("steam_1", samplePeriod(), time.Minute, now); err != nil {
			return err
		}
		return errors.New("stop")
	})
	if err == nil {
		t.Fatal("expected rollback")
	}
	if _, err := repo.Usage(t.Context(), "steam_1", samplePeriod().Key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("usage error=%v", err)
	}
	if _, err := repo.Player(t.Context(), "steam_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("player error=%v", err)
	}
}

func TestPlayersListsKnownPlayersByLastOnline(t *testing.T) {
	repo, _ := openTemp(t)
	first := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)
	if err := repo.WithTx(t.Context(), func(tx *Tx) error {
		if err := tx.UpsertPlayer(domain.Player{UserID: "steam_old", Name: "Old"}, first); err != nil {
			return err
		}
		return tx.UpsertPlayer(domain.Player{UserID: "steam_new", Name: "New"}, second)
	}); err != nil {
		t.Fatal(err)
	}
	players, err := repo.Players(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(players) != 2 || players[0].UserID != "steam_new" || players[1].UserID != "steam_old" {
		t.Fatalf("players=%+v", players)
	}
}

func TestWarningIsUniqueAndEnforcementIsAppendOnly(t *testing.T) {
	repo, _ := openTemp(t)
	now := time.Now().UTC()
	if err := repo.WithTx(t.Context(), func(tx *Tx) error {
		if err := tx.UpsertPlayer(domain.Player{UserID: "steam_1", Name: "Kevin"}, now); err != nil {
			return err
		}
		first, err := tx.EnsureWarning("steam_1", samplePeriod().Key, 5*time.Minute, now)
		if err != nil || !first {
			return errors.New("first warning was not created")
		}
		second, err := tx.EnsureWarning("steam_1", samplePeriod().Key, 5*time.Minute, now)
		if err != nil || second {
			return errors.New("duplicate warning was created")
		}
		return tx.AppendEnforcement(EnforcementEvent{UserID: "steam_1", PeriodKey: samplePeriod().Key, Action: "kick", Result: "success", CreatedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	events, err := repo.EnforcementEvents(context.Background(), "steam_1", samplePeriod().Key)
	if err != nil || len(events) != 1 || events[0].Result != "success" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestPolicyStateSchemaStoresLastCreditRecovered(t *testing.T) {
	repo, _ := openTemp(t)
	now := time.Now().UTC()
	if err := repo.WithTx(t.Context(), func(tx *Tx) error {
		if err := tx.UpsertPlayer(domain.Player{UserID: "credit-user", Name: "Credit"}, now); err != nil {
			return err
		}
		_, err := tx.tx.Exec(`
INSERT INTO policy_states(user_id, policy_revision, strategy, credit_ms, last_credit_recovered_ms, updated_at)
VALUES(?, ?, 'credit', ?, ?, ?)`, "credit-user", "revision", 45*60*1000, 15*60*1000, formatTime(now))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var recoveredMS int64
	if err := repo.db.QueryRow(`SELECT last_credit_recovered_ms FROM policy_states WHERE user_id=?`, "credit-user").Scan(&recoveredMS); err != nil {
		t.Fatal(err)
	}
	if recoveredMS != 15*60*1000 {
		t.Fatalf("recovered_ms=%d", recoveredMS)
	}
}

func TestPolicyStateReadsLastCreditRecovered(t *testing.T) {
	repo, _ := openTemp(t)
	now := time.Now().UTC()
	if err := repo.WithTx(t.Context(), func(tx *Tx) error {
		if err := tx.UpsertPlayer(domain.Player{UserID: "credit-user", Name: "Credit"}, now); err != nil {
			return err
		}
		if _, err := tx.tx.Exec(`
INSERT INTO policy_states(user_id, policy_revision, strategy, credit_ms, last_credit_recovered_ms, updated_at)
VALUES(?, ?, 'credit', ?, ?, ?)`, "credit-user", "revision", 45*60*1000, 15*60*1000, formatTime(now)); err != nil {
			return err
		}
		state, err := tx.PolicyState("credit-user", "revision")
		if err != nil {
			return err
		}
		field := reflect.ValueOf(state).FieldByName("LastCreditRecovered")
		if !field.IsValid() {
			return errors.New("PolicyState.LastCreditRecovered is missing")
		}
		if got := time.Duration(field.Int()); got != 15*time.Minute {
			return fmt.Errorf("last recovered=%v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyStateUpsertWritesLastCreditRecovered(t *testing.T) {
	repo, _ := openTemp(t)
	now := time.Now().UTC()
	if err := repo.WithTx(t.Context(), func(tx *Tx) error {
		if err := tx.UpsertPlayer(domain.Player{UserID: "credit-user", Name: "Credit"}, now); err != nil {
			return err
		}
		return tx.UpsertPolicyState(PolicyState{
			UserID: "credit-user", PolicyRevision: "revision", Strategy: "credit",
			Credit: 45 * time.Minute, LastCreditAt: now.Add(-time.Hour),
			LastCreditRecovered: 15 * time.Minute, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.WithTx(t.Context(), func(tx *Tx) error {
		state, err := tx.PolicyState("credit-user", "revision")
		if err != nil {
			return err
		}
		if state.LastCreditRecovered != 15*time.Minute {
			return fmt.Errorf("last recovered=%v", state.LastCreditRecovered)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenUpgradesVersionOneEnforcementSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guard.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	oldSchema := `
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
INSERT INTO schema_migrations(version, applied_at) VALUES(1, '2026-07-10T00:00:00Z');
CREATE TABLE players (
    user_id TEXT PRIMARY KEY, player_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL,
    account_name TEXT NOT NULL DEFAULT '', first_seen TEXT NOT NULL, last_online TEXT NOT NULL
);
CREATE TABLE enforcement_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL REFERENCES players(user_id) ON DELETE CASCADE,
    period_key TEXT NOT NULL, action TEXT NOT NULL, result TEXT NOT NULL,
    generation INTEGER NOT NULL DEFAULT 0, error_summary TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);`
	if _, err := db.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	now := time.Now().UTC()
	if err := repo.WithTx(t.Context(), func(tx *Tx) error {
		if err := tx.UpsertPlayer(domain.Player{UserID: "steam_1", Name: "Kevin"}, now); err != nil {
			return err
		}
		return tx.AppendEnforcement(EnforcementEvent{
			UserID: "steam_1", PeriodKey: "period", Action: "kick", Result: "failure",
			PolicyRevision: "revision-2", CreatedAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	events, err := repo.EnforcementEventsForPolicy(t.Context(), "steam_1", "period", "revision-2")
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}
