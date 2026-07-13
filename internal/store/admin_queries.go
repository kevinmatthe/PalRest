package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

type ServerMetricSample struct {
	ObservedAt time.Time            `json:"observed_at"`
	Metrics    domain.ServerMetrics `json:"metrics"`
}

type ServerDocumentOccurrence struct {
	Kind        string    `json:"kind"`
	ObservedAt  time.Time `json:"observed_at"`
	ContentHash string    `json:"content_hash"`
	Canonical   []byte    `json:"-"`
}

func (r *Repository) ReadServerMetrics(ctx context.Context, actor string, start, end time.Time, limit int) ([]ServerMetricSample, error) {
	if err := validateAdminRead(actor, start, end, limit); err != nil {
		return nil, fmt.Errorf("read server metrics: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT observed_at,server_fps,current_player_num,server_frame_time,max_player_num,uptime_seconds,base_camp_num,game_days
FROM server_metric_samples WHERE observed_at>=? AND observed_at<? ORDER BY observed_at LIMIT ?`, formatObservationTime(start), formatObservationTime(end), limit)
	result := make([]ServerMetricSample, 0)
	outcome := "success"
	if err == nil {
		for rows.Next() {
			var sample ServerMetricSample
			var at string
			err = rows.Scan(&at, &sample.Metrics.ServerFPS, &sample.Metrics.CurrentPlayerNum, &sample.Metrics.ServerFrameTime, &sample.Metrics.MaxPlayerNum, &sample.Metrics.UptimeSeconds, &sample.Metrics.BaseCampNum, &sample.Metrics.Days)
			if err != nil {
				break
			}
			sample.ObservedAt, err = parseTime(at)
			if err != nil {
				break
			}
			result = append(result, sample)
		}
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
	}
	if err == nil {
		if len(result) == 0 {
			outcome = "not_found"
		}
		err = insertAdminAudit(ctx, tx, actor, "read_server_metrics", "server", "metrics", &start, &end, outcome)
	}
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		return nil, r.auditQueryError(ctx, actor, "read_server_metrics", "server", "metrics", &start, &end, err)
	}
	if outcome == "not_found" {
		return nil, ErrNotFound
	}
	return result, nil
}

func (r *Repository) ReadServerDocuments(ctx context.Context, actor, kind string, limit int) ([]ServerDocumentOccurrence, error) {
	if strings.TrimSpace(actor) == "" {
		return nil, fmt.Errorf("read server documents: actor is empty")
	}
	if kind != "info" && kind != "settings" {
		return nil, fmt.Errorf("read server documents: unknown kind %q", kind)
	}
	if limit < 1 || limit > 2000 {
		return nil, fmt.Errorf("read server documents: limit must be between 1 and 2000")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT o.kind,o.observed_at,o.content_hash,d.canonical_json
FROM server_document_observations o JOIN server_documents d ON d.kind=o.kind AND d.content_hash=o.content_hash
WHERE o.kind=? ORDER BY o.observed_at,o.content_hash LIMIT ?`, kind, limit)
	result := make([]ServerDocumentOccurrence, 0)
	outcome := "success"
	if err == nil {
		for rows.Next() {
			var item ServerDocumentOccurrence
			var at string
			err = rows.Scan(&item.Kind, &at, &item.ContentHash, &item.Canonical)
			if err != nil {
				break
			}
			item.ObservedAt, err = parseTime(at)
			if err != nil {
				break
			}
			result = append(result, item)
		}
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
	}
	if err == nil {
		if len(result) == 0 {
			outcome = "not_found"
		}
		err = insertAdminAudit(ctx, tx, actor, "read_server_documents", "server", kind, nil, nil, outcome)
	}
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		return nil, r.auditQueryError(ctx, actor, "read_server_documents", "server", kind, nil, nil, err)
	}
	if outcome == "not_found" {
		return nil, ErrNotFound
	}
	return result, nil
}

func validateAdminRead(actor string, start, end time.Time, limit int) error {
	if strings.TrimSpace(actor) == "" {
		return fmt.Errorf("actor is empty")
	}
	if start.IsZero() || end.IsZero() || !start.Before(end) {
		return fmt.Errorf("nonzero start must be before end")
	}
	if limit < 1 || limit > 2000 {
		return fmt.Errorf("limit must be between 1 and 2000")
	}
	return nil
}

func insertAdminAudit(ctx context.Context, tx *sql.Tx, actor, action, subjectType, subjectID string, start, end *time.Time, outcome string) error {
	var rangeStart, rangeEnd any
	if start != nil {
		rangeStart = formatObservationTime(*start)
	}
	if end != nil {
		rangeEnd = formatObservationTime(*end)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO sensitive_access_audit(actor,action,subject_type,subject_id,range_start,range_end,outcome,requested_at) VALUES(?,?,?,?,?,?,?,?)`, actor, action, subjectType, subjectID, rangeStart, rangeEnd, outcome, formatObservationTime(time.Now()))
	return err
}

func (r *Repository) auditQueryError(ctx context.Context, actor, action, subjectType, subjectID string, start, end *time.Time, queryErr error) error {
	auditErr := r.WithTx(ctx, func(tx *Tx) error {
		return insertAdminAudit(ctx, tx.tx, actor, action, subjectType, subjectID, start, end, "error")
	})
	if auditErr != nil {
		return errors.Join(queryErr, auditErr)
	}
	return queryErr
}
