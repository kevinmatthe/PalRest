package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var progressMetricNames = []string{"owned_pals", "capture_total", "paldeck", "fast_travel", "level", "experience"}
var progressReasonPattern = regexp.MustCompile(`^[a-z0-9_]{0,80}$`)

// A missing metric is unknown, never a measured zero. Sets carry their stable IDs
// so equal counts with different members still produce a change.
type ProgressMetric struct {
	State  string   `json:"state"`
	Value  *int64   `json:"value,omitempty"`
	IDs    []string `json:"ids,omitempty"`
	Reason string   `json:"reason,omitempty"`
}

type SaveProgress struct {
	SchemaVersion    int                       `json:"schema_version"`
	Metrics          map[string]ProgressMetric `json:"metrics"`
	UnattributedPals *ProgressMetric           `json:"unattributed_pals,omitempty"`
}

type ProgressCheckpoint struct {
	ID               uint                      `json:"id"`
	WorldID          string                    `json:"world_id"`
	ObservedAt       string                    `json:"observed_at"`
	CapturedAt       string                    `json:"captured_at"`
	Source           string                    `json:"source"`
	SourceTimeKind   string                    `json:"source_time_kind"`
	SchemaVersion    int                       `json:"schema_version"`
	Consistent       bool                      `json:"consistent"`
	Boundary         string                    `json:"boundary"`
	Metrics          map[string]ProgressMetric `json:"metrics"`
	UnattributedPals *ProgressMetric           `json:"unattributed_pals,omitempty"`
}

type ProgressChange struct {
	ID                   uint     `json:"id"`
	Metric               string   `json:"metric"`
	Before               int64    `json:"before"`
	After                int64    `json:"after"`
	Delta                int64    `json:"delta"`
	Added                []string `json:"added"`
	Removed              []string `json:"removed"`
	IntervalStart        string   `json:"interval_start"`
	IntervalEnd          string   `json:"interval_end"`
	PreviousCheckpointID uint     `json:"previous_checkpoint_id"`
	CheckpointID         uint     `json:"checkpoint_id"`
	RuleVersion          int      `json:"rule_version"`
	Confidence           string   `json:"confidence"`
	Source               string   `json:"source"`
}

type PlayerProgress struct {
	UserID          string               `json:"user_id"`
	Status          string               `json:"status"`
	Baseline        *ProgressCheckpoint  `json:"baseline"`
	Checkpoints     []ProgressCheckpoint `json:"checkpoints"`
	Changes         []ProgressChange     `json:"changes"`
	CheckpointTotal int64                `json:"checkpoint_total"`
	ChangeTotal     int64                `json:"change_total"`
}

type progressCheckpointModel struct {
	ID                 uint   `gorm:"primaryKey"`
	ImportID           uint   `gorm:"not null;uniqueIndex:progress_import_player,priority:1"`
	SavePlayerHex      string `gorm:"not null;uniqueIndex:progress_import_player,priority:2;index:progress_time,priority:1"`
	WorldID            string `gorm:"not null"`
	WorldIDKind        string `gorm:"not null"`
	ObservedAt         string `gorm:"not null;index:progress_time,priority:2"`
	CapturedAt         string `gorm:"not null"`
	SourceTimeKind     string `gorm:"not null"`
	SchemaVersion      int    `gorm:"not null"`
	ParserName         string `gorm:"not null"`
	ParserVersion      int    `gorm:"not null"`
	ProgressDefinition string `gorm:"not null"`
	Consistent         bool   `gorm:"not null"`
	ConsistencyReason  string `gorm:"not null"`
	Accepted           bool   `gorm:"not null"`
	Boundary           string `gorm:"not null"`
	MetricsJSON        string `gorm:"not null"`
	UnattributedJSON   string `gorm:"not null;default:'null'"`
}

func (progressCheckpointModel) TableName() string { return "save_progress_checkpoints" }

type progressHeadModel struct {
	SavePlayerHex   string `gorm:"primaryKey"`
	CheckpointID    uint   `gorm:"not null"`
	PendingBoundary string `gorm:"not null;default:''"`
}

