package store

import (
	"context"
	"database/sql"
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
	Players       []domain.Player
	JoinedUserIDs []string
	LeftUserIDs   []string
	Intervals     []AnalyticsInterval
}

func (r *Repository) RecordAnalyticsObservation(ctx context.Context, observation AnalyticsObservation) error {
	if observation.At.IsZero() {
		return fmt.Errorf("record analytics observation: observation time is zero")
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
			for _, userID := range interval.OnlineUserIDs {
				var exists int
				err := tx.tx.QueryRowContext(ctx, `SELECT 1 FROM players WHERE user_id=?`, userID).Scan(&exists)
				if err == sql.ErrNoRows {
					return fmt.Errorf("analytics interval %d references unknown online player %q", i, userID)
				}
				if err != nil {
					return fmt.Errorf("check analytics interval %d player %q: %w", i, userID, err)
				}
			}
			if err := recordAnalyticsInterval(ctx, tx, interval); err != nil {
				return fmt.Errorf("record analytics interval %d: %w", i, err)
			}
		}

		joinDate := observation.At.Format("2006-01-02")
		for _, userID := range observation.JoinedUserIDs {
			if err := upsertDailyAnalytics(ctx, tx, userID, joinDate, 0, observation.At, observation.At, 1); err != nil {
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
	parsed, err := time.Parse("2006-01-02", interval.LocalDate)
	if err != nil || parsed.Format("2006-01-02") != interval.LocalDate {
		return fmt.Errorf("local date %q must be a valid YYYY-MM-DD date", interval.LocalDate)
	}
	startBucket := interval.Start.UTC().Truncate(5 * time.Minute)
	endBucket := interval.End.Add(-time.Nanosecond).UTC().Truncate(5 * time.Minute)
	if !startBucket.Equal(endBucket) {
		return fmt.Errorf("interval crosses UTC 5-minute bucket boundary")
	}
	return nil
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
