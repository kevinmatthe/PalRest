package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

type ServerMetricObservation struct {
	At       time.Time
	Metrics  domain.ServerMetrics
	Event    *ActivityEvent
	Expected *ServerMetricToken
}

type ServerMetricToken struct {
	Exists  bool
	At      time.Time
	Metrics domain.ServerMetrics
}

type ServerMetricSnapshot struct {
	At      time.Time
	Metrics domain.ServerMetrics
	Event   *ActivityEvent
}

func (r *Repository) CurrentServerRuntime(ctx context.Context) (ServerRuntimeState, error) {
	return scanServerRuntime(r.db.QueryRowContext(ctx, `SELECT epoch,restarted_at FROM server_runtime_state WHERE id=1`))
}

func currentServerRuntimeTx(ctx context.Context, tx *sql.Tx) (ServerRuntimeState, error) {
	return scanServerRuntime(tx.QueryRowContext(ctx, `SELECT epoch,restarted_at FROM server_runtime_state WHERE id=1`))
}

func scanServerRuntime(row rowScanner) (ServerRuntimeState, error) {
	var state ServerRuntimeState
	var restartedAt sql.NullString
	if err := row.Scan(&state.Epoch, &restartedAt); err != nil {
		return ServerRuntimeState{}, err
	}
	if restartedAt.Valid {
		at, err := parseTime(restartedAt.String)
		if err != nil {
			return ServerRuntimeState{}, fmt.Errorf("parse server runtime restart time: %w", err)
		}
		state.RestartedAt = at
	}
	return state, nil
}

func serverRuntimeStatesEqual(a, b ServerRuntimeState) bool {
	return a.Epoch == b.Epoch && a.RestartedAt.UTC().Equal(b.RestartedAt.UTC())
}

type ServerDocumentObservation struct {
	Kind      string
	At        time.Time
	Canonical []byte
	Hash      string
	Event     *ActivityEvent
	Expected  *ServerDocumentToken
}

type ServerDocumentToken struct {
	Exists bool
	At     time.Time
	Hash   string
}

type ServerDocumentSnapshot struct {
	Kind      string
	At        time.Time
	Canonical []byte
	Hash      string
	Event     *ActivityEvent
}

func (r *Repository) RecordServerMetricObservation(ctx context.Context, write ServerMetricObservation) error {
	if err := validateServerMetricObservation(write); err != nil {
		return err
	}
	write.At = write.At.UTC()
	return r.WithTx(ctx, func(tx *Tx) error {
		if err := lockServerObservationState(ctx, tx.tx, "metrics"); err != nil {
			return fmt.Errorf("record server metric observation: lock current state: %w", err)
		}
		latestAt, latestMetrics, err := latestServerMetricStateTx(ctx, tx.tx)
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
				var latestEventID sql.NullString
				eventErr := tx.tx.QueryRowContext(ctx, `SELECT event_id FROM server_metric_samples WHERE observed_at=?`, formatObservationTime(write.At)).Scan(&latestEventID)
				if errors.Is(eventErr, sql.ErrNoRows) {
					return nil
				}
				if eventErr != nil {
					return fmt.Errorf("record server metric observation: read replay sample: %w", eventErr)
				}
				if err := compareOptionalStoredEvent(ctx, tx.tx, latestEventID, write.Event); err != nil {
					return fmt.Errorf("record server metric observation: sample at %s: %w", write.At.Format(time.RFC3339Nano), err)
				}
				return nil
			}
		}
		if !serverMetricTokenMatches(write.Expected, err == nil, latestAt, latestMetrics) {
			return fmt.Errorf("record server metric observation: %w", ErrObservationConflict)
		}
		if err := validateServerMetricTransition(err == nil, latestMetrics.UptimeSeconds, write); err != nil {
			return err
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
		if write.Event != nil && write.Event.EventType == "server_restarted" {
			if _, err := tx.tx.ExecContext(ctx, `UPDATE server_runtime_state SET epoch=epoch+1,restarted_at=? WHERE id=1`, formatObservationTime(write.At)); err != nil {
				return fmt.Errorf("record server metric observation: advance runtime epoch: %w", err)
			}
			var epoch int64
			if err := tx.tx.QueryRowContext(ctx, `SELECT epoch FROM server_runtime_state WHERE id=1`).Scan(&epoch); err != nil {
				return fmt.Errorf("record server metric observation: read advanced runtime epoch: %w", err)
			}
			if _, err := tx.tx.ExecContext(ctx, `UPDATE trajectory_samples SET runtime_epoch=? WHERE observed_at>=? AND runtime_epoch<?`, epoch, formatObservationTime(write.At), epoch); err != nil {
				return fmt.Errorf("record server metric observation: repair trajectory runtime epoch: %w", err)
			}
		}
		if _, err := tx.tx.ExecContext(ctx, `
INSERT INTO server_observation_state(
    kind,observed_at,server_fps,current_player_num,server_frame_time,max_player_num,
    uptime_seconds,base_camp_num,game_days
) VALUES('metrics',?,?,?,?,?,?,?,?)
ON CONFLICT(kind) DO UPDATE SET
    observed_at=excluded.observed_at,server_fps=excluded.server_fps,
    current_player_num=excluded.current_player_num,server_frame_time=excluded.server_frame_time,
    max_player_num=excluded.max_player_num,uptime_seconds=excluded.uptime_seconds,
    base_camp_num=excluded.base_camp_num,game_days=excluded.game_days`,
			formatObservationTime(write.At), write.Metrics.ServerFPS, write.Metrics.CurrentPlayerNum,
			write.Metrics.ServerFrameTime, write.Metrics.MaxPlayerNum, write.Metrics.UptimeSeconds,
			write.Metrics.BaseCampNum, write.Metrics.Days); err != nil {
			return fmt.Errorf("record server metric observation: update current state: %w", err)
		}
		return nil
	})
}