func (progressHeadModel) TableName() string { return "save_progress_heads" }

type progressChangeModel struct {
	ID                   uint   `gorm:"primaryKey"`
	SavePlayerHex        string `gorm:"not null;index:progress_changes_time,priority:1"`
	CheckpointID         uint   `gorm:"not null;uniqueIndex:progress_change_rule,priority:1"`
	PreviousCheckpointID uint   `gorm:"not null;uniqueIndex:progress_change_rule,priority:2"`
	Metric               string `gorm:"not null;uniqueIndex:progress_change_rule,priority:3"`
	RuleVersion          int    `gorm:"not null;uniqueIndex:progress_change_rule,priority:4"`
	Before               int64  `gorm:"not null"`
	After                int64  `gorm:"not null"`
	Delta                int64  `gorm:"not null"`
	AddedJSON            string `gorm:"not null"`
	RemovedJSON          string `gorm:"not null"`
	IntervalStart        string `gorm:"not null"`
	IntervalEnd          string `gorm:"not null;index:progress_changes_time,priority:2"`
}

func (progressChangeModel) TableName() string { return "save_progress_changes" }

func validateSaveProgress(s SaveSnapshot, p *SaveProgress) error {
	if p == nil {
		return nil
	}
	if p.SchemaVersion < 1 {
		return fmt.Errorf("invalid progress schema version")
	}
	at, err := time.Parse(time.RFC3339Nano, s.Source.SourceTime)
	if err != nil || at.IsZero() {
		return fmt.Errorf("progress source time is required")
	}
	captured, err := time.Parse(time.RFC3339Nano, s.Source.CapturedAt)
	if err != nil || captured.IsZero() {
		return fmt.Errorf("progress capture time is required")
	}
	if len(s.Source.WorldID) > 128 || strings.TrimSpace(s.Source.WorldID) != s.Source.WorldID {
		return fmt.Errorf("invalid progress world identity")
	}
	if s.Source.SourceTimeKind != "save_timestamp" && s.Source.SourceTimeKind != "file_mtime" {
		return fmt.Errorf("invalid progress source time kind")
	}
	if s.Source.WorldIDKind != "save" && s.Source.WorldIDKind != "directory" && s.Source.WorldIDKind != "explicit" && s.Source.WorldIDKind != "unknown" {
		return fmt.Errorf("invalid progress world identity kind")
	}
	if !progressReasonPattern.MatchString(s.Source.ConsistencyReason) {
		return fmt.Errorf("invalid progress consistency reason")
	}
	if s.Source.ProgressDefinition != "" {
		decoded, err := hex.DecodeString(s.Source.ProgressDefinition)
		if err != nil || len(decoded) != 32 {
			return fmt.Errorf("invalid progress definition hash")
		}
	}
	for name, m := range p.Metrics {
		if !slices.Contains(progressMetricNames, name) {
			return fmt.Errorf("unsupported progress metric %q", name)
		}
		if err := validateProgressMetric(m, isProgressSet(name)); err != nil {
			return err
		}
		if name == "level" && m.State == "known" && *m.Value == 0 {
			return fmt.Errorf("invalid progress level")
		}
	}
	if p.UnattributedPals != nil {
		return validateProgressMetric(*p.UnattributedPals, false)
	}
	return nil
}

func isProgressSet(name string) bool {
	return name == "owned_pals" || name == "paldeck" || name == "fast_travel"
}

func validateProgressMetric(m ProgressMetric, isSet bool) error {
	if !progressReasonPattern.MatchString(m.Reason) {
		return fmt.Errorf("invalid progress metric reason")
	}
	if m.State != "known" && m.State != "unknown" && m.State != "unsupported" {
		return fmt.Errorf("invalid progress metric state")
	}
	if m.State != "known" {
		if m.Value != nil || len(m.IDs) > 0 {
			return fmt.Errorf("unavailable progress metric contains a value")
		}
		return nil
	}
	if m.Value == nil || *m.Value < 0 || *m.Value > 9007199254740991 {
		return fmt.Errorf("invalid progress metric value")
	}
	if !isSet {
		if len(m.IDs) > 0 {
			return fmt.Errorf("progress counter is not a set")
		}
		return nil
	}
	if int64(len(m.IDs)) != *m.Value {
		return fmt.Errorf("progress set count mismatch")
	}
	seen := make(map[string]bool, len(m.IDs))
	for _, id := range m.IDs {
		if strings.TrimSpace(id) == "" || len(id) > 256 || seen[id] {
			return fmt.Errorf("invalid or duplicate progress set ID")
		}
		seen[id] = true
	}
	return nil
}

