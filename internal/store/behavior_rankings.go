package store

import (
	"context"
	"fmt"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/behavior"
)

// ListUsersWithTrajectories returns distinct user IDs that have samples in [start, end).
func (r *Repository) ListUsersWithTrajectories(ctx context.Context, start, end time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT user_id FROM trajectory_samples
WHERE observed_at>=? AND observed_at<?
GROUP BY user_id
HAVING COUNT(*)>=2
ORDER BY MAX(observed_at) DESC
LIMIT ?`, formatObservationTime(start), formatObservationTime(end), limit)
	if err != nil {
		return nil, fmt.Errorf("list users with trajectories: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan trajectory user: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListTrajectoryPointsAsc returns up to limit samples ascending for behavior analysis.
func (r *Repository) ListTrajectoryPointsAsc(ctx context.Context, userID string, start, end time.Time, limit int) ([]behavior.Point, error) {
	if limit <= 0 {
		limit = behavior.MaxSamples
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT segment_id,observed_at,x,y
FROM trajectory_samples
WHERE user_id=? AND observed_at>=? AND observed_at<?
ORDER BY observed_at ASC, id ASC
LIMIT ?`, userID, formatObservationTime(start), formatObservationTime(end), limit)
	if err != nil {
		return nil, fmt.Errorf("list trajectory points: %w", err)
	}
	defer rows.Close()
	out := make([]behavior.Point, 0)
	for rows.Next() {
		var segmentID, observedAt string
		var p behavior.Point
		if err := rows.Scan(&segmentID, &observedAt, &p.X, &p.Y); err != nil {
			return nil, fmt.Errorf("scan trajectory point: %w", err)
		}
		t, err := parseTime(observedAt)
		if err != nil {
			return nil, fmt.Errorf("parse trajectory point time: %w", err)
		}
		p.ObservedAt = t
		p.SegmentID = segmentID
		out = append(out, p)
	}
	return out, rows.Err()
}

// PlayerDisplayNames maps user_id -> best effort display name.
func (r *Repository) PlayerDisplayNames(ctx context.Context, userIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	for _, id := range userIDs {
		var name, account string
		err := r.db.QueryRowContext(ctx, `SELECT COALESCE(name,''), COALESCE(account_name,'') FROM players WHERE user_id=?`, id).Scan(&name, &account)
		if err != nil {
			out[id] = id
			continue
		}
		if name != "" {
			out[id] = name
		} else if account != "" {
			out[id] = account
		} else {
			out[id] = id
		}
	}
	return out, nil
}
