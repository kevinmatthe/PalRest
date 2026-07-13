package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

type ServerMetricObservation struct {
	At      time.Time
	Metrics domain.ServerMetrics
	Event   *ActivityEvent
}

type ServerDocumentObservation struct {
	Kind      string
	At        time.Time
	Canonical []byte
	Hash      string
	Event     *ActivityEvent
}

type ServerDocumentSnapshot struct {
	Kind      string
	At        time.Time
	Canonical []byte
	Hash      string
}

func (r *Repository) RecordServerMetricObservation(ctx context.Context, write ServerMetricObservation) error {
	if err := validateServerMetricObservation(write); err != nil {
		return err
	}
	write.At = write.At.UTC()
	return r.WithTx(ctx, func(tx *Tx) error {
		latestAt, latestMetrics, latestEventID, err := latestServerMetricTx(ctx, tx.tx)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil {
			switch {
			case write.At.Before(latestAt):
				return fmt.Errorf("record server metric observation: time %s is older than latest sample %s", write.At.Format(time.RFC3339Nano), latestAt.Format(time.RFC3339Nano))
			case write.At.Equal(latestAt):
				if latestMetrics != write.Metrics {
					return fmt.Errorf("record server metric observation: sample at %s does not match stored metrics", write.At.Format(time.RFC3339Nano))
				}
				if err := compareOptionalStoredEvent(ctx, tx.tx, latestEventID, write.Event); err != nil {
					return fmt.Errorf("record server metric observation: sample at %s: %w", write.At.Format(time.RFC3339Nano), err)
				}
				return nil
			}
		}

		eventID, err := insertOptionalActivityEvent(ctx, tx.tx, write.Event)
		if err != nil {
			return fmt.Errorf("record server metric observation: %w", err)
		}
		if _, err := tx.tx.ExecContext(ctx, `
INSERT INTO server_metric_samples(
    observed_at,server_fps,current_player_num,server_frame_time,max_player_num,
    uptime_seconds,base_camp_num,game_days,event_id
) VALUES(?,?,?,?,?,?,?,?,?)`, formatObservationTime(write.At), write.Metrics.ServerFPS, write.Metrics.CurrentPlayerNum,
			write.Metrics.ServerFrameTime, write.Metrics.MaxPlayerNum, write.Metrics.UptimeSeconds,
			write.Metrics.BaseCampNum, write.Metrics.Days, eventID); err != nil {
			return fmt.Errorf("record server metric observation: insert sample: %w", err)
		}
		return nil
	})
}

func (r *Repository) LatestServerMetrics(ctx context.Context) (time.Time, domain.ServerMetrics, error) {
	latestAt, metrics, _, err := latestServerMetricDB(ctx, r.db)
	return latestAt, metrics, err
}

func latestServerMetricTx(ctx context.Context, tx *sql.Tx) (time.Time, domain.ServerMetrics, sql.NullString, error) {
	return scanLatestServerMetric(tx.QueryRowContext(ctx, `
SELECT observed_at,server_fps,current_player_num,server_frame_time,max_player_num,
       uptime_seconds,base_camp_num,game_days,event_id
FROM server_metric_samples ORDER BY observed_at DESC LIMIT 1`))
}

func latestServerMetricDB(ctx context.Context, db *sql.DB) (time.Time, domain.ServerMetrics, sql.NullString, error) {
	return scanLatestServerMetric(db.QueryRowContext(ctx, `
SELECT observed_at,server_fps,current_player_num,server_frame_time,max_player_num,
       uptime_seconds,base_camp_num,game_days,event_id
FROM server_metric_samples ORDER BY observed_at DESC LIMIT 1`))
}

type rowScanner interface {
	Scan(...any) error
}

