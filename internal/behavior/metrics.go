// Package behavior summarizes trajectory samples with the same edge rules as webui/src/behavior.
package behavior

import (
	"math"
	"sort"
	"time"
)

const (
	DIdle       = 500.0
	VIdle       = 50.0
	VTravel     = 800.0
	TGap        = 5 * time.Minute
	TActiveCap  = TGap
	MaxSamples  = 500
	DefaultLimit = 25
)

type Point struct {
	ObservedAt time.Time
	SegmentID  string
	X, Y       float64
}

type ClassMs struct {
	Stationary int64 `json:"stationary"`
	Local      int64 `json:"local"`
	Traveling  int64 `json:"traveling"`
}

type Summary struct {
	SampleCount         int     `json:"sample_count"`
	SegmentCount        int     `json:"segment_count"`
	WindowMs            int64   `json:"window_ms"`
	ObservedActiveMs    int64   `json:"observed_active_ms"`
	PathLength          float64 `json:"path_length"`
	Radius              float64 `json:"radius"`
	MeanSpeed           float64 `json:"mean_speed"`
	PeakSpeed           float64 `json:"peak_speed"`
	SampleDensityPerHour float64 `json:"sample_density_per_hour"`
	ClassMs             ClassMs `json:"class_ms"`
	TravelingShare      float64 `json:"traveling_share"`
	LocalShare          float64 `json:"local_share"`
	StationaryShare     float64 `json:"stationary_share"`
	GapMs               int64   `json:"gap_ms"`
	DominantClass       string  `json:"dominant_class"`
}

// Summarize classifies consecutive edges and aggregates motion metrics.
func Summarize(points []Point, windowStart, windowEnd time.Time) Summary {
	sorted := make([]Point, 0, len(points))
	for _, p := range points {
		if p.ObservedAt.IsZero() || math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
			continue
		}
		sorted = append(sorted, p)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ObservedAt.Before(sorted[j].ObservedAt)
	})

	if windowEnd.Before(windowStart) {
		windowStart, windowEnd = windowEnd, windowStart
	}
	windowMs := windowEnd.Sub(windowStart).Milliseconds()
	if windowMs < 0 {
		windowMs = 0
	}
	if len(sorted) == 0 {
		return Summary{WindowMs: windowMs, DominantClass: "unknown"}
	}
	if windowStart.IsZero() {
		windowStart = sorted[0].ObservedAt
	}
	if windowEnd.IsZero() || !windowEnd.After(windowStart) {
		windowEnd = sorted[len(sorted)-1].ObservedAt
		if !windowEnd.After(windowStart) {
			windowEnd = windowStart.Add(time.Millisecond)
		}
		windowMs = windowEnd.Sub(windowStart).Milliseconds()
	}

	var class ClassMs
	var gapMs, observedActiveMs, movingMs int64
	var pathLength, peakSpeed float64
	segments := map[string]struct{}{}
	for _, p := range sorted {
		segments[p.SegmentID] = struct{}{}
	}

	for i := 0; i < len(sorted)-1; i++ {
		a, b := sorted[i], sorted[i+1]
		dt := b.ObservedAt.Sub(a.ObservedAt)
		if dt <= 0 {
			continue
		}
		dtMs := dt.Milliseconds()
		if a.SegmentID != b.SegmentID || dt > TGap {
			gapMs += dtMs
			continue
		}
		dist := math.Hypot(b.X-a.X, b.Y-a.Y)
		speed := dist / dt.Seconds()
		capped := dtMs
		if time.Duration(capped)*time.Millisecond > TActiveCap {
			capped = TActiveCap.Milliseconds()
		}
		observedActiveMs += capped
		pathLength += dist
		if speed > peakSpeed {
			peakSpeed = speed
		}
		switch {
		case dist < DIdle || speed < VIdle:
			class.Stationary += capped
		case speed >= VTravel:
			class.Traveling += capped
			movingMs += capped
		default:
			class.Local += capped
			movingMs += capped
		}
	}

	var cx, cy float64
	for _, p := range sorted {
		cx += p.X
		cy += p.Y
	}
	n := float64(len(sorted))
	cx /= n
	cy /= n
	var radius float64
	for _, p := range sorted {
		d := math.Hypot(p.X-cx, p.Y-cy)
		if d > radius {
			radius = d
		}
	}

	meanSpeed := 0.0
	if movingMs > 0 {
		meanSpeed = pathLength / (float64(movingMs) / 1000.0)
	}
	density := 0.0
	if observedActiveMs > 0 {
		density = float64(len(sorted)) / (float64(observedActiveMs) / 3_600_000.0)
	}

	sum := Summary{
		SampleCount:          len(sorted),
		SegmentCount:         len(segments),
		WindowMs:             windowMs,
		ObservedActiveMs:     observedActiveMs,
		PathLength:           pathLength,
		Radius:               radius,
		MeanSpeed:            meanSpeed,
		PeakSpeed:            peakSpeed,
		SampleDensityPerHour: density,
		ClassMs:              class,
		GapMs:                gapMs,
		DominantClass:        dominant(class),
	}
	if observedActiveMs > 0 {
		sum.TravelingShare = float64(class.Traveling) / float64(observedActiveMs)
		sum.LocalShare = float64(class.Local) / float64(observedActiveMs)
		sum.StationaryShare = float64(class.Stationary) / float64(observedActiveMs)
	}
	return sum
}

func dominant(c ClassMs) string {
	type pair struct {
		name string
		ms   int64
	}
	// Tie-break: traveling > local > stationary
	order := []pair{
		{"traveling", c.Traveling},
		{"local", c.Local},
		{"stationary", c.Stationary},
	}
	best := "unknown"
	var bestMs int64
	for _, p := range order {
		if p.ms > bestMs {
			bestMs = p.ms
			best = p.name
		}
	}
	if bestMs == 0 {
		return "unknown"
	}
	return best
}
