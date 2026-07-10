package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Repository struct {
	db *sql.DB
}

type Tx struct {
	tx *sql.Tx
}

type EnforcementEvent struct {
	UserID       string
	PeriodKey    string
	Action       string
	Result       string
	Generation   int64
	ErrorSummary string
	CreatedAt    time.Time
}

func Open(ctx context.Context, path string) (*Repository, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	repo := &Repository{db: db}
	if err := repo.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return repo, nil
}

func (r *Repository) migrate(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
		return fmt.Errorf("apply migration 1: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, ?)`, formatTime(time.Now())); err != nil {
		return fmt.Errorf("record migration 1: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func (r *Repository) Close() error { return r.db.Close() }

func (r *Repository) Ping(ctx context.Context) error { return r.db.PingContext(ctx) }

func (r *Repository) WithTx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(&Tx{tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (tx *Tx) UpsertPlayer(player domain.Player, now time.Time) error {
	_, err := tx.tx.Exec(`
INSERT INTO players(user_id, player_id, name, account_name, first_seen, last_online)
VALUES(?, ?, ?, ?, ?, ?)
ON CONFLICT(user_id) DO UPDATE SET
    player_id=excluded.player_id,
    name=excluded.name,
    account_name=excluded.account_name,
    last_online=excluded.last_online`,
		player.UserID, player.PlayerID, player.Name, player.AccountName, formatTime(now), formatTime(now))
	if err != nil {
		return fmt.Errorf("upsert player: %w", err)
	}
	return nil
}

func (tx *Tx) AddUsage(userID string, period domain.Period, delta time.Duration, now time.Time) (time.Duration, error) {
	if delta < 0 {
		return 0, fmt.Errorf("usage delta cannot be negative")
	}
	deltaMS := delta.Milliseconds()
	_, err := tx.tx.Exec(`
INSERT INTO usage_periods(user_id, period_key, period_start, period_end, used_ms, updated_at)
VALUES(?, ?, ?, ?, ?, ?)
ON CONFLICT(user_id, period_key) DO UPDATE SET
    used_ms=usage_periods.used_ms + excluded.used_ms,
    period_start=excluded.period_start,
    period_end=excluded.period_end,
    updated_at=excluded.updated_at`,
		userID, period.Key, formatTime(period.Start), formatTime(period.End), deltaMS, formatTime(now))
	if err != nil {
		return 0, fmt.Errorf("add usage: %w", err)
	}
	var usedMS int64
	if err := tx.tx.QueryRow(`SELECT used_ms FROM usage_periods WHERE user_id=? AND period_key=?`, userID, period.Key).Scan(&usedMS); err != nil {
		return 0, fmt.Errorf("read updated usage: %w", err)
	}
	return time.Duration(usedMS) * time.Millisecond, nil
}

func (tx *Tx) EnsureWarning(userID, periodKey string, threshold time.Duration, now time.Time) (bool, error) {
	result, err := tx.tx.Exec(`
INSERT OR IGNORE INTO warning_events(user_id, period_key, threshold_ms, created_at, updated_at)
VALUES(?, ?, ?, ?, ?)`, userID, periodKey, threshold.Milliseconds(), formatTime(now), formatTime(now))
	if err != nil {
		return false, fmt.Errorf("ensure warning: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (tx *Tx) AppendEnforcement(event EnforcementEvent) error {
	_, err := tx.tx.Exec(`
INSERT INTO enforcement_events(user_id, period_key, action, result, generation, error_summary, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?)`, event.UserID, event.PeriodKey, event.Action, event.Result, event.Generation, event.ErrorSummary, formatTime(event.CreatedAt))
	if err != nil {
		return fmt.Errorf("append enforcement: %w", err)
	}
	return nil
}

func (r *Repository) Usage(ctx context.Context, userID, periodKey string) (domain.Usage, error) {
	var usage domain.Usage
	var start, end, updated string
	var usedMS int64
	err := r.db.QueryRowContext(ctx, `
SELECT user_id, period_key, period_start, period_end, used_ms, updated_at
FROM usage_periods WHERE user_id=? AND period_key=?`, userID, periodKey).
		Scan(&usage.UserID, &usage.Period.Key, &start, &end, &usedMS, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Usage{}, ErrNotFound
	}
	if err != nil {
		return domain.Usage{}, fmt.Errorf("read usage: %w", err)
	}
	usage.Period.Start, err = parseTime(start)
	if err != nil {
		return domain.Usage{}, err
	}
	usage.Period.End, err = parseTime(end)
	if err != nil {
		return domain.Usage{}, err
	}
	usage.Updated, err = parseTime(updated)
	usage.Used = time.Duration(usedMS) * time.Millisecond
	return usage, err
}

func (r *Repository) Player(ctx context.Context, userID string) (domain.Player, error) {
	var player domain.Player
	var lastOnline string
	err := r.db.QueryRowContext(ctx, `SELECT user_id, player_id, name, account_name, last_online FROM players WHERE user_id=?`, userID).
		Scan(&player.UserID, &player.PlayerID, &player.Name, &player.AccountName, &lastOnline)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Player{}, ErrNotFound
	}
	if err != nil {
		return domain.Player{}, fmt.Errorf("read player: %w", err)
	}
	player.LastOnline, err = parseTime(lastOnline)
	return player, err
}

func (r *Repository) EnforcementEvents(ctx context.Context, userID, periodKey string) ([]EnforcementEvent, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT user_id, period_key, action, result, generation, error_summary, created_at
FROM enforcement_events WHERE user_id=? AND period_key=? ORDER BY id`, userID, periodKey)
	if err != nil {
		return nil, fmt.Errorf("query enforcement events: %w", err)
	}
	defer rows.Close()
	var events []EnforcementEvent
	for rows.Next() {
		var event EnforcementEvent
		var created string
		if err := rows.Scan(&event.UserID, &event.PeriodKey, &event.Action, &event.Result, &event.Generation, &event.ErrorSummary, &created); err != nil {
			return nil, err
		}
		event.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse database time: %w", err)
	}
	return parsed, nil
}