func normalizedProgressMetrics(input map[string]ProgressMetric) map[string]ProgressMetric {
	out := make(map[string]ProgressMetric, len(progressMetricNames))
	for _, name := range progressMetricNames {
		m, ok := input[name]
		if !ok {
			m = ProgressMetric{State: "unknown", Reason: "not_collected"}
		}
		m.IDs = slices.Clone(m.IDs)
		slices.Sort(m.IDs)
		out[name] = m
	}
	return out
}

func insertPlayerProgress(tx *gorm.DB, importID uint, s SaveSnapshot, player SavePlayer) error {
	if player.Progress == nil {
		// A legacy observation cannot establish continuity with the next checkpoint.
		return tx.Where("save_player_hex = ?", player.SavePlayerHex).Delete(&progressHeadModel{}).Error
	}
	p := player.Progress
	at, _ := time.Parse(time.RFC3339Nano, s.Source.SourceTime)
	captured, _ := time.Parse(time.RFC3339Nano, s.Source.CapturedAt)
	metrics := normalizedProgressMetrics(p.Metrics)
	raw, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	unattributed, err := json.Marshal(p.UnattributedPals)
	if err != nil {
		return err
	}
	current := progressCheckpointModel{ImportID: importID, SavePlayerHex: player.SavePlayerHex, WorldID: s.Source.WorldID, WorldIDKind: s.Source.WorldIDKind,
		ObservedAt: formatObservationTime(at), CapturedAt: formatObservationTime(captured), SourceTimeKind: s.Source.SourceTimeKind,
		SchemaVersion: p.SchemaVersion, ParserName: s.Parser.Name, ParserVersion: s.Parser.Version,
		ProgressDefinition: s.Source.ProgressDefinition,
		Consistent:         s.Source.Consistent, ConsistencyReason: s.Source.ConsistencyReason, Accepted: true, Boundary: "baseline", MetricsJSON: string(raw), UnattributedJSON: string(unattributed)}
	var previous progressCheckpointModel
	var head progressHeadModel
	err = tx.Where("save_player_hex = ?", player.SavePlayerHex).Take(&head).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	hasPrevious := err == nil
	if hasPrevious {
		if err := tx.First(&previous, head.CheckpointID).Error; err != nil {
			return err
		}
	}
	var old map[string]ProgressMetric
	if hasPrevious {
		if err := json.Unmarshal([]byte(previous.MetricsJSON), &old); err != nil {
			return err
		}
		switch {
		case current.ObservedAt <= previous.ObservedAt:
			current.Accepted = false
			current.Boundary = "out_of_order"
		case current.WorldID != previous.WorldID:
			current.Boundary = "world_changed"
		case current.SchemaVersion != previous.SchemaVersion || current.ParserName != previous.ParserName || current.ParserVersion != previous.ParserVersion || current.SourceTimeKind != previous.SourceTimeKind || current.WorldIDKind != previous.WorldIDKind || current.ProgressDefinition != previous.ProgressDefinition:
			current.Boundary = "schema_changed"
		case !previous.Consistent:
			current.Boundary = "after_inconsistent"
		case head.PendingBoundary != "":
			current.Boundary = head.PendingBoundary
		default:
			current.Boundary = ""
		}
	}
	if current.Accepted && !current.Consistent {
		current.Boundary = "inconsistent"
	}
	if current.Accepted && (current.WorldID == "" || current.WorldIDKind == "unknown") {
		current.Boundary = "world_unknown"
	}
	// Cumulative counters and unlock sets are monotonic under this rule version.
	// A regression is a boundary, not a negative capture/unlock activity.
	if current.Boundary == "" && progressCountersRegressed(old, metrics) {
		current.Boundary = "counter_reset"
	}
	if err := tx.Create(&current).Error; err != nil {
		return err
	}
	if !current.Accepted {
		return tx.Model(&head).Update("pending_boundary", "after_out_of_order").Error
	}
	if current.Boundary == "" {
		for _, name := range progressMetricNames {
			before, after := old[name], metrics[name]
			if before.State != "known" || after.State != "known" || before.Value == nil || after.Value == nil {
				continue
			}
			added, removed := []string{}, []string{}
			if isProgressSet(name) {
				// Legacy checkpoints can retain counts without complete membership.
				// Preserve numeric deltas, but null details must remain unknown.
				if completeProgressSet(before) && completeProgressSet(after) {
					added, removed = progressSetDiff(before.IDs, after.IDs)
				} else {
					added, removed = nil, nil
				}
			}
			if *before.Value == *after.Value && len(added) == 0 && len(removed) == 0 {
				continue
			}
			a, _ := json.Marshal(added)
			b, _ := json.Marshal(removed)
			change := progressChangeModel{SavePlayerHex: player.SavePlayerHex, CheckpointID: current.ID, PreviousCheckpointID: previous.ID,
				Metric: name, RuleVersion: 1, Before: *before.Value, After: *after.Value, Delta: *after.Value - *before.Value,
				AddedJSON: string(a), RemovedJSON: string(b), IntervalStart: previous.ObservedAt, IntervalEnd: current.ObservedAt}
			if err := tx.Create(&change).Error; err != nil {
				return err
			}
		}
	}
	head = progressHeadModel{SavePlayerHex: player.SavePlayerHex, CheckpointID: current.ID}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "save_player_hex"}}, DoUpdates: clause.AssignmentColumns([]string{"checkpoint_id", "pending_boundary"})}).Create(&head).Error
}