func validateServerMetricTransition(latestExists bool, oldUptime int64, write ServerMetricObservation) error {
	restarted := latestExists && write.Metrics.UptimeSeconds < oldUptime
	if !restarted {
		if write.Event != nil {
			return fmt.Errorf("record server metric observation: event requires an authoritative uptime decrease")
		}
		return nil
	}
	if write.Event == nil {
		return fmt.Errorf("record server metric observation: uptime decrease requires server_restarted event")
	}
	if write.Event.EventType != "server_restarted" {
		return fmt.Errorf("record server metric observation: uptime decrease event must be server_restarted")
	}
	if write.Event.SubjectType != "server" || write.Event.SubjectID != "server" || write.Event.Source != "palworld_rest" ||
		write.Event.Confidence != "observed" || write.Event.SchemaVersion != 1 || write.Event.SourceRef != write.Event.CorrelationID {
		return fmt.Errorf("record server metric observation: server_restarted envelope does not match service contract")
	}
	var payload struct {
		Old *int64 `json:"old_uptime_seconds"`
		New *int64 `json:"new_uptime_seconds"`
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(write.Event.PayloadJSON)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("record server metric observation: decode server_restarted payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("record server metric observation: server_restarted payload has trailing content")
	}
	if payload.Old == nil || payload.New == nil || *payload.Old != oldUptime || *payload.New != write.Metrics.UptimeSeconds {
		return fmt.Errorf("record server metric observation: server_restarted payload does not match authoritative uptime transition")
	}
	return nil
}

func (r *Repository) LatestServerMetrics(ctx context.Context) (time.Time, domain.ServerMetrics, error) {
	snapshot, err := r.LatestServerMetricObservation(ctx)
	return snapshot.At, snapshot.Metrics, err
}

func (r *Repository) LatestServerMetricObservation(ctx context.Context) (ServerMetricSnapshot, error) {
	latestAt, metrics, err := latestServerMetricStateDB(ctx, r.db)
	if err != nil {
		return ServerMetricSnapshot{}, err
	}
	var eventID sql.NullString
	eventErr := r.db.QueryRowContext(ctx, `SELECT event_id FROM server_metric_samples WHERE observed_at=?`, formatObservationTime(latestAt)).Scan(&eventID)
	if eventErr != nil && !errors.Is(eventErr, sql.ErrNoRows) {
		return ServerMetricSnapshot{}, fmt.Errorf("read latest server metrics: read raw sample: %w", eventErr)
	}
	event, err := readOptionalStoredEvent(ctx, r.db, eventID)
	if err != nil {
		return ServerMetricSnapshot{}, fmt.Errorf("read latest server metrics: %w", err)
	}
	return ServerMetricSnapshot{At: latestAt, Metrics: metrics, Event: event}, nil
}

func latestServerMetricStateTx(ctx context.Context, tx *sql.Tx) (time.Time, domain.ServerMetrics, error) {
	return scanLatestServerMetric(tx.QueryRowContext(ctx, `
SELECT observed_at,server_fps,current_player_num,server_frame_time,max_player_num,
       uptime_seconds,base_camp_num,game_days
FROM server_observation_state WHERE kind='metrics'`))
}

func latestServerMetricStateDB(ctx context.Context, db *sql.DB) (time.Time, domain.ServerMetrics, error) {
	return scanLatestServerMetric(db.QueryRowContext(ctx, `
SELECT observed_at,server_fps,current_player_num,server_frame_time,max_player_num,
       uptime_seconds,base_camp_num,game_days
FROM server_observation_state WHERE kind='metrics'`))
}

type rowScanner interface {
	Scan(...any) error
}

func scanLatestServerMetric(row rowScanner) (time.Time, domain.ServerMetrics, error) {
	var observedAt string
	var metrics domain.ServerMetrics
	err := row.Scan(&observedAt, &metrics.ServerFPS, &metrics.CurrentPlayerNum, &metrics.ServerFrameTime,
		&metrics.MaxPlayerNum, &metrics.UptimeSeconds, &metrics.BaseCampNum, &metrics.Days)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, domain.ServerMetrics{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, domain.ServerMetrics{}, fmt.Errorf("read latest server metrics: %w", err)
	}
	at, err := parseTime(observedAt)
	if err != nil {
		return time.Time{}, domain.ServerMetrics{}, fmt.Errorf("parse latest server metric time: %w", err)
	}
	return at, metrics, nil
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
		if err := lockServerObservationState(ctx, tx.tx, write.Kind); err != nil {
			return fmt.Errorf("record server document observation: lock current state: %w", err)
		}
		var stateAt, stateHash string
		stateErr := tx.tx.QueryRowContext(ctx, `
SELECT observed_at,content_hash FROM server_observation_state WHERE kind=?`, write.Kind).Scan(&stateAt, &stateHash)
		if stateErr != nil && !errors.Is(stateErr, sql.ErrNoRows) {
			return fmt.Errorf("record server document observation: read current state: %w", stateErr)
		}
		if stateErr == nil {
			parsedLatest, parseErr := parseTime(stateAt)
			if parseErr != nil {
				return fmt.Errorf("record server document observation: parse current state time: %w", parseErr)
			}
			switch {
			case write.At.Before(parsedLatest):
				return fmt.Errorf("record server document observation: time %s is older than current state %s", write.At.Format(time.RFC3339Nano), parsedLatest.Format(time.RFC3339Nano))
			case write.At.Equal(parsedLatest):
				if stateHash != write.Hash {
					return fmt.Errorf("record server document observation: state at %s has different hash", write.At.Format(time.RFC3339Nano))
				}
				if err := compareStoredDocumentBlob(ctx, tx.tx, write.Kind, write.Hash, write.Canonical); err != nil {
					return err
				}
				var storedEventID sql.NullString
				occurrenceErr := tx.tx.QueryRowContext(ctx, `
SELECT event_id FROM server_document_observations WHERE kind=? AND observed_at=?`,
					write.Kind, formatObservationTime(write.At)).Scan(&storedEventID)
				if errors.Is(occurrenceErr, sql.ErrNoRows) {
					if write.Event != nil {
						return fmt.Errorf("record server document observation: unchanged state has no event")
					}
					return nil
				}
				if occurrenceErr != nil {
					return fmt.Errorf("record server document observation: read exact occurrence: %w", occurrenceErr)
				}
				if err := compareOptionalStoredEvent(ctx, tx.tx, storedEventID, write.Event); err != nil {
					return fmt.Errorf("record server document observation: occurrence at %s: %w", write.At.Format(time.RFC3339Nano), err)
				}
				changed = true
				return nil
			}
			if !serverDocumentTokenMatches(write.Expected, true, stateAt, stateHash) {
				return fmt.Errorf("record server document observation: %w", ErrObservationConflict)
			}
			if stateHash == write.Hash {
				if write.Event != nil {
					return fmt.Errorf("record server document observation: unchanged document cannot have a transition event")
				}
				if err := compareStoredDocumentBlob(ctx, tx.tx, write.Kind, write.Hash, write.Canonical); err != nil {
					return err
				}
				if _, err := tx.tx.ExecContext(ctx, `UPDATE server_observation_state SET observed_at=? WHERE kind=?`, formatObservationTime(write.At), write.Kind); err != nil {
					return fmt.Errorf("record server document observation: advance current state: %w", err)
				}
				return nil
			}
		}
		if stateErr != nil && !serverDocumentTokenMatches(write.Expected, false, stateAt, stateHash) {
			return fmt.Errorf("record server document observation: %w", ErrObservationConflict)
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
		if _, err := tx.tx.ExecContext(ctx, `
INSERT INTO server_observation_state(kind,observed_at,content_hash) VALUES(?,?,?)
ON CONFLICT(kind) DO UPDATE SET observed_at=excluded.observed_at,content_hash=excluded.content_hash`,
			write.Kind, formatObservationTime(write.At), write.Hash); err != nil {
			return fmt.Errorf("record server document observation: update current state: %w", err)
		}
		changed = true
		return nil
	})
	return changed, err
}

