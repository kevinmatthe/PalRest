package observation_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/observation"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

func TestServerRestartDetectionSurvivesRawRetentionAndReopen(t *testing.T) {
	repo, path := openServerRepository(t)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	service := observation.NewServer(repo, observation.NewID)
	if err := service.RecordMetrics(t.Context(), old, domain.ServerMetrics{UptimeSeconds: 100, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := repo.CleanupRawObservations(t.Context(), old.Add(100*24*time.Hour), 100); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	service = observation.NewServer(reopened, func() string { return "restart-after-retention" })
	if err := service.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordMetrics(t.Context(), old.Add(101*24*time.Hour), domain.ServerMetrics{UptimeSeconds: 1, ServerFrameTime: 1}); err != nil {
		t.Fatal(err)
	}
	latest, err := reopened.LatestServerMetricObservation(t.Context())
	if err != nil || latest.Event == nil || latest.Event.EventType != "server_restarted" || !strings.Contains(latest.Event.PayloadJSON, `"old_uptime_seconds":100`) {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
}
