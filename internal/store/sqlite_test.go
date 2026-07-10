package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
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
