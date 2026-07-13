package observation_test

import (
	"fmt"
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

func TestServerRestartBreaksTrajectoryForEitherAsyncCommitOrder(t *testing.T) {
	for _, playerFirst := range []bool{true, false} {
		name := "restart metrics first"
		if playerFirst {
			name = "player boundary sample first"
		}
		t.Run(name, func(t *testing.T) {
			repo, _ := openServerRepository(t)
			defer repo.Close()
			ids := 0
			newID := func() string { ids++; return fmt.Sprintf("id-%d", ids) }
			players := observation.New(repo, 75*time.Second, 100, 5*time.Minute, 90*24*time.Hour, newID)
			server := observation.NewServer(repo, newID)
			base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
			boundary := base.Add(time.Minute)
			if err := server.RecordMetrics(t.Context(), base, domain.ServerMetrics{UptimeSeconds: 100, ServerFrameTime: 1}); err != nil {
				t.Fatal(err)
			}
			initial := domain.Player{UserID: "u", LocationX: 10, LocationY: 10, Level: 10, Ping: 20}
			if err := players.Observe(t.Context(), base, []domain.Player{initial}, "players-1"); err != nil {
				t.Fatal(err)
			}
			moved := initial
			moved.LocationX = 211
			restart := func() {
				if err := server.RecordMetrics(t.Context(), boundary, domain.ServerMetrics{UptimeSeconds: 1, ServerFrameTime: 1}); err != nil {
					t.Fatal(err)
				}
			}
			observe := func() {
				if err := players.Observe(t.Context(), boundary, []domain.Player{moved}, "players-2"); err != nil {
					t.Fatal(err)
				}
			}
			if playerFirst {
				observe()
				restart()
			} else {
				restart()
				observe()
			}
			timeline, err := repo.ReadSensitivePlayerTimeline(t.Context(), "admin", "u", base.Add(-time.Second), boundary.Add(time.Second), 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(timeline.Trajectories) != 2 || timeline.Trajectories[0].SegmentID == timeline.Trajectories[1].SegmentID {
				t.Fatalf("trajectories=%+v", timeline.Trajectories)
			}
		})
	}
}
