package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"
)

// ServerFPSPoint is one server metrics sample for health charts.
type ServerFPSPoint struct {
	At        time.Time
	FPS       int
	FrameTime float64
	Players   int
}

// PingSummaryPoint is a per-poll online latency summary.
type PingSummaryPoint struct {
	At           time.Time
	SampleCount  int
	MissingCount int
	Min          *float64
	P50          *float64
	P90          *float64
	P99          *float64
	Max          *float64
}

// PingSummaryInput is the write shape for one poll.
type PingSummaryInput struct {
	At           time.Time
	SampleCount  int
	MissingCount int
	Min          *float64
	P50          *float64
	P90          *float64
	P99          *float64
	Max          *float64
}

// SummarizePings builds a ping summary from online player pings.
// Unknown / invalid pings count as missing.
func SummarizePings(at time.Time, pings []float64, missing int) PingSummaryInput {
	out := PingSummaryInput{At: at.UTC(), MissingCount: missing}
	valid := make([]float64, 0, len(pings))
	for _, p := range pings {
		if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 {
			out.MissingCount++
			continue
		}
		valid = append(valid, p)
	}
	out.SampleCount = len(valid)
	if len(valid) == 0 {
		return out
	}
	sort.Float64s(valid)
	minV := valid[0]
	maxV := valid[len(valid)-1]
	p50 := percentileSorted(valid, 0.50)
	p90 := percentileSorted(valid, 0.90)
	p99 := percentileSorted(valid, 0.99)
	out.Min, out.Max = &minV, &maxV
	out.P50, out.P90, out.P99 = &p50, &p90, &p99
	return out
}

func percentileSorted(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	// Nearest-rank method.
	rank := int(math.Ceil(p*float64(n))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= n {
		rank = n - 1
	}
	return sorted[rank]
}

// RecordPingSummary upserts one poll latency summary (idempotent on observed_at).
func (r *Repository) RecordPingSummary(ctx context.Context, in PingSummaryInput) error {
	if in.At.IsZero() {
		return fmt.Errorf("record ping summary: observation time is zero")
	}
	if in.SampleCount < 0 || in.MissingCount < 0 {
		return fmt.Errorf("record ping summary: counts must be nonnegative")
	}
	at := formatObservationTime(in.At.UTC())
	_, err := r.db.ExecContext(ctx, `
INSERT INTO ping_summary_samples(
  observed_at,sample_count,missing_count,ping_min,ping_p50,ping_p90,ping_p99,ping_max
) VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(observed_at) DO UPDATE SET
  sample_count=excluded.sample_count,
  missing_count=excluded.missing_count,
  ping_min=excluded.ping_min,
  ping_p50=excluded.ping_p50,
  ping_p90=excluded.ping_p90,
  ping_p99=excluded.ping_p99,
  ping_max=excluded.ping_max
`, at, in.SampleCount, in.MissingCount, nullFloat(in.Min), nullFloat(in.P50), nullFloat(in.P90), nullFloat(in.P99), nullFloat(in.Max))
	if err != nil {
		return fmt.Errorf("record ping summary: %w", err)
	}
	return nil
}

func nullFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

// ServerFPSSeries returns raw server metric samples in [start, end).
func (r *Repository) ServerFPSSeries(ctx context.Context, start, end time.Time, limit int) ([]ServerFPSPoint, error) {
	if err := validateHealthRange(start, end, limit); err != nil {
		return nil, fmt.Errorf("server fps series: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT observed_at,server_fps,server_frame_time,current_player_num
FROM server_metric_samples
WHERE observed_at>=? AND observed_at<?
ORDER BY observed_at ASC
LIMIT ?`, formatObservationTime(start.UTC()), formatObservationTime(end.UTC()), limit)
	if err != nil {
		return nil, fmt.Errorf("server fps series: query: %w", err)
	}
	defer rows.Close()
	out := make([]ServerFPSPoint, 0)
	for rows.Next() {
		var p ServerFPSPoint
		var at string
		if err := rows.Scan(&at, &p.FPS, &p.FrameTime, &p.Players); err != nil {
			return nil, fmt.Errorf("server fps series: scan: %w", err)
		}
		p.At, err = parseTime(at)
		if err != nil {
			return nil, fmt.Errorf("server fps series: parse time: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("server fps series: iterate: %w", err)
	}
	return downsampleFPS(out, 720), nil
}

// PingSummarySeries returns latency summaries in [start, end).
func (r *Repository) PingSummarySeries(ctx context.Context, start, end time.Time, limit int) ([]PingSummaryPoint, error) {
	if err := validateHealthRange(start, end, limit); err != nil {
		return nil, fmt.Errorf("ping summary series: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT observed_at,sample_count,missing_count,ping_min,ping_p50,ping_p90,ping_p99,ping_max
FROM ping_summary_samples
WHERE observed_at>=? AND observed_at<?
ORDER BY observed_at ASC
LIMIT ?`, formatObservationTime(start.UTC()), formatObservationTime(end.UTC()), limit)
	if err != nil {
		return nil, fmt.Errorf("ping summary series: query: %w", err)
	}
	defer rows.Close()
	out := make([]PingSummaryPoint, 0)
	for rows.Next() {
		var p PingSummaryPoint
		var at string
		var min, p50, p90, p99, max sql.NullFloat64
		if err := rows.Scan(&at, &p.SampleCount, &p.MissingCount, &min, &p50, &p90, &p99, &max); err != nil {
			return nil, fmt.Errorf("ping summary series: scan: %w", err)
		}
		p.At, err = parseTime(at)
		if err != nil {
			return nil, fmt.Errorf("ping summary series: parse time: %w", err)
		}
		p.Min, p.P50, p.P90, p.P99, p.Max = scanOptFloat(min), scanOptFloat(p50), scanOptFloat(p90), scanOptFloat(p99), scanOptFloat(max)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ping summary series: iterate: %w", err)
	}
	return downsamplePing(out, 720), nil
}

func scanOptFloat(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	x := v.Float64
	return &x
}

func validateHealthRange(start, end time.Time, limit int) error {
	if start.IsZero() || end.IsZero() {
		return fmt.Errorf("start and end are required")
	}
	if !end.After(start) {
		return fmt.Errorf("end must be after start")
	}
	if limit <= 0 || limit > 20_000 {
		return fmt.Errorf("limit must be between 1 and 20000")
	}
	return nil
}

func downsampleFPS(in []ServerFPSPoint, max int) []ServerFPSPoint {
	if len(in) <= max {
		return in
	}
	out := make([]ServerFPSPoint, 0, max)
	step := float64(len(in)-1) / float64(max-1)
	for i := 0; i < max; i++ {
		idx := int(math.Round(float64(i) * step))
		if idx >= len(in) {
			idx = len(in) - 1
		}
		out = append(out, in[idx])
	}
	return out
}

func downsamplePing(in []PingSummaryPoint, max int) []PingSummaryPoint {
	if len(in) <= max {
		return in
	}
	out := make([]PingSummaryPoint, 0, max)
	step := float64(len(in)-1) / float64(max-1)
	for i := 0; i < max; i++ {
		idx := int(math.Round(float64(i) * step))
		if idx >= len(in) {
			idx = len(in) - 1
		}
		out = append(out, in[idx])
	}
	return out
}
