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
	if version != 9 {
		t.Fatalf("migration version=%d", version)
	}

	for _, table := range []string{
		"player_sessions",
		"concurrency_buckets",
		"player_daily_stats",
		"analytics_observation_state",
		"activity_events",
		"trajectory_samples",
		"server_metric_samples",
		"server_documents",
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
	for _, index := range []string{"activity_events_subject_time", "activity_events_retention", "trajectory_user_time", "trajectory_samples_retention", "sensitive_audit_actor_time"} {
		var count int
		if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("index %s count=%d", index, count)
		}
	}

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
	if version != 9 || players != 1 {
		t.Fatalf("version=%d players=%d", version, players)
	}
	for _, table := range []string{"activity_events", "trajectory_samples", "server_metric_samples", "server_documents", "sensitive_access_audit"} {
		var count int
		if err := repo.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("table %s count=%d", table, count)
		}
	}
	for _, index := range []string{"activity_events_subject_time", "activity_events_retention", "trajectory_user_time", "trajectory_samples_retention", "sensitive_audit_actor_time"} {
		var count int
		if err := repo.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("index %s count=%d", index, count)
		}
	}

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

func TestObservationCleanupSelectorsUseTimeLeadingIndexes(t *testing.T) {
	repo, _ := openTemp(t)
	for _, tc := range []struct {
		name, query, index string
	}{
		{"activity events", `SELECT rowid FROM activity_events WHERE occurred_at<? ORDER BY occurred_at,id LIMIT ?`, "activity_events_retention"},
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
DROP TABLE activity_events;
DROP TABLE trajectory_samples;
DROP TABLE server_metric_samples;
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
	if version != 9 || len(players) != 1 || players[0].UserID != "u1" || !watermark.Equal(at) {
		t.Fatalf("version=%d players=%+v watermark=%v", version, players, watermark)
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
DROP TABLE activity_events;
DROP TABLE trajectory_samples;
DROP TABLE server_metric_samples;
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
	if version != 9 || indexes != 1 || players != 1 || sessions != 1 {
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