func scanLatestServerMetric(row rowScanner) (time.Time, domain.ServerMetrics, sql.NullString, error) {
	var observedAt string
	var metrics domain.ServerMetrics
	var eventID sql.NullString
	err := row.Scan(&observedAt, &metrics.ServerFPS, &metrics.CurrentPlayerNum, &metrics.ServerFrameTime,
		&metrics.MaxPlayerNum, &metrics.UptimeSeconds, &metrics.BaseCampNum, &metrics.Days, &eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, domain.ServerMetrics{}, sql.NullString{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, domain.ServerMetrics{}, sql.NullString{}, fmt.Errorf("read latest server metrics: %w", err)
	}
	at, err := parseTime(observedAt)
	if err != nil {
		return time.Time{}, domain.ServerMetrics{}, sql.NullString{}, fmt.Errorf("parse latest server metric time: %w", err)
	}
	return at, metrics, eventID, nil
}

func validateServerMetricObservation(write ServerMetricObservation) error {
	if write.At.IsZero() {
		return fmt.Errorf("record server metric observation: observation time is zero")
	}
	if write.Metrics.ServerFPS < 0 || write.Metrics.CurrentPlayerNum < 0 || write.Metrics.MaxPlayerNum < 0 ||
		write.Metrics.UptimeSeconds < 0 || write.Metrics.BaseCampNum < 0 || write.Metrics.Days < 0 {
		return fmt.Errorf("record server metric observation: metric counts and uptime must be nonnegative")
	}
	if math.IsNaN(write.Metrics.ServerFrameTime) || math.IsInf(write.Metrics.ServerFrameTime, 0) || write.Metrics.ServerFrameTime < 0 {
		return fmt.Errorf("record server metric observation: server frame time must be finite and nonnegative")
	}
	if write.Event != nil {
		if err := validateActivityEvent(*write.Event); err != nil {
			return fmt.Errorf("record server metric observation: event: %w", err)
		}
		if !write.Event.OccurredAt.Equal(write.At) || !write.Event.ObservedAt.Equal(write.At) {
			return fmt.Errorf("record server metric observation: event timestamps must match observation time")
		}
	}
	return nil
}

func (r *Repository) RecordServerDocumentObservation(ctx context.Context, write ServerDocumentObservation) (bool, error) {
	if err := validateServerDocumentObservation(write); err != nil {
		return false, err
	}
	write.At = write.At.UTC()
	changed := false
	err := r.WithTx(ctx, func(tx *Tx) error {
		var storedHash string
		var storedEventID sql.NullString
		err := tx.tx.QueryRowContext(ctx, `
SELECT content_hash,event_id FROM server_document_observations WHERE kind=? AND observed_at=?`,
			write.Kind, formatObservationTime(write.At)).Scan(&storedHash, &storedEventID)
		if err == nil {
			if storedHash != write.Hash {
				return fmt.Errorf("record server document observation: occurrence at %s has different hash", write.At.Format(time.RFC3339Nano))
			}
			if err := compareStoredDocumentBlob(ctx, tx.tx, write.Kind, write.Hash, write.Canonical); err != nil {
				return err
			}
			if err := compareOptionalStoredEvent(ctx, tx.tx, storedEventID, write.Event); err != nil {
				return fmt.Errorf("record server document observation: occurrence at %s: %w", write.At.Format(time.RFC3339Nano), err)
			}
			changed = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("record server document observation: read exact occurrence: %w", err)
		}

		var latestAt, latestHash string
		err = tx.tx.QueryRowContext(ctx, `
SELECT observed_at,content_hash FROM server_document_observations
WHERE kind=? ORDER BY observed_at DESC LIMIT 1`, write.Kind).Scan(&latestAt, &latestHash)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("record server document observation: read latest occurrence: %w", err)
		}
		if err == nil {
			parsedLatest, parseErr := parseTime(latestAt)
			if parseErr != nil {
				return fmt.Errorf("record server document observation: parse latest occurrence time: %w", parseErr)
			}
			if !write.At.After(parsedLatest) {
				return fmt.Errorf("record server document observation: time %s is not newer than latest occurrence %s", write.At.Format(time.RFC3339Nano), parsedLatest.Format(time.RFC3339Nano))
			}
			if latestHash == write.Hash {
				if write.Event != nil {
					return fmt.Errorf("record server document observation: unchanged document cannot have a transition event")
				}
				return compareStoredDocumentBlob(ctx, tx.tx, write.Kind, write.Hash, write.Canonical)
			}
		}

		if err := ensureServerDocumentBlob(ctx, tx.tx, write); err != nil {
			return err
		}
		eventID, err := insertOptionalActivityEvent(ctx, tx.tx, write.Event)
		if err != nil {
			return fmt.Errorf("record server document observation: %w", err)
		}
		if _, err := tx.tx.ExecContext(ctx, `
INSERT INTO server_document_observations(kind,observed_at,content_hash,event_id)
VALUES(?,?,?,?)`, write.Kind, formatObservationTime(write.At), write.Hash, eventID); err != nil {
			return fmt.Errorf("record server document observation: insert occurrence: %w", err)
		}
		changed = true
		return nil
	})
	return changed, err
}

func (r *Repository) LatestServerDocument(ctx context.Context, kind string) (ServerDocumentSnapshot, error) {
	if kind != "info" && kind != "settings" {
		return ServerDocumentSnapshot{}, fmt.Errorf("read latest server document: unknown kind %q", kind)
	}
	var snapshot ServerDocumentSnapshot
	var observedAt string
	err := r.db.QueryRowContext(ctx, `
SELECT o.kind,o.observed_at,o.content_hash,d.canonical_json
FROM server_document_observations o
JOIN server_documents d ON d.kind=o.kind AND d.content_hash=o.content_hash
WHERE o.kind=? ORDER BY o.observed_at DESC LIMIT 1`, kind).
		Scan(&snapshot.Kind, &observedAt, &snapshot.Hash, &snapshot.Canonical)
	if errors.Is(err, sql.ErrNoRows) {
		return ServerDocumentSnapshot{}, ErrNotFound
	}
	if err != nil {
		return ServerDocumentSnapshot{}, fmt.Errorf("read latest server document: %w", err)
	}
	snapshot.At, err = parseTime(observedAt)
	if err != nil {
		return ServerDocumentSnapshot{}, fmt.Errorf("read latest server document time: %w", err)
	}
	return snapshot, nil
}

