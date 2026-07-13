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
var ErrObservationConflict = errors.New("server observation baseline conflict")

type Repository struct {
	db *sql.DB
	// beforeSensitiveTimelineAudit is a narrow deterministic test seam for
	// failures between a completed sensitive read and its audit write.
	beforeSensitiveTimelineAudit func()
}

type Tx struct {
	tx *sql.Tx
}

type EnforcementEvent struct {
	UserID         string
	PeriodKey      string
	Action         string
	Result         string
	PolicyRevision string
	Generation     int64
	ErrorSummary   string
	CreatedAt      time.Time
}

type WarningEvent struct {
	UserID      string
	PeriodKey   string
	Threshold   time.Duration
	Status      string
	Attempts    int
	NextAttempt time.Time
	LastError   string
	UpdatedAt   time.Time
}

type EnforcementRetry struct {
	Attempts    int
	LastAttempt time.Time
}

type PolicyState struct {
	UserID              string
	PolicyRevision      string
	Strategy            string
	WindowStart         time.Time
	Used                time.Duration
	CooldownUntil       time.Time
	Credit              time.Duration
	LastCreditAt        time.Time
	LastCreditRecovered time.Duration
	UpdatedAt           time.Time
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
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return fmt.Errorf("read migration version: %w", err)
	}
	if version < 1 {
		if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
			return fmt.Errorf("apply migration 1: %w", err)
		}
		if err := recordMigration(ctx, tx, 1); err != nil {
			return err
		}
	}
	if version < 2 {
		hasRevision, err := columnExists(ctx, tx, "enforcement_events", "policy_revision")
		if err != nil {
			return err
		}
		if !hasRevision {
			if _, err := tx.ExecContext(ctx, schemaV2); err != nil {
				return fmt.Errorf("apply migration 2: %w", err)
			}
		} else if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS enforcement_policy_lookup ON enforcement_events(user_id, period_key, policy_revision, created_at)`); err != nil {
			return fmt.Errorf("apply migration 2 index: %w", err)
		}
		if err := recordMigration(ctx, tx, 2); err != nil {
			return err
		}
	}
	if version < 3 {
		if _, err := tx.ExecContext(ctx, schemaV3); err != nil {
			return fmt.Errorf("apply migration 3: %w", err)
		}
		if err := recordMigration(ctx, tx, 3); err != nil {
			return err
		}
	}
	if version < 4 {
		if _, err := tx.ExecContext(ctx, schemaV4); err != nil {
			return fmt.Errorf("apply migration 4: %w", err)
		}
		if err := recordMigration(ctx, tx, 4); err != nil {
			return err
		}
	}
	if version < 5 {
		hasRecovery, err := columnExists(ctx, tx, "policy_states", "last_credit_recovered_ms")
		if err != nil {
			return err
		}
		if !hasRecovery {
			if _, err := tx.ExecContext(ctx, schemaV5); err != nil {
				return fmt.Errorf("apply migration 5: %w", err)
			}
		}
		if err := recordMigration(ctx, tx, 5); err != nil {
			return err
		}
	}
	if version < 6 {
		if _, err := tx.ExecContext(ctx, schemaV6); err != nil {
			return fmt.Errorf("apply migration 6: %w", err)
		}
		if err := recordMigration(ctx, tx, 6); err != nil {
			return err
		}
	}
	if version < 7 {
		if _, err := tx.ExecContext(ctx, schemaV7); err != nil {
			return fmt.Errorf("apply migration 7: %w", err)
		}
		if err := recordMigration(ctx, tx, 7); err != nil {
			return err
		}
	}
	if version < 8 {
		if _, err := tx.ExecContext(ctx, schemaV8); err != nil {
			return fmt.Errorf("apply migration 8: %w", err)
		}
		if err := recordMigration(ctx, tx, 8); err != nil {
			return err
		}
	}
	if version < 9 {
		if _, err := tx.ExecContext(ctx, schemaV9); err != nil {
			return fmt.Errorf("apply migration 9: %w", err)
		}
		if err := recordMigration(ctx, tx, 9); err != nil {
			return err
		}
	}
	if version < 10 {
		if _, err := tx.ExecContext(ctx, schemaV10); err != nil {
			return fmt.Errorf("apply migration 10: %w", err)
		}
		if err := recordMigration(ctx, tx, 10); err != nil {
			return err
		}
	}
	if version < 11 {
		if _, err := tx.ExecContext(ctx, schemaV11); err != nil {
			return fmt.Errorf("apply migration 11: %w", err)
		}
		if err := recordMigration(ctx, tx, 11); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func recordMigration(ctx context.Context, tx *sql.Tx, version int) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, version, formatTime(time.Now())); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}
	return nil
}

func columnExists(ctx context.Context, tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
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

func (r *Repository) PolicyDocument(ctx context.Context) (string, error) {
	var policyYAML string
	err := r.db.QueryRowContext(ctx, `SELECT policy_yaml FROM policy_documents WHERE id=1`).Scan(&policyYAML)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read policy document: %w", err)
	}
	return policyYAML, nil
}

func (r *Repository) UpsertPolicyDocument(ctx context.Context, policyYAML string, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO policy_documents(id, policy_yaml, updated_at)
VALUES(1, ?, ?)
ON CONFLICT(id) DO UPDATE SET policy_yaml=excluded.policy_yaml, updated_at=excluded.updated_at`, policyYAML, formatTime(now))
	if err != nil {
		return fmt.Errorf("upsert policy document: %w", err)
	}
	return nil
}