// Replaying the current observation is harmless. Replaying an older one may
// be a restored save or a manual backfill: retain the head but break continuity.
func markProgressReplay(tx *gorm.DB, importID uint) error {
	return tx.Model(&progressHeadModel{}).
		Where("save_player_hex IN (SELECT save_player_hex FROM save_players WHERE import_id = ?)", importID).
		Where("checkpoint_id IN (SELECT id FROM save_progress_checkpoints WHERE import_id <> ?)", importID).
		Update("pending_boundary", "replayed_snapshot").Error
}

func progressCountersRegressed(before, after map[string]ProgressMetric) bool {
	for _, name := range []string{"capture_total", "paldeck", "fast_travel", "level", "experience"} {
		a, b := before[name], after[name]
		if a.State != "known" || b.State != "known" || a.Value == nil || b.Value == nil {
			continue
		}
		if *b.Value < *a.Value {
			return true
		}
		if isProgressSet(name) && completeProgressSet(a) && completeProgressSet(b) {
			_, removed := progressSetDiff(a.IDs, b.IDs)
			if len(removed) > 0 {
				return true
			}
		}
	}
	return false
}

func completeProgressSet(metric ProgressMetric) bool {
	return metric.State == "known" && validateProgressMetric(metric, true) == nil
}

func progressSetDiff(before, after []string) ([]string, []string) {
	added, removed := []string{}, []string{}
	old, next := make(map[string]bool, len(before)), make(map[string]bool, len(after))
	for _, id := range before {
		old[id] = true
	}
	for _, id := range after {
		next[id] = true
		if !old[id] {
			added = append(added, id)
		}
	}
	for _, id := range before {
		if !next[id] {
			removed = append(removed, id)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	return added, removed
}

func (m progressCheckpointModel) dto() (ProgressCheckpoint, error) {
	out := ProgressCheckpoint{ID: m.ID, WorldID: m.WorldID, ObservedAt: m.ObservedAt, CapturedAt: m.CapturedAt, Source: "save_import", SourceTimeKind: m.SourceTimeKind, SchemaVersion: m.SchemaVersion, Consistent: m.Consistent, Boundary: m.Boundary}
	err := json.Unmarshal([]byte(m.MetricsJSON), &out.Metrics)
	if err == nil {
		out.Metrics = normalizedProgressMetrics(out.Metrics)
	}
	if err == nil && m.UnattributedJSON != "" {
		err = json.Unmarshal([]byte(m.UnattributedJSON), &out.UnattributedPals)
	}
	return out, err
}

// ReadPlayerProgress returns a bounded window and its preceding observation.
// Identity is resolved afresh and must be unique; late REST discovery is safe,
// while nickname matches and stale/ambiguous mapping-table rows are never used.
func (r *Repository) ReadPlayerProgress(ctx context.Context, userID string, start, end time.Time, limit int) (PlayerProgress, error) {
	out := PlayerProgress{UserID: userID, Status: "not_collected", Checkpoints: []ProgressCheckpoint{}, Changes: []ProgressChange{}}
	if start.IsZero() || !start.Before(end) || end.Sub(start) > 31*24*time.Hour || limit < 1 || limit > 500 {
		return out, fmt.Errorf("invalid progress range")
	}
	err := r.gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var player struct{ PlayerID string }
		err := tx.Table("players").Select("player_id").Where("user_id = ?", userID).Take(&player).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var n int64
		if err := tx.Table("players").Where("player_id = ?", player.PlayerID).Count(&n).Error; err != nil {
			return err
		}
		if !saveHexPattern.MatchString(player.PlayerID) || n != 1 {
			out.Status = "identity_unknown"
			return nil
		}
		from, to := formatObservationTime(start), formatObservationTime(end)
		var baseline progressCheckpointModel
		err = tx.Where("save_player_hex = ? AND accepted = ? AND observed_at < ?", player.PlayerID, true, from).Order("observed_at DESC, id DESC").Take(&baseline).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			dto, err := baseline.dto()
			if err != nil {
				return err
			}
			out.Baseline = &dto
		}
		query := func() *gorm.DB {
			return tx.Model(&progressCheckpointModel{}).Where("save_player_hex = ? AND accepted = ? AND observed_at >= ? AND observed_at < ?", player.PlayerID, true, from, to)
		}
		if err := query().Count(&out.CheckpointTotal).Error; err != nil {
			return err
		}
		var checkpoints []progressCheckpointModel
		if err := query().Order("observed_at DESC, id DESC").Limit(limit).Find(&checkpoints).Error; err != nil {
			return err
		}
		slices.Reverse(checkpoints)
		for _, m := range checkpoints {
			dto, err := m.dto()
			if err != nil {
				return err
			}
			out.Checkpoints = append(out.Checkpoints, dto)
		}
		changes := func() *gorm.DB {
			return tx.Model(&progressChangeModel{}).Where("save_player_hex = ? AND interval_end >= ? AND interval_end < ?", player.PlayerID, from, to)
		}
		if err := changes().Count(&out.ChangeTotal).Error; err != nil {
			return err
		}
		var models []progressChangeModel
		if err := changes().Order("interval_end DESC, id DESC").Limit(limit).Find(&models).Error; err != nil {
			return err
		}
		for _, m := range models {
			c := ProgressChange{ID: m.ID, Metric: m.Metric, Before: m.Before, After: m.After, Delta: m.Delta, IntervalStart: m.IntervalStart, IntervalEnd: m.IntervalEnd,
				PreviousCheckpointID: m.PreviousCheckpointID, CheckpointID: m.CheckpointID, RuleVersion: m.RuleVersion, Confidence: "observed", Source: "save_import"}
			if err := json.Unmarshal([]byte(m.AddedJSON), &c.Added); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(m.RemovedJSON), &c.Removed); err != nil {
				return err
			}
			out.Changes = append(out.Changes, c)
		}
		if out.Baseline != nil || len(out.Checkpoints) > 0 {
			out.Status = "available"
		}
		return nil
	})
	return out, err
}
