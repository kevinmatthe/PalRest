package store

import (
	"context"
	"fmt"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

type AnalyticsInterval struct {
	Start         time.Time
	End           time.Time
	OnlineUserIDs []string
	LocalDate     string
}

type AnalyticsObservation struct {
	At            time.Time
	LocalDate     string
	Players       []domain.Player
	JoinedUserIDs []string
	LeftUserIDs   []string
	Intervals     []AnalyticsInterval
}

type RankingRow struct {
	UserID, Name string
	Observed     time.Duration
}
type ConcurrencyBucket struct {
	Start         time.Time
	Average       *float64
	Max           *int
	MaxObservedAt *time.Time
	Coverage      float64
}
type DailyActivity struct {
	Date     string
	Observed time.Duration
}

func validateDateRange(start, end string) error {
	if err := validateAnalyticsLocalDate(start); err != nil {
		return err
	}
	if err := validateAnalyticsLocalDate(end); err != nil {
		return err
	}
	if start >= end {
		return fmt.Errorf("start date must be before end date")
	}
	return nil
}

func (r *Repository) Ranking(ctx context.Context, startDate, endDate string) ([]RankingRow, error) {
	if err := validateDateRange(startDate, endDate); err != nil {
		return nil, fmt.Errorf("ranking: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT p.user_id,p.name,SUM(d.observed_ms) total FROM player_daily_stats d JOIN players p ON p.user_id=d.user_id WHERE d.local_date>=? AND d.local_date<? GROUP BY p.user_id,p.name ORDER BY total DESC,p.name COLLATE NOCASE ASC,p.user_id ASC`, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("query ranking: %w", err)
	}
	defer rows.Close()
	out := make([]RankingRow, 0)
	for rows.Next() {
		var x RankingRow
		var ms int64
		if err := rows.Scan(&x.UserID, &x.Name, &ms); err != nil {
			return nil, fmt.Errorf("scan ranking: %w", err)
		}
		x.Observed = time.Duration(ms) * time.Millisecond
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ranking: %w", err)
	}
	return out, nil
}

func (r *Repository) Concurrency(ctx context.Context, start, end time.Time) ([]ConcurrencyBucket, error) {
	if start.IsZero() || end.IsZero() || !start.Before(end) || start.Location() != time.UTC || end.Location() != time.UTC {
		return nil, fmt.Errorf("concurrency: UTC start must be before UTC end")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT bucket_start,weighted_count_ms,observed_ms,max_count,max_observed_at FROM concurrency_buckets WHERE bucket_start>=? AND bucket_start<? ORDER BY bucket_start`, formatTime(start), formatTime(end))
	if err != nil {
		return nil, fmt.Errorf("query concurrency: %w", err)
	}
	defer rows.Close()
	out := make([]ConcurrencyBucket, 0)
	for rows.Next() {
		var x ConcurrencyBucket
		var bs, peak string
		var weighted, observed int64
		var maximum int
		if err := rows.Scan(&bs, &weighted, &observed, &maximum, &peak); err != nil {
			return nil, fmt.Errorf("scan concurrency: %w", err)
		}
		var err error
		x.Start, err = parseTime(bs)
		if err != nil {
			return nil, fmt.Errorf("parse concurrency bucket start: %w", err)
		}
		if observed > 0 {
			avg := float64(weighted) / float64(observed)
			x.Average = &avg
			x.Coverage = float64(observed) / 300000
			if x.Coverage > 1 {
				x.Coverage = 1
			}
			x.Max = &maximum
			p, err := parseTime(peak)
			if err != nil {
				return nil, fmt.Errorf("parse concurrency peak: %w", err)
			}
			x.MaxObservedAt = &p
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate concurrency: %w", err)
	}
	return out, nil
}

func (r *Repository) PlayerDailyActivity(ctx context.Context, userID, startDate, endDate string) ([]DailyActivity, error) {
	if userID == "" {
		return nil, fmt.Errorf("player daily activity: user ID is empty")
	}
	if err := validateDateRange(startDate, endDate); err != nil {
		return nil, fmt.Errorf("player daily activity: %w", err)
	}
	var exists int
	if err := r.db.QueryRowContext(ctx, `SELECT 1 FROM players WHERE user_id=?`, userID).Scan(&exists); err != nil {
		return nil, ErrNotFound
	}
	rows, err := r.db.QueryContext(ctx, `SELECT local_date,observed_ms FROM player_daily_stats WHERE user_id=? AND local_date>=? AND local_date<? ORDER BY local_date`, userID, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("query player daily activity: %w", err)
	}
	defer rows.Close()
	out := make([]DailyActivity, 0)
	for rows.Next() {
		var x DailyActivity
		var ms int64
		if err := rows.Scan(&x.Date, &ms); err != nil {
			return nil, fmt.Errorf("scan player daily activity: %w", err)
		}
		x.Observed = time.Duration(ms) * time.Millisecond
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate player daily activity: %w", err)
	}
	return out, nil
}

func (r *Repository) CleanupAnalytics(ctx context.Context, cutoff time.Time, cutoffDate string, batchSize int) error {
	if cutoff.IsZero() {
		return fmt.Errorf("cleanup analytics: cutoff is zero")
	}
	if err := validateAnalyticsLocalDate(cutoffDate); err != nil {
		return fmt.Errorf("cleanup analytics: %w", err)
	}
	if batchSize <= 0 {
		return fmt.Errorf("cleanup analytics: batch size must be positive")
	}
	return r.WithTx(ctx, func(tx *Tx) error {
		for _, d := range []struct {
			q string
			a any
		}{{`DELETE FROM player_sessions WHERE rowid IN (SELECT rowid FROM player_sessions WHERE ended_at IS NOT NULL AND ended_at<? LIMIT ?)`, formatTime(cutoff)}, {`DELETE FROM concurrency_buckets WHERE rowid IN (SELECT rowid FROM concurrency_buckets WHERE bucket_start<? LIMIT ?)`, formatTime(cutoff)}, {`DELETE FROM player_daily_stats WHERE rowid IN (SELECT rowid FROM player_daily_stats WHERE local_date<? LIMIT ?)`, cutoffDate}} {
			if _, err := tx.tx.ExecContext(ctx, d.q, d.a, batchSize); err != nil {
				return fmt.Errorf("cleanup analytics batch: %w", err)
			}
		}
		return nil
	})
}

// OpenAnalyticsPlayers returns the players with open analytics sessions and the
// earliest last-observed time shared by the recovered baseline.
func (r *Repository) OpenAnalyticsPlayers(ctx context.Context) ([]domain.Player, time.Time, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT p.user_id, p.player_id, p.name, p.account_name, p.last_online, s.last_observed_at
FROM player_sessions s
JOIN players p ON p.user_id = s.user_id
WHERE s.ended_at IS NULL
ORDER BY p.user_id`)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("query open analytics players: %w", err)
	}
	defer rows.Close()
	var players []domain.Player
	var baseline time.Time
	for rows.Next() {
		var player domain.Player
		var lastOnline, lastObserved string
		if err := rows.Scan(&player.UserID, &player.PlayerID, &player.Name, &player.AccountName, &lastOnline, &lastObserved); err != nil {
			return nil, time.Time{}, fmt.Errorf("scan open analytics player: %w", err)
		}
		player.LastOnline, err = parseTime(lastOnline)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("parse open analytics player %q last online: %w", player.UserID, err)
		}
		observedAt, err := parseTime(lastObserved)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("parse open analytics player %q baseline: %w", player.UserID, err)
		}
		if baseline.IsZero() || observedAt.Before(baseline) {
			baseline = observedAt
		}
		players = append(players, player)
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, fmt.Errorf("iterate open analytics players: %w", err)
	}
	return players, baseline, nil
}

