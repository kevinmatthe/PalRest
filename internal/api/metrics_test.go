package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type metricsAdminStore struct {
	fakeAdminStore
	at      time.Time
	metrics domain.ServerMetrics
	err     error
}

func (m metricsAdminStore) LatestServerMetrics(context.Context) (time.Time, domain.ServerMetrics, error) {
	if m.err != nil {
		return time.Time{}, domain.ServerMetrics{}, m.err
	}
	return m.at, m.metrics, nil
}

func TestPrometheusMetricsExposesServerAndPerPlayerSeries(t *testing.T) {
	server := testServer()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	server.adminStore = metricsAdminStore{
		at: now,
		metrics: domain.ServerMetrics{
			ServerFPS: 58, CurrentPlayerNum: 2, ServerFrameTime: 17.2,
			MaxPlayerNum: 32, UptimeSeconds: 3600, BaseCampNum: 3, Days: 100,
		},
	}
	server.snapshots = fakeSnapshots{[]domain.PlayerSnapshot{
		{
			Player: domain.Player{UserID: "u1", Name: "Anu", Ping: 42, Level: 10},
			Policy: domain.ResolvedPolicy{Limit: 2 * time.Hour},
			Used:   30 * time.Minute, Remaining: 90 * time.Minute, Online: true,
		},
		{
			Player: domain.Player{UserID: "u2", Name: `Bo "Lag"`, Ping: 180, Level: 20},
			Policy: domain.ResolvedPolicy{Limit: time.Hour},
			Used:   10 * time.Minute, Remaining: 50 * time.Minute, Online: true,
		},
		{
			Player: domain.Player{UserID: "u3", Name: "Offline"},
			Policy: domain.ResolvedPolicy{Limit: time.Hour},
			Used:   5 * time.Minute, Remaining: 55 * time.Minute, Online: false,
		},
	}}
	server.status = fakeStatus{domain.PollStatus{OnlineCount: 2, LastSuccess: now}}

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	ct := res.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Fatalf("content-type=%q", ct)
	}
	body := res.Body.String()
	for _, want := range []string{
		"palrest_up 1",
		"palrest_online_players 2",
		"palrest_server_fps 58",
		"palrest_server_frame_time_milliseconds 17.2",
		`palrest_player_online{name="Anu",user_id="u1"} 1`,
		`palrest_player_ping_milliseconds{name="Anu",user_id="u1"} 42`,
		`palrest_player_ping_milliseconds{name="Bo \"Lag\"",user_id="u2"} 180`,
		`palrest_player_online{name="Offline",user_id="u3"} 0`,
		`palrest_player_used_seconds{name="Anu",user_id="u1"} 1800`,
		`palrest_player_remaining_seconds{name="Anu",user_id="u1"} 5400`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
	// Offline players must not emit ping (stale series stay out of Grafana).
	if strings.Contains(body, `palrest_player_ping_milliseconds{name="Offline"`) {
		t.Fatalf("offline ping should be absent:\n%s", body)
	}
}

func TestPrometheusMetricsWorksWithoutServerMetrics(t *testing.T) {
	server := testServer()
	server.adminStore = metricsAdminStore{err: store.ErrNotFound}
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, "palrest_up 1") || strings.Contains(body, "palrest_server_fps") {
		t.Fatalf("body=%s", body)
	}
}
