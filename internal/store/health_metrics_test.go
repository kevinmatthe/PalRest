package store

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

func TestSummarizePingsPercentiles(t *testing.T) {
	at := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// 1..100
	pings := make([]float64, 0, 100)
	for i := 1; i <= 100; i++ {
		pings = append(pings, float64(i))
	}
	s := SummarizePings(at, pings, 2)
	if s.SampleCount != 100 || s.MissingCount != 2 {
		t.Fatalf("counts sample=%d missing=%d", s.SampleCount, s.MissingCount)
	}
	if s.Min == nil || *s.Min != 1 || s.Max == nil || *s.Max != 100 {
		t.Fatalf("min/max=%v/%v", s.Min, s.Max)
	}
	if s.P50 == nil || *s.P50 != 50 {
		t.Fatalf("p50=%v", s.P50)
	}
	if s.P90 == nil || *s.P90 != 90 {
		t.Fatalf("p90=%v", s.P90)
	}
	if s.P99 == nil || *s.P99 != 99 {
		t.Fatalf("p99=%v", s.P99)
	}
}

func TestSummarizePingsEmpty(t *testing.T) {
	s := SummarizePings(time.Now().UTC(), []float64{math.NaN(), -1}, 1)
	if s.SampleCount != 0 || s.MissingCount != 3 || s.P50 != nil {
		t.Fatalf("%+v", s)
	}
}

func TestRecordAndQueryHealthSeries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "health.db")
	repo, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	ctx := t.Context()
	base := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)

	m := domain.ServerMetrics{
		ServerFPS: 60, CurrentPlayerNum: 3, ServerFrameTime: 16,
		MaxPlayerNum: 32, UptimeSeconds: 100, BaseCampNum: 1, Days: 1,
	}
	if err := repo.RecordServerMetrics(ctx, base, m); err != nil {
		t.Fatal(err)
	}
	m.ServerFPS = 45
	m.UptimeSeconds = 160
	if err := repo.RecordServerMetrics(ctx, base.Add(time.Minute), m); err != nil {
		t.Fatal(err)
	}

	p50, p90 := 40.0, 90.0
	if err := repo.RecordPingSummary(ctx, PingSummaryInput{
		At: base, SampleCount: 5, MissingCount: 1, P50: &p50, P90: &p90, Max: &p90, Min: &p50, P99: &p90,
	}); err != nil {
		t.Fatal(err)
	}

	fps, err := repo.ServerFPSSeries(ctx, base.Add(-time.Minute), base.Add(2*time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(fps) != 2 || fps[0].FPS != 60 || fps[1].FPS != 45 {
		t.Fatalf("fps=%+v", fps)
	}
	ping, err := repo.PingSummarySeries(ctx, base.Add(-time.Minute), base.Add(2*time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ping) != 1 || ping[0].SampleCount != 5 || ping[0].P50 == nil || *ping[0].P50 != 40 {
		t.Fatalf("ping=%+v", ping)
	}

	if err := repo.RecordPlayerObservation(ctx, PlayerObservationWrite{
		PrivateSamples: []PlayerPrivateSample{
			{UserID: "u1", ObservedAt: base, IP: "192.0.2.1", Ping: 35, Level: 1, SourceRef: "poll"},
			{UserID: "u1", ObservedAt: base.Add(time.Minute), IP: "192.0.2.1", Ping: 80, Level: 1, SourceRef: "poll"},
			{UserID: "u2", ObservedAt: base.Add(time.Minute), IP: "192.0.2.2", Ping: 120, Level: 2, SourceRef: "poll"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	series, err := repo.PlayerPingSeries(ctx, "u1", base.Add(-time.Minute), base.Add(2*time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 2 || series[0].Ping != 35 || series[1].Ping != 80 {
		t.Fatalf("player series=%+v", series)
	}
	latest, err := repo.LatestPlayerPings(ctx, base.Add(-time.Minute), base.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 || latest[0].UserID != "u2" || latest[0].Ping != 120 || latest[1].UserID != "u1" || latest[1].Ping != 80 {
		t.Fatalf("latest=%+v", latest)
	}
}