func (r *Repository) RecordAnalyticsObservation(ctx context.Context, observation AnalyticsObservation) error {
	if observation.At.IsZero() {
		return fmt.Errorf("record analytics observation: observation time is zero")
	}
	if err := validateAnalyticsLocalDate(observation.LocalDate); err != nil {
		return fmt.Errorf("record analytics observation: %w", err)
	}
	joined, err := validateUniqueAnalyticsUserIDs("joined", observation.JoinedUserIDs)
	if err != nil {
		return fmt.Errorf("record analytics observation: %w", err)
	}
	if _, err := validateUniqueAnalyticsUserIDs("left", observation.LeftUserIDs); err != nil {
		return fmt.Errorf("record analytics observation: %w", err)
	}
	for _, userID := range observation.LeftUserIDs {
		if _, ok := joined[userID]; ok {
			return fmt.Errorf("record analytics observation: user ID %q cannot be both joined and left", userID)
		}
	}
	for i, interval := range observation.Intervals {
		if err := validateAnalyticsInterval(interval); err != nil {
			return fmt.Errorf("record analytics observation: interval %d: %w", i, err)
		}
	}

	return r.WithTx(ctx, func(tx *Tx) error {
		for _, player := range observation.Players {
			if err := tx.UpsertPlayer(player, observation.At); err != nil {
				return fmt.Errorf("record analytics player %q: %w", player.UserID, err)
			}
		}
		for _, userID := range observation.JoinedUserIDs {
			if _, err := tx.tx.ExecContext(ctx, `
INSERT INTO player_sessions(user_id, started_at, last_observed_at)
VALUES(?, ?, ?)`, userID, formatTime(observation.At), formatTime(observation.At)); err != nil {
				return fmt.Errorf("open analytics session for %q: %w", userID, err)
			}
		}
		for _, player := range observation.Players {
			if _, err := tx.tx.ExecContext(ctx, `
UPDATE player_sessions SET last_observed_at=? WHERE user_id=? AND ended_at IS NULL`, formatTime(observation.At), player.UserID); err != nil {
				return fmt.Errorf("update analytics session for %q: %w", player.UserID, err)
			}
		}
		for _, userID := range observation.LeftUserIDs {
			if _, err := tx.tx.ExecContext(ctx, `
UPDATE player_sessions
SET ended_at=?, last_observed_at=?, close_reason='observed_offline'
WHERE user_id=? AND ended_at IS NULL`, formatTime(observation.At), formatTime(observation.At), userID); err != nil {
				return fmt.Errorf("close analytics session for %q: %w", userID, err)
			}
		}

		for i, interval := range observation.Intervals {
			if err := recordAnalyticsInterval(ctx, tx, interval); err != nil {
				return fmt.Errorf("record analytics interval %d: %w", i, err)
			}
		}

		for _, userID := range observation.JoinedUserIDs {
			if err := upsertDailyAnalytics(ctx, tx, userID, observation.LocalDate, 0, observation.At, observation.At, 1); err != nil {
				return fmt.Errorf("count analytics session for %q: %w", userID, err)
			}
		}
		return nil
	})
}