func lockServerObservationState(ctx context.Context, tx *sql.Tx, kind string) error {
	_, err := tx.ExecContext(ctx, `UPDATE server_observation_state SET observed_at=observed_at WHERE kind=?`, kind)
	return err
}

func serverMetricTokenMatches(expected *ServerMetricToken, exists bool, at time.Time, metrics domain.ServerMetrics) bool {
	if expected == nil {
		return true
	}
	if expected.Exists != exists {
		return false
	}
	return !exists || expected.At.UTC().Equal(at) && expected.Metrics == metrics
}

func serverDocumentTokenMatches(expected *ServerDocumentToken, exists bool, observedAt, hash string) bool {
	if expected == nil {
		return true
	}
	if expected.Exists != exists {
		return false
	}
	if !exists {
		return true
	}
	at, err := parseTime(observedAt)
	return err == nil && expected.At.UTC().Equal(at) && expected.Hash == hash
}

func (r *Repository) LatestServerDocument(ctx context.Context, kind string) (ServerDocumentSnapshot, error) {
	if kind != "info" && kind != "settings" {
		return ServerDocumentSnapshot{}, fmt.Errorf("read latest server document: unknown kind %q", kind)
	}
	var snapshot ServerDocumentSnapshot
	var observedAt string
	var eventID sql.NullString
	err := r.db.QueryRowContext(ctx, `
SELECT s.kind,s.observed_at,s.content_hash,d.canonical_json,o.event_id
FROM server_observation_state s
JOIN server_documents d ON d.kind=s.kind AND d.content_hash=s.content_hash
LEFT JOIN server_document_observations o ON o.kind=s.kind AND o.observed_at=s.observed_at
WHERE s.kind=?`, kind).
		Scan(&snapshot.Kind, &observedAt, &snapshot.Hash, &snapshot.Canonical, &eventID)
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
	snapshot.Event, err = readOptionalStoredEvent(ctx, r.db, eventID)
	if err != nil {
		return ServerDocumentSnapshot{}, fmt.Errorf("read latest server document: %w", err)
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
	if write.Hash != serverDocumentHash(write.Canonical) {
		return fmt.Errorf("record server document observation: hash does not match canonical content")
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

func serverDocumentHash(canonical []byte) string {
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])
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
	stored, err := readStoredEvent(ctx, tx, storedID.String)
	if err != nil {
		return err
	}
	if *stored != *event {
		return fmt.Errorf("stored event %q does not match replay", event.ID)
	}
	return nil
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readOptionalStoredEvent(ctx context.Context, queryer queryRower, storedID sql.NullString) (*ActivityEvent, error) {
	if !storedID.Valid {
		return nil, nil
	}
	return readStoredEvent(ctx, queryer, storedID.String)
}

func readStoredEvent(ctx context.Context, queryer queryRower, id string) (*ActivityEvent, error) {
	var stored ActivityEvent
	var occurredAt, observedAt string
	err := queryer.QueryRowContext(ctx, `
SELECT id,event_type,subject_type,subject_id,occurred_at,observed_at,source,source_ref,
       correlation_id,confidence,schema_version,payload_json
FROM activity_events WHERE id=?`, id).Scan(&stored.ID, &stored.EventType, &stored.SubjectType,
		&stored.SubjectID, &occurredAt, &observedAt, &stored.Source, &stored.SourceRef, &stored.CorrelationID,
		&stored.Confidence, &stored.SchemaVersion, &stored.PayloadJSON)
	if err != nil {
		return nil, fmt.Errorf("read stored event %q: %w", id, err)
	}
	stored.OccurredAt, err = parseTime(occurredAt)
	if err != nil {
		return nil, fmt.Errorf("parse stored event occurred time: %w", err)
	}
	stored.ObservedAt, err = parseTime(observedAt)
	if err != nil {
		return nil, fmt.Errorf("parse stored event observed time: %w", err)
	}
	return &stored, nil
}
