package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"time"
	"unicode"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

type ActivityEvent struct {
	ID            string    `json:"id"`
	EventType     string    `json:"event_type"`
	SubjectType   string    `json:"subject_type"`
	SubjectID     string    `json:"subject_id"`
	OccurredAt    time.Time `json:"occurred_at"`
	ObservedAt    time.Time `json:"observed_at"`
	Source        string    `json:"source"`
	SourceRef     string    `json:"source_ref"`
	CorrelationID string    `json:"correlation_id"`
	Confidence    string    `json:"confidence"`
	SchemaVersion int       `json:"schema_version"`
	PayloadJSON   string    `json:"payload_json"`
}

type TrajectorySample struct {
	UserID       string    `json:"user_id"`
	SegmentID    string    `json:"segment_id"`
	ObservedAt   time.Time `json:"observed_at"`
	X            float64   `json:"x"`
	Y            float64   `json:"y"`
	Ping         float64   `json:"ping"`
	Level        int       `json:"level"`
	SourceRef    string    `json:"source_ref"`
	RuntimeEpoch int64     `json:"runtime_epoch"`
}

type PlayerPrivateSample struct {
	UserID     string    `json:"user_id"`
	ObservedAt time.Time `json:"observed_at"`
	IP         string    `json:"ip"`
	Ping       float64   `json:"ping"`
	Level      int       `json:"level"`
	SourceRef  string    `json:"source_ref"`
}

type SensitivePlayerTimeline struct {
	Events         []ActivityEvent       `json:"events"`
	Trajectories   []TrajectorySample    `json:"trajectories"`
	PrivateSamples []PlayerPrivateSample `json:"private_samples"`
}

type PlayerObservationWrite struct {
	Runtime        *ServerRuntimeState
	Events         []ActivityEvent
	Trajectories   []TrajectorySample
	PrivateSamples []PlayerPrivateSample
}

type ServerRuntimeState struct {
	Epoch       int64
	RestartedAt time.Time
}

var knownObservationSources = map[string]struct{}{
	"guard":         {},
	"palworld_rest": {},
	"save_snapshot": {},
}

var knownObservationConfidences = map[string]struct{}{
	"observed":         {},
	"snapshot_derived": {},
}