func validateAnalyticsInterval(interval AnalyticsInterval) error {
	if !interval.Start.Before(interval.End) {
		return fmt.Errorf("start must be before end")
	}
	if interval.End.Sub(interval.Start).Milliseconds() <= 0 {
		return fmt.Errorf("duration must include at least one whole millisecond")
	}
	if err := validateAnalyticsLocalDate(interval.LocalDate); err != nil {
		return err
	}
	if _, err := validateUniqueAnalyticsUserIDs("online", interval.OnlineUserIDs); err != nil {
		return err
	}
	startBucket := interval.Start.UTC().Truncate(5 * time.Minute)
	endBucket := interval.End.Add(-time.Nanosecond).UTC().Truncate(5 * time.Minute)
	if !startBucket.Equal(endBucket) {
		return fmt.Errorf("interval crosses UTC 5-minute bucket boundary")
	}
	return nil
}

func validateAnalyticsLocalDate(localDate string) error {
	parsed, err := time.Parse("2006-01-02", localDate)
	if err != nil || parsed.Format("2006-01-02") != localDate {
		return fmt.Errorf("local date %q must be a valid YYYY-MM-DD date", localDate)
	}
	return nil
}

func validateUniqueAnalyticsUserIDs(kind string, userIDs []string) (map[string]struct{}, error) {
	seen := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID == "" {
			return nil, fmt.Errorf("%s user ID cannot be empty", kind)
		}
		if _, ok := seen[userID]; ok {
			return nil, fmt.Errorf("%s user ID %q is duplicated", kind, userID)
		}
		seen[userID] = struct{}{}
	}
	return seen, nil
}

func recordAnalyticsInterval(ctx context.Context, tx *Tx, interval AnalyticsInterval) error {
	durationMS := interval.End.Sub(interval.Start).Milliseconds()
	count := len(interval.OnlineUserIDs)
	bucketStart := interval.Start.UTC().Truncate(5 * time.Minute)
	if _, err := tx.tx.ExecContext(ctx, `
INSERT INTO concurrency_buckets(bucket_start, weighted_count_ms, observed_ms, max_count, max_observed_at)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(bucket_start) DO UPDATE SET
    weighted_count_ms=concurrency_buckets.weighted_count_ms + excluded.weighted_count_ms,
    observed_ms=concurrency_buckets.observed_ms + excluded.observed_ms,
    max_count=CASE WHEN excluded.max_count > concurrency_buckets.max_count THEN excluded.max_count ELSE concurrency_buckets.max_count END,
    max_observed_at=CASE WHEN excluded.max_count > concurrency_buckets.max_count THEN excluded.max_observed_at ELSE concurrency_buckets.max_observed_at END`,
		formatTime(bucketStart), int64(count)*durationMS, durationMS, count, formatTime(interval.Start)); err != nil {
		return fmt.Errorf("upsert concurrency bucket: %w", err)
	}
	for _, userID := range interval.OnlineUserIDs {
		if err := upsertDailyAnalytics(ctx, tx, userID, interval.LocalDate, durationMS, interval.Start, interval.End, 0); err != nil {
			return err
		}
	}
	return nil
}

func upsertDailyAnalytics(ctx context.Context, tx *Tx, userID, localDate string, observedMS int64, firstObservedAt, lastObservedAt time.Time, sessionCount int) error {
	if _, err := tx.tx.ExecContext(ctx, `
INSERT INTO player_daily_stats(user_id, local_date, observed_ms, first_observed_at, last_observed_at, session_count)
VALUES(?, ?, ?, ?, ?, ?)
ON CONFLICT(user_id, local_date) DO UPDATE SET
    observed_ms=player_daily_stats.observed_ms + excluded.observed_ms,
    first_observed_at=MIN(player_daily_stats.first_observed_at, excluded.first_observed_at),
    last_observed_at=MAX(player_daily_stats.last_observed_at, excluded.last_observed_at),
    session_count=player_daily_stats.session_count + excluded.session_count`,
		userID, localDate, observedMS, formatTime(firstObservedAt), formatTime(lastObservedAt), sessionCount); err != nil {
		return fmt.Errorf("upsert daily analytics for %q: %w", userID, err)
	}
	return nil
}
