package behavior

import (
	"testing"
	"time"
)

func TestSummarizeTraveling(t *testing.T) {
	t0 := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	points := []Point{
		{ObservedAt: t0, SegmentID: "s1", X: 0, Y: 0},
		{ObservedAt: t0.Add(time.Minute), SegmentID: "s1", X: 100_000, Y: 0},
	}
	s := Summarize(points, t0, t0.Add(5*time.Minute))
	if s.DominantClass != "traveling" {
		t.Fatalf("dominant=%s", s.DominantClass)
	}
	if s.PathLength < 99_000 || s.TravelingShare < 0.99 {
		t.Fatalf("path=%v share=%v", s.PathLength, s.TravelingShare)
	}
}

func TestSummarizeStationary(t *testing.T) {
	t0 := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	points := []Point{
		{ObservedAt: t0, SegmentID: "s1", X: 0, Y: 0},
		{ObservedAt: t0.Add(2 * time.Minute), SegmentID: "s1", X: 10, Y: 0},
	}
	s := Summarize(points, t0, t0.Add(10*time.Minute))
	if s.DominantClass != "stationary" {
		t.Fatalf("dominant=%s", s.DominantClass)
	}
}

func TestSummarizeCrossSegmentGap(t *testing.T) {
	t0 := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	points := []Point{
		{ObservedAt: t0, SegmentID: "s1", X: 0, Y: 0},
		{ObservedAt: t0.Add(time.Minute), SegmentID: "s2", X: 100_000, Y: 0},
	}
	s := Summarize(points, t0, t0.Add(5*time.Minute))
	if s.PathLength != 0 || s.ObservedActiveMs != 0 || s.GapMs != 60_000 {
		t.Fatalf("summary=%+v", s)
	}
}

func TestDominantTieBreak(t *testing.T) {
	if got := dominant(ClassMs{Stationary: 10, Local: 10, Traveling: 10}); got != "traveling" {
		t.Fatalf("got %s", got)
	}
}