func validateServerDocumentObservation(write ServerDocumentObservation) error {
	if write.Kind != "info" && write.Kind != "settings" {
		return fmt.Errorf("record server document observation: unknown kind %q", write.Kind)
	}
	if write.At.IsZero() {
		return fmt.Errorf("record server document observation: observation time is zero")
	}
	if write.Hash == "" {
		return fmt.Errorf("record server document observation: hash is empty")
	}
	if !validJSONObject(write.Canonical) {
		return fmt.Errorf("record server document observation: canonical content must be a valid JSON object")
	}
	if write.Event != nil {
		if err := validateActivityEvent(*write.Event); err != nil {
			return fmt.Errorf("record server document observation: event: %w", err)
		}
		if !write.Event.OccurredAt.Equal(write.At) || !write.Event.ObservedAt.Equal(write.At) {
			return fmt.Errorf("record server document observation: event timestamps must match observation time")
		}
	}
	return nil
}

func ensureServerDocumentBlob(ctx context.Context, tx *sql.Tx, write ServerDocumentObservation) error {
	var stored []byte
	err := tx.QueryRowContext(ctx, `SELECT canonical_json FROM server_documents WHERE kind=? AND content_hash=?`, write.Kind, write.Hash).Scan(&stored)
	if err == nil {
		if !bytes.Equal(stored, write.Canonical) {
			return fmt.Errorf("record server document observation: hash %q has different canonical content", write.Hash)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("record server document observation: read document blob: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO server_documents(kind,content_hash,observed_at,canonical_json)
VALUES(?,?,?,?)`, write.Kind, write.Hash, formatObservationTime(write.At), string(write.Canonical)); err != nil {
		return fmt.Errorf("record server document observation: insert document blob: %w", err)
	}
	return nil
}

func compareStoredDocumentBlob(ctx context.Context, tx *sql.Tx, kind, hash string, canonical []byte) error {
	var stored []byte
	if err := tx.QueryRowContext(ctx, `SELECT canonical_json FROM server_documents WHERE kind=? AND content_hash=?`, kind, hash).Scan(&stored); err != nil {
		return fmt.Errorf("record server document observation: read stored document blob: %w", err)
	}
	if !bytes.Equal(stored, canonical) {
		return fmt.Errorf("record server document observation: stored canonical content does not match replay")
	}
	return nil
}

func insertOptionalActivityEvent(ctx context.Context, tx *sql.Tx, event *ActivityEvent) (any, error) {
	if event == nil {
		return nil, nil
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO activity_events(
    id,event_type,subject_type,subject_id,occurred_at,observed_at,source,source_ref,
    correlation_id,confidence,schema_version,payload_json
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, event.ID, event.EventType, event.SubjectType, event.SubjectID,
		formatObservationTime(event.OccurredAt), formatObservationTime(event.ObservedAt), event.Source, event.SourceRef,
		event.CorrelationID, event.Confidence, event.SchemaVersion, event.PayloadJSON); err != nil {
		return nil, fmt.Errorf("insert activity event %q: %w", event.ID, err)
	}
	return event.ID, nil
}

func compareOptionalStoredEvent(ctx context.Context, tx *sql.Tx, storedID sql.NullString, event *ActivityEvent) error {
	if !storedID.Valid {
		if event != nil {
			return fmt.Errorf("stored observation has no event")
		}
		return nil
	}
	if event == nil {
		return fmt.Errorf("stored observation has event %q", storedID.String)
	}
	if storedID.String != event.ID {
		return fmt.Errorf("stored event ID %q does not match %q", storedID.String, event.ID)
	}
	var stored ActivityEvent
	var occurredAt, observedAt string
	err := tx.QueryRowContext(ctx, `
SELECT id,event_type,subject_type,subject_id,occurred_at,observed_at,source,source_ref,
       correlation_id,confidence,schema_version,payload_json
FROM activity_events WHERE id=?`, storedID.String).Scan(&stored.ID, &stored.EventType, &stored.SubjectType,
		&stored.SubjectID, &occurredAt, &observedAt, &stored.Source, &stored.SourceRef, &stored.CorrelationID,
		&stored.Confidence, &stored.SchemaVersion, &stored.PayloadJSON)
	if err != nil {
		return fmt.Errorf("read stored event %q: %w", storedID.String, err)
	}
	stored.OccurredAt, err = parseTime(occurredAt)
	if err != nil {
		return fmt.Errorf("parse stored event occurred time: %w", err)
	}
	stored.ObservedAt, err = parseTime(observedAt)
	if err != nil {
		return fmt.Errorf("parse stored event observed time: %w", err)
	}
	if stored != *event {
		return fmt.Errorf("stored event %q does not match replay", event.ID)
	}
	return nil
}