func (r *Repository) ResetPlayerPolicyState(ctx context.Context, userID string) error {
	return r.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.tx.ExecContext(ctx, `DELETE FROM usage_periods WHERE user_id=?`, userID); err != nil {
			return fmt.Errorf("delete usage periods: %w", err)
		}
		if _, err := tx.tx.ExecContext(ctx, `DELETE FROM warning_events WHERE user_id=?`, userID); err != nil {
			return fmt.Errorf("delete warning events: %w", err)
		}
		if _, err := tx.tx.ExecContext(ctx, `DELETE FROM enforcement_events WHERE user_id=?`, userID); err != nil {
			return fmt.Errorf("delete enforcement events: %w", err)
		}
		if _, err := tx.tx.ExecContext(ctx, `DELETE FROM policy_states WHERE user_id=?`, userID); err != nil {
			return fmt.Errorf("delete policy states: %w", err)
		}
		return nil
	})
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

func (tx *Tx) Usage(userID, periodKey string) (time.Duration, error) {
	var usedMS int64
	err := tx.tx.QueryRow(`SELECT used_ms FROM usage_periods WHERE user_id=? AND period_key=?`, userID, periodKey).Scan(&usedMS)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read transaction usage: %w", err)
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

func (tx *Tx) Warning(userID, periodKey string, threshold time.Duration) (WarningEvent, error) {
	var event WarningEvent
	var thresholdMS int64
	var nextAttempt sql.NullString
	var updated string
	err := tx.tx.QueryRow(`
SELECT user_id, period_key, threshold_ms, status, attempts, next_attempt, last_error, updated_at
FROM warning_events WHERE user_id=? AND period_key=? AND threshold_ms=?`, userID, periodKey, threshold.Milliseconds()).
		Scan(&event.UserID, &event.PeriodKey, &thresholdMS, &event.Status, &event.Attempts, &nextAttempt, &event.LastError, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return WarningEvent{}, ErrNotFound
	}
	if err != nil {
		return WarningEvent{}, fmt.Errorf("read warning: %w", err)
	}
	event.Threshold = time.Duration(thresholdMS) * time.Millisecond
	event.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return WarningEvent{}, err
	}
	if nextAttempt.Valid {
		event.NextAttempt, err = parseTime(nextAttempt.String)
	}
	return event, err
}