func (r *Repository) RecordPlayerObservation(ctx context.Context, write PlayerObservationWrite) error {
	for i := range write.Events {
		if err := validateActivityEvent(write.Events[i]); err != nil {
			return fmt.Errorf("record player observation: event %d: %w", i, err)
		}
	}
	for i := range write.Trajectories {
		if err := validateTrajectorySample(write.Trajectories[i]); err != nil {
			return fmt.Errorf("record player observation: trajectory %d: %w", i, err)
		}
	}
	for i := range write.PrivateSamples {
		if err := validatePlayerPrivateSample(write.PrivateSamples[i]); err != nil {
			return fmt.Errorf("record player observation: private sample %d: %w", i, err)
		}
	}
	return r.WithTx(ctx, func(tx *Tx) error {
		if write.Runtime != nil {
			current, err := currentServerRuntimeTx(ctx, tx.tx)
			if err != nil {
				return fmt.Errorf("record player observation: read server runtime: %w", err)
			}
			if !serverRuntimeStatesEqual(current, *write.Runtime) {
				replay, replayErr := playerObservationAlreadyStored(ctx, tx.tx, write, current.Epoch)
				if replayErr != nil {
					return fmt.Errorf("record player observation: prove runtime-conflicted replay: %w", replayErr)
				}
				if replay {
					return nil
				}
				return fmt.Errorf("record player observation: %w", ErrObservationConflict)
			}
		}
		for _, event := range write.Events {
			result, err := tx.tx.ExecContext(ctx, `
INSERT INTO activity_events(
    id,event_type,subject_type,subject_id,occurred_at,observed_at,source,source_ref,
    correlation_id,confidence,schema_version,payload_json
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, event.ID, event.EventType, event.SubjectType, event.SubjectID,
				formatObservationTime(event.OccurredAt), formatObservationTime(event.ObservedAt), event.Source, event.SourceRef,
				event.CorrelationID, event.Confidence, event.SchemaVersion, event.PayloadJSON)
			if err != nil {
				return fmt.Errorf("insert activity event %q: %w", event.ID, err)
			}
			inserted, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("read activity event insert result: %w", err)
			}
			if inserted == 0 {
				stored, err := readStoredEvent(ctx, tx.tx, event.ID)
				if err != nil {
					return err
				}
				if !activityEventsEqual(*stored, event) {
					return fmt.Errorf("activity event ID %q conflicts with different content", event.ID)
				}
			}
		}
		for _, sample := range write.Trajectories {
			result, err := tx.tx.ExecContext(ctx, `
INSERT INTO trajectory_samples(user_id,segment_id,observed_at,x,y,ping,level,source_ref,runtime_epoch)
VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id,observed_at) DO NOTHING`, sample.UserID, sample.SegmentID, formatObservationTime(sample.ObservedAt),
				sample.X, sample.Y, sample.Ping, sample.Level, sample.SourceRef, sample.RuntimeEpoch)
			if err != nil {
				return fmt.Errorf("insert trajectory sample for %q at %s: %w", sample.UserID, formatObservationTime(sample.ObservedAt), err)
			}
			inserted, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("read trajectory insert result: %w", err)
			}
			if inserted == 0 {
				var stored TrajectorySample
				var observed string
				err := tx.tx.QueryRowContext(ctx, `SELECT user_id,segment_id,observed_at,x,y,ping,level,source_ref,runtime_epoch FROM trajectory_samples WHERE user_id=? AND observed_at=?`, sample.UserID, formatObservationTime(sample.ObservedAt)).Scan(&stored.UserID, &stored.SegmentID, &observed, &stored.X, &stored.Y, &stored.Ping, &stored.Level, &stored.SourceRef, &stored.RuntimeEpoch)
				if err != nil {
					return fmt.Errorf("read replayed trajectory: %w", err)
				}
				stored.ObservedAt, err = parseTime(observed)
				if err != nil {
					return err
				}
				if !trajectorySamplesEqual(stored, sample) {
					return fmt.Errorf("trajectory for %q at %s conflicts with different content", sample.UserID, formatObservationTime(sample.ObservedAt))
				}
			}
		}
		for _, sample := range write.PrivateSamples {
			result, err := tx.tx.ExecContext(ctx, `
INSERT INTO player_private_samples(user_id,observed_at,ip,ping,level,source_ref)
VALUES(?,?,?,?,?,?) ON CONFLICT(user_id,observed_at) DO NOTHING`, sample.UserID, formatObservationTime(sample.ObservedAt), sample.IP,
				sample.Ping, sample.Level, sample.SourceRef)
			if err != nil {
				return fmt.Errorf("insert private player sample for %q at %s: %w", sample.UserID, formatObservationTime(sample.ObservedAt), err)
			}
			inserted, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("read private sample insert result: %w", err)
			}
			if inserted == 0 {
				var stored PlayerPrivateSample
				var observed string
				err := tx.tx.QueryRowContext(ctx, `SELECT user_id,observed_at,ip,ping,level,source_ref FROM player_private_samples WHERE user_id=? AND observed_at=?`, sample.UserID, formatObservationTime(sample.ObservedAt)).Scan(&stored.UserID, &observed, &stored.IP, &stored.Ping, &stored.Level, &stored.SourceRef)
				if err != nil {
					return fmt.Errorf("read replayed private sample: %w", err)
				}
				stored.ObservedAt, err = parseTime(observed)
				if err != nil {
					return err
				}
				if !privateSamplesEqual(stored, sample) {
					return fmt.Errorf("private sample for %q at %s conflicts with different content", sample.UserID, formatObservationTime(sample.ObservedAt))
				}
			}
		}
		return nil
	})
}

func playerObservationAlreadyStored(ctx context.Context, tx *sql.Tx, write PlayerObservationWrite, currentEpoch int64) (bool, error) {
	if len(write.Events) == 0 && len(write.Trajectories) == 0 && len(write.PrivateSamples) == 0 {
		return false, nil
	}
	for _, event := range write.Events {
		stored, err := readStoredEvent(ctx, tx, event.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !activityEventsEqual(*stored, event) {
			return false, nil
		}
	}
	for _, sample := range write.Trajectories {
		var stored TrajectorySample
		var observed string
		err := tx.QueryRowContext(ctx, `SELECT user_id,segment_id,observed_at,x,y,ping,level,source_ref,runtime_epoch FROM trajectory_samples WHERE user_id=? AND observed_at=?`, sample.UserID, formatObservationTime(sample.ObservedAt)).
			Scan(&stored.UserID, &stored.SegmentID, &observed, &stored.X, &stored.Y, &stored.Ping, &stored.Level, &stored.SourceRef, &stored.RuntimeEpoch)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		stored.ObservedAt, err = parseTime(observed)
		if err != nil {
			return false, err
		}
		originalEpoch := stored.RuntimeEpoch
		stored.RuntimeEpoch = sample.RuntimeEpoch
		if !trajectorySamplesEqual(stored, sample) || originalEpoch < sample.RuntimeEpoch || originalEpoch > currentEpoch {
			return false, nil
		}
	}
	for _, sample := range write.PrivateSamples {
		var stored PlayerPrivateSample
		var observed string
		err := tx.QueryRowContext(ctx, `SELECT user_id,observed_at,ip,ping,level,source_ref FROM player_private_samples WHERE user_id=? AND observed_at=?`, sample.UserID, formatObservationTime(sample.ObservedAt)).
			Scan(&stored.UserID, &observed, &stored.IP, &stored.Ping, &stored.Level, &stored.SourceRef)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		stored.ObservedAt, err = parseTime(observed)
		if err != nil || !privateSamplesEqual(stored, sample) {
			return false, err
		}
	}
	return true, nil
}

func activityEventsEqual(a, b ActivityEvent) bool {
	return a.ID == b.ID && a.EventType == b.EventType && a.SubjectType == b.SubjectType && a.SubjectID == b.SubjectID &&
		a.OccurredAt.Equal(b.OccurredAt) && a.ObservedAt.Equal(b.ObservedAt) && a.Source == b.Source && a.SourceRef == b.SourceRef &&
		a.CorrelationID == b.CorrelationID && a.Confidence == b.Confidence && a.SchemaVersion == b.SchemaVersion && jsonSemanticallyEqual(a.PayloadJSON, b.PayloadJSON)
}

func jsonSemanticallyEqual(a, b string) bool {
	if !validJSONObject([]byte(a)) || !validJSONObject([]byte(b)) {
		return false
	}
	var left, right any
	leftDecoder := json.NewDecoder(bytes.NewBufferString(a))
	leftDecoder.UseNumber()
	rightDecoder := json.NewDecoder(bytes.NewBufferString(b))
	rightDecoder.UseNumber()
	if leftDecoder.Decode(&left) != nil || rightDecoder.Decode(&right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

func trajectorySamplesEqual(a, b TrajectorySample) bool {
	return a.UserID == b.UserID && a.SegmentID == b.SegmentID && a.ObservedAt.Equal(b.ObservedAt) && a.X == b.X && a.Y == b.Y && a.Ping == b.Ping && a.Level == b.Level && a.SourceRef == b.SourceRef && a.RuntimeEpoch == b.RuntimeEpoch
}

func privateSamplesEqual(a, b PlayerPrivateSample) bool {
	return a.UserID == b.UserID && a.ObservedAt.Equal(b.ObservedAt) && a.IP == b.IP && a.Ping == b.Ping && a.Level == b.Level && a.SourceRef == b.SourceRef
}

func validatePlayerPrivateSample(sample PlayerPrivateSample) error {
	if strings.TrimSpace(sample.UserID) == "" || strings.TrimSpace(sample.SourceRef) == "" {
		return fmt.Errorf("user ID and source reference must be nonempty")
	}
	if sample.ObservedAt.IsZero() {
		return fmt.Errorf("observed time is zero")
	}
	if sample.IP == "" || len(sample.IP) > 256 || strings.IndexFunc(sample.IP, unicode.IsControl) >= 0 {
		return fmt.Errorf("IP must be nonempty safe text of at most 256 bytes")
	}
	if !finite(sample.Ping) || sample.Ping < 0 || sample.Level < 0 {
		return fmt.Errorf("ping must be finite and nonnegative and level must be nonnegative")
	}
	return nil
}

func validateActivityEvent(event ActivityEvent) error {
	for name, value := range map[string]string{
		"ID": event.ID, "event type": event.EventType, "subject type": event.SubjectType,
		"subject ID": event.SubjectID, "source reference": event.SourceRef,
		"correlation ID": event.CorrelationID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is empty", name)
		}
	}
	if event.OccurredAt.IsZero() || event.ObservedAt.IsZero() {
		return fmt.Errorf("timestamps must be nonzero")
	}
	if _, ok := knownObservationSources[event.Source]; !ok {
		return fmt.Errorf("unknown source %q", event.Source)
	}
	if _, ok := knownObservationConfidences[event.Confidence]; !ok {
		return fmt.Errorf("unknown confidence %q", event.Confidence)
	}
	if event.SchemaVersion <= 0 {
		return fmt.Errorf("schema version must be positive")
	}
	if !validJSONObject([]byte(event.PayloadJSON)) {
		return fmt.Errorf("payload must be a valid JSON object")
	}
	return nil
}

func validateTrajectorySample(sample TrajectorySample) error {
	for name, value := range map[string]string{
		"user ID": sample.UserID, "segment ID": sample.SegmentID, "source reference": sample.SourceRef,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is empty", name)
		}
	}
	if sample.ObservedAt.IsZero() {
		return fmt.Errorf("observed time is zero")
	}
	if sample.RuntimeEpoch < 0 {
		return fmt.Errorf("runtime epoch must be nonnegative")
	}
	if !finite(sample.X) || !finite(sample.Y) || !finite(sample.Ping) || sample.Ping < 0 {
		return fmt.Errorf("coordinates must be finite and ping must be finite and nonnegative")
	}
	return nil
}

func (r *Repository) RecordServerMetrics(ctx context.Context, at time.Time, metrics domain.ServerMetrics) error {
	return r.RecordServerMetricObservation(ctx, ServerMetricObservation{At: at, Metrics: metrics})
}

func (r *Repository) RecordServerDocument(ctx context.Context, kind string, at time.Time, canonical []byte, hash string) (bool, error) {
	if kind != "info" && kind != "settings" {
		return false, fmt.Errorf("record server document: unknown kind %q", kind)
	}
	if at.IsZero() {
		return false, fmt.Errorf("record server document: observation time is zero")
	}
	if strings.TrimSpace(hash) == "" {
		return false, fmt.Errorf("record server document: hash is empty")
	}
	if hash != serverDocumentHash(canonical) {
		return false, fmt.Errorf("record server document: hash does not match canonical content")
	}
	if !validJSONObject(canonical) {
		return false, fmt.Errorf("record server document: canonical content must be a valid JSON object")
	}
	result, err := r.db.ExecContext(ctx, `
INSERT INTO server_documents(kind,content_hash,observed_at,canonical_json)
VALUES(?,?,?,?) ON CONFLICT(kind,content_hash) DO NOTHING`, kind, hash, formatObservationTime(at), string(canonical))
	if err != nil {
		return false, fmt.Errorf("insert server document: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read server document insert result: %w", err)
	}
	return inserted == 1, nil
}

func (r *Repository) ReadSensitivePlayerTimeline(ctx context.Context, actor, userID string, start, end time.Time, limit int) (SensitivePlayerTimeline, error) {
	var empty SensitivePlayerTimeline
	if strings.TrimSpace(actor) == "" {
		return empty, fmt.Errorf("read sensitive player timeline: actor is empty")
	}
	if strings.TrimSpace(userID) == "" {
		return empty, fmt.Errorf("read sensitive player timeline: user ID is empty")
	}
	if start.IsZero() || end.IsZero() || !start.Before(end) {
		return empty, fmt.Errorf("read sensitive player timeline: nonzero start must be before end")
	}
	if limit < 1 || limit > 2000 {
		return empty, fmt.Errorf("read sensitive player timeline: limit must be between 1 and 2000")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		queryErr := fmt.Errorf("begin sensitive timeline transaction: %w", err)
		return empty, r.auditQueryError(ctx, actor, "read_player_timeline", "player", userID, &start, &end, queryErr)
	}
	events, err := queryTimelineEvents(ctx, tx, userID, start, end, limit)
	if err == nil {
		var samples []TrajectorySample
		samples, err = queryTimelineSamples(ctx, tx, userID, start, end, limit)
		if err == nil {
			var private []PlayerPrivateSample
			private, err = queryTimelinePrivateSamples(ctx, tx, userID, start, end, limit)
			if err != nil {
				goto queryFailed
			}
			outcome := "success"
			if len(events) == 0 && len(samples) == 0 && len(private) == 0 {
				known, knownErr := knownPlayerTx(ctx, tx, userID)
				if knownErr != nil {
					err = knownErr
					goto queryFailed
				}
				if !known {
					outcome = "not_found"
				}
			}
			if r.beforeSensitiveTimelineAudit != nil {
				r.beforeSensitiveTimelineAudit()
			}
			if auditErr := insertTimelineAudit(ctx, tx, actor, userID, start, end, outcome); auditErr != nil {
				_ = tx.Rollback()
				queryErr := fmt.Errorf("audit sensitive player timeline: %w", auditErr)
				return empty, r.auditQueryError(ctx, actor, "read_player_timeline", "player", userID, &start, &end, queryErr)
			}
			if commitErr := tx.Commit(); commitErr != nil {
				_ = tx.Rollback()
				queryErr := fmt.Errorf("commit sensitive player timeline: %w", commitErr)
				return empty, r.auditQueryError(ctx, actor, "read_player_timeline", "player", userID, &start, &end, queryErr)
			}
			if outcome == "not_found" {
				return empty, ErrNotFound
			}
			return SensitivePlayerTimeline{Events: events, Trajectories: samples, PrivateSamples: private}, nil
		}
	}

queryFailed:

	// SQLite permits many statement errors without poisoning a transaction, but
	// rolling back before the audit is safer and avoids blocking the repository's
	// single connection. Error outcomes deliberately use this second transaction;
	// successful reads never do.
	_ = tx.Rollback()
	queryErr := err
	return empty, r.auditQueryError(ctx, actor, "read_player_timeline", "player", userID, &start, &end, fmt.Errorf("query sensitive player timeline: %w", queryErr))
}

func queryTimelinePrivateSamples(ctx context.Context, tx *sql.Tx, userID string, start, end time.Time, limit int) ([]PlayerPrivateSample, error) {
	rows, err := tx.QueryContext(ctx, `SELECT user_id,observed_at,ip,ping,level,source_ref
FROM player_private_samples WHERE user_id=? AND observed_at>=? AND observed_at<? ORDER BY observed_at,id LIMIT ?`,
		userID, formatObservationTime(start), formatObservationTime(end), limit)
	if err != nil {
		return nil, fmt.Errorf("query private player samples: %w", err)
	}
	defer rows.Close()
	result := make([]PlayerPrivateSample, 0)
	for rows.Next() {
		var sample PlayerPrivateSample
		var observed string
		if err := rows.Scan(&sample.UserID, &observed, &sample.IP, &sample.Ping, &sample.Level, &sample.SourceRef); err != nil {
			return nil, err
		}
		sample.ObservedAt, err = parseTime(observed)
		if err != nil {
			return nil, err
		}
		result = append(result, sample)
	}
	return result, rows.Err()
}

func knownPlayerTx(ctx context.Context, tx *sql.Tx, userID string) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM players WHERE user_id=? UNION ALL
SELECT 1 FROM player_sessions WHERE user_id=? UNION ALL
SELECT 1 FROM activity_events WHERE subject_type='player' AND subject_id=? UNION ALL
SELECT 1 FROM trajectory_samples WHERE user_id=? UNION ALL
SELECT 1 FROM player_private_samples WHERE user_id=?)`, userID, userID, userID, userID, userID).Scan(&exists)
	return exists == 1, err
}

func queryTimelineEvents(ctx context.Context, tx *sql.Tx, userID string, start, end time.Time, limit int) ([]ActivityEvent, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT id,event_type,subject_type,subject_id,occurred_at,observed_at,source,source_ref,
       correlation_id,confidence,schema_version,payload_json
FROM activity_events
WHERE subject_type='player' AND subject_id=? AND occurred_at>=? AND occurred_at<?
ORDER BY occurred_at,id LIMIT ?`, userID, formatObservationTime(start), formatObservationTime(end), limit)
	if err != nil {
		return nil, fmt.Errorf("query activity events: %w", err)
	}
	defer rows.Close()
	events := make([]ActivityEvent, 0)
	for rows.Next() {
		var event ActivityEvent
		var occurredAt, observedAt string
		if err := rows.Scan(&event.ID, &event.EventType, &event.SubjectType, &event.SubjectID,
			&occurredAt, &observedAt, &event.Source, &event.SourceRef, &event.CorrelationID,
			&event.Confidence, &event.SchemaVersion, &event.PayloadJSON); err != nil {
			return nil, fmt.Errorf("scan activity event: %w", err)
		}
		var err error
		event.OccurredAt, err = parseTime(occurredAt)
		if err != nil {
			return nil, fmt.Errorf("parse activity event %q occurred time: %w", event.ID, err)
		}
		event.ObservedAt, err = parseTime(observedAt)
		if err != nil {
			return nil, fmt.Errorf("parse activity event %q observed time: %w", event.ID, err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate activity events: %w", err)
	}
	return events, nil
}

func queryTimelineSamples(ctx context.Context, tx *sql.Tx, userID string, start, end time.Time, limit int) ([]TrajectorySample, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT user_id,segment_id,observed_at,x,y,ping,level,source_ref,runtime_epoch
FROM trajectory_samples
WHERE user_id=? AND observed_at>=? AND observed_at<?
ORDER BY observed_at,id LIMIT ?`, userID, formatObservationTime(start), formatObservationTime(end), limit)
	if err != nil {
		return nil, fmt.Errorf("query trajectory samples: %w", err)
	}
	defer rows.Close()
	samples := make([]TrajectorySample, 0)
	for rows.Next() {
		var sample TrajectorySample
		var observedAt string
		if err := rows.Scan(&sample.UserID, &sample.SegmentID, &observedAt, &sample.X, &sample.Y,
			&sample.Ping, &sample.Level, &sample.SourceRef, &sample.RuntimeEpoch); err != nil {
			return nil, fmt.Errorf("scan trajectory sample: %w", err)
		}
		var err error
		sample.ObservedAt, err = parseTime(observedAt)
		if err != nil {
			return nil, fmt.Errorf("parse trajectory sample observed time: %w", err)
		}
		sample.SegmentID = encodeRuntimeSegmentID(sample.RuntimeEpoch, sample.SegmentID)
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trajectory samples: %w", err)
	}
	return samples, nil
}

func encodeRuntimeSegmentID(epoch int64, segmentID string) string {
	return fmt.Sprintf("runtime:%d:%s", epoch, base64.RawURLEncoding.EncodeToString([]byte(segmentID)))
}

func insertTimelineAudit(ctx context.Context, tx *sql.Tx, actor, userID string, start, end time.Time, outcome string) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO sensitive_access_audit(
    actor,action,subject_type,subject_id,range_start,range_end,outcome,requested_at
) VALUES(?,?,?,?,?,?,?,?)`, actor, "read_player_timeline", "player", userID,
		formatObservationTime(start), formatObservationTime(end), outcome, formatObservationTime(time.Now()))
	return err
}

func (r *Repository) CleanupRawObservations(ctx context.Context, cutoff time.Time, limit int) (deleted int, err error) {
	if cutoff.IsZero() {
		return 0, fmt.Errorf("cleanup raw observations: cutoff is zero")
	}
	if limit < 1 || limit > 2000 {
		return 0, fmt.Errorf("cleanup raw observations: limit must be between 1 and 2000")
	}
	for _, target := range []struct {
		name, query string
	}{
		{"activity events", `DELETE FROM activity_events WHERE rowid IN (
SELECT e.rowid FROM activity_events e
WHERE e.occurred_at<?
  AND NOT EXISTS (SELECT 1 FROM server_metric_samples m WHERE m.event_id=e.id)
  AND NOT EXISTS (SELECT 1 FROM server_document_observations d WHERE d.event_id=e.id)
ORDER BY e.occurred_at,e.id LIMIT ?)`},
		{"trajectory samples", `DELETE FROM trajectory_samples WHERE id IN (SELECT id FROM trajectory_samples WHERE observed_at<? ORDER BY observed_at,id LIMIT ?)`},
		{"private player samples", `DELETE FROM player_private_samples WHERE id IN (SELECT id FROM player_private_samples WHERE observed_at<? ORDER BY observed_at,id LIMIT ?)`},
		{"server metric samples", `DELETE FROM server_metric_samples WHERE rowid IN (SELECT rowid FROM server_metric_samples WHERE observed_at<? ORDER BY observed_at LIMIT ?)`},
	} {
		var affected int64
		if err := r.WithTx(ctx, func(tx *Tx) error {
			result, execErr := tx.tx.ExecContext(ctx, target.query, formatObservationTime(cutoff), limit)
			if execErr != nil {
				return fmt.Errorf("delete %s: %w", target.name, execErr)
			}
			affected, execErr = result.RowsAffected()
			if execErr != nil {
				return fmt.Errorf("count deleted %s: %w", target.name, execErr)
			}
			return nil
		}); err != nil {
			return deleted, fmt.Errorf("cleanup raw observations: %w", err)
		}
		deleted += int(affected)
	}
	return deleted, nil
}

func validJSONObject(value []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	isObject, err := consumeStrictJSONValue(decoder)
	if err != nil || !isObject {
		return false
	}
	_, err = decoder.Token()
	return err == io.EOF
}

func consumeStrictJSONValue(decoder *json.Decoder) (bool, error) {
	token, err := decoder.Token()
	if err != nil {
		return false, err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return false, nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return false, fmt.Errorf("JSON object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return false, fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			if _, err := consumeStrictJSONValue(decoder); err != nil {
				return false, err
			}
		}
		closing, err := decoder.Token()
		return true, strictClosingDelimiter(closing, '}', err)
	case '[':
		for decoder.More() {
			if _, err := consumeStrictJSONValue(decoder); err != nil {
				return false, err
			}
		}
		closing, err := decoder.Token()
		return false, strictClosingDelimiter(closing, ']', err)
	default:
		return false, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func strictClosingDelimiter(token json.Token, want json.Delim, err error) error {
	if err != nil {
		return err
	}
	if token != want {
		return fmt.Errorf("unexpected JSON closing delimiter %v", token)
	}
	return nil
}

const observationTimeFormat = "2006-01-02T15:04:05.000000000Z"

func formatObservationTime(value time.Time) string {
	return value.UTC().Format(observationTimeFormat)
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
