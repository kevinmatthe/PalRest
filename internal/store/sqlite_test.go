package store

import (
	"context"
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