func (tx *Tx) UpdateWarningResult(userID, periodKey string, threshold time.Duration, status, errorSummary string, nextAttempt, now time.Time) error {
	var next any
	if !nextAttempt.IsZero() {
		next = formatTime(nextAttempt)
	}
	result, err := tx.tx.Exec(`
UPDATE warning_events SET status=?, attempts=attempts+1, next_attempt=?, last_error=?, updated_at=?
WHERE user_id=? AND period_key=? AND threshold_ms=?`, status, next, errorSummary, formatTime(now), userID, periodKey, threshold.Milliseconds())
	if err != nil {
		return fmt.Errorf("update warning result: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrNotFound
	}
	return nil
}

func (tx *Tx) AppendEnforcement(event EnforcementEvent) error {
	_, err := tx.tx.Exec(`
INSERT INTO enforcement_events(user_id, period_key, action, result, policy_revision, generation, error_summary, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, event.UserID, event.PeriodKey, event.Action, event.Result, event.PolicyRevision, event.Generation, event.ErrorSummary, formatTime(event.CreatedAt))
	if err != nil {
		return fmt.Errorf("append enforcement: %w", err)
	}
	return nil
}

// AppendActivityEvent adds an immutable business event inside the caller's
// transaction. The boolean is false when the same stable event ID was already
// committed, allowing action-result replays to remain idempotent.
func (tx *Tx) AppendActivityEvent(event ActivityEvent) (bool, error) {
	if err := validateActivityEvent(event); err != nil {
		return false, fmt.Errorf("append activity event: %w", err)
	}
	result, err := tx.tx.Exec(`
INSERT INTO activity_events(
    id,event_type,subject_type,subject_id,occurred_at,observed_at,source,source_ref,
    correlation_id,confidence,schema_version,payload_json
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, event.ID, event.EventType,
		event.SubjectType, event.SubjectID, formatObservationTime(event.OccurredAt),
		formatObservationTime(event.ObservedAt), event.Source, event.SourceRef,
		event.CorrelationID, event.Confidence, event.SchemaVersion, event.PayloadJSON)
	if err != nil {
		return false, fmt.Errorf("append activity event %q: %w", event.ID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read activity event insert result: %w", err)
	}
	if rows == 1 {
		return true, nil
	}
	stored, err := readStoredEvent(context.Background(), tx.tx, event.ID)
	if err != nil {
		return false, err
	}
	if !activityEventsEqual(*stored, event) {
		return false, fmt.Errorf("activity event ID %q conflicts with different content", event.ID)
	}
	return false, nil
}

func (tx *Tx) EnforcementRetry(userID, periodKey, policyRevision string) (EnforcementRetry, error) {
	rows, err := tx.tx.Query(`
SELECT result, created_at FROM enforcement_events
WHERE user_id=? AND period_key=? AND policy_revision=? AND action='kick'
ORDER BY id DESC`, userID, periodKey, policyRevision)
	if err != nil {
		return EnforcementRetry{}, fmt.Errorf("query enforcement retry: %w", err)
	}
	defer rows.Close()
	var state EnforcementRetry
	for rows.Next() {
		var result, created string
		if err := rows.Scan(&result, &created); err != nil {
			return EnforcementRetry{}, err
		}
		if state.Attempts == 0 {
			if result != "failure" {
				return EnforcementRetry{}, nil
			}
			state.LastAttempt, err = parseTime(created)
			if err != nil {
				return EnforcementRetry{}, err
			}
		}
		if result != "failure" {
			break
		}
		state.Attempts++
	}
	return state, rows.Err()
}

func (tx *Tx) PolicyState(userID, policyRevision string) (PolicyState, error) {
	var state PolicyState
	var windowStart, cooldownUntil, lastCreditAt sql.NullString
	var updated string
	var usedMS, creditMS, lastCreditRecoveredMS int64
	err := tx.tx.QueryRow(`
SELECT user_id, policy_revision, strategy, window_start, used_ms, cooldown_until, credit_ms, last_credit_at, last_credit_recovered_ms, updated_at
FROM policy_states WHERE user_id=? AND policy_revision=?`, userID, policyRevision).
		Scan(&state.UserID, &state.PolicyRevision, &state.Strategy, &windowStart, &usedMS, &cooldownUntil, &creditMS, &lastCreditAt, &lastCreditRecoveredMS, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return PolicyState{}, ErrNotFound
	}
	if err != nil {
		return PolicyState{}, fmt.Errorf("read policy state: %w", err)
	}
	var parseErr error
	if windowStart.Valid {
		state.WindowStart, parseErr = parseTime(windowStart.String)
		if parseErr != nil {
			return PolicyState{}, parseErr
		}
	}
	if cooldownUntil.Valid {
		state.CooldownUntil, parseErr = parseTime(cooldownUntil.String)
		if parseErr != nil {
			return PolicyState{}, parseErr
		}
	}
	if lastCreditAt.Valid {
		state.LastCreditAt, parseErr = parseTime(lastCreditAt.String)
		if parseErr != nil {
			return PolicyState{}, parseErr
		}
	}
	state.UpdatedAt, parseErr = parseTime(updated)
	if parseErr != nil {
		return PolicyState{}, parseErr
	}
	state.Used = time.Duration(usedMS) * time.Millisecond
	state.Credit = time.Duration(creditMS) * time.Millisecond
	state.LastCreditRecovered = time.Duration(lastCreditRecoveredMS) * time.Millisecond
	return state, nil
}

func (tx *Tx) UpsertPolicyState(state PolicyState) error {
	_, err := tx.tx.Exec(`
INSERT INTO policy_states(user_id, policy_revision, strategy, window_start, used_ms, cooldown_until, credit_ms, last_credit_at, last_credit_recovered_ms, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(user_id, policy_revision) DO UPDATE SET
    strategy=excluded.strategy,
    window_start=excluded.window_start,
    used_ms=excluded.used_ms,
    cooldown_until=excluded.cooldown_until,
    credit_ms=excluded.credit_ms,
    last_credit_at=excluded.last_credit_at,
    last_credit_recovered_ms=excluded.last_credit_recovered_ms,
    updated_at=excluded.updated_at`,
		state.UserID, state.PolicyRevision, state.Strategy, nullableTime(state.WindowStart), state.Used.Milliseconds(),
		nullableTime(state.CooldownUntil), state.Credit.Milliseconds(), nullableTime(state.LastCreditAt), state.LastCreditRecovered.Milliseconds(), formatTime(state.UpdatedAt))
	if err != nil {
		return fmt.Errorf("upsert policy state: %w", err)
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

func (r *Repository) Players(ctx context.Context) ([]domain.Player, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT user_id, player_id, name, account_name, last_online FROM players ORDER BY last_online DESC, user_id`)
	if err != nil {
		return nil, fmt.Errorf("query players: %w", err)
	}
	defer rows.Close()
	var players []domain.Player
	for rows.Next() {
		var player domain.Player
		var lastOnline string
		if err := rows.Scan(&player.UserID, &player.PlayerID, &player.Name, &player.AccountName, &lastOnline); err != nil {
			return nil, err
		}
		player.LastOnline, err = parseTime(lastOnline)
		if err != nil {
			return nil, err
		}
		players = append(players, player)
	}
	return players, rows.Err()
}

func (r *Repository) EnforcementEvents(ctx context.Context, userID, periodKey string) ([]EnforcementEvent, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT user_id, period_key, action, result, policy_revision, generation, error_summary, created_at
FROM enforcement_events WHERE user_id=? AND period_key=? ORDER BY id`, userID, periodKey)
	if err != nil {
		return nil, fmt.Errorf("query enforcement events: %w", err)
	}
	defer rows.Close()
	var events []EnforcementEvent
	for rows.Next() {
		var event EnforcementEvent
		var created string
		if err := rows.Scan(&event.UserID, &event.PeriodKey, &event.Action, &event.Result, &event.PolicyRevision, &event.Generation, &event.ErrorSummary, &created); err != nil {
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

func (r *Repository) EnforcementEventsForPolicy(ctx context.Context, userID, periodKey, policyRevision string) ([]EnforcementEvent, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT user_id, period_key, action, result, policy_revision, generation, error_summary, created_at
FROM enforcement_events WHERE user_id=? AND period_key=? AND policy_revision=? ORDER BY id`, userID, periodKey, policyRevision)
	if err != nil {
		return nil, fmt.Errorf("query policy enforcement events: %w", err)
	}
	defer rows.Close()
	var events []EnforcementEvent
	for rows.Next() {
		var event EnforcementEvent
		var created string
		if err := rows.Scan(&event.UserID, &event.PeriodKey, &event.Action, &event.Result, &event.PolicyRevision, &event.Generation, &event.ErrorSummary, &created); err != nil {
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

func (r *Repository) WarningEvents(ctx context.Context, userID, periodKey string) ([]WarningEvent, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT user_id, period_key, threshold_ms, status, attempts, next_attempt, last_error, updated_at
FROM warning_events WHERE user_id=? AND period_key=? ORDER BY threshold_ms DESC`, userID, periodKey)
	if err != nil {
		return nil, fmt.Errorf("query warning events: %w", err)
	}
	defer rows.Close()
	var events []WarningEvent
	for rows.Next() {
		var event WarningEvent
		var thresholdMS int64
		var nextAttempt sql.NullString
		var updated string
		if err := rows.Scan(&event.UserID, &event.PeriodKey, &thresholdMS, &event.Status, &event.Attempts, &nextAttempt, &event.LastError, &updated); err != nil {
			return nil, err
		}
		event.Threshold = time.Duration(thresholdMS) * time.Millisecond
		event.UpdatedAt, err = parseTime(updated)
		if err != nil {
			return nil, err
		}
		if nextAttempt.Valid {
			event.NextAttempt, err = parseTime(nextAttempt.String)
			if err != nil {
				return nil, err
			}
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatTime(value)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse database time: %w", err)
	}
	return parsed, nil
}
