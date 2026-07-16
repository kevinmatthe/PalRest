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
	export  *store.MetricsExport
}

func (m metricsAdminStore) LatestServerMetrics(context.Context) (time.Time, domain.ServerMetrics, error) {
	if m.err != nil {
		return time.Time{}, domain.ServerMetrics{}, m.err
	}
	return m.at, m.metrics, nil
}

func (m metricsAdminStore) LoadMetricsExport(context.Context) (store.MetricsExport, error) {
	if m.export != nil {
		return *m.export, nil
	}
	if m.err != nil {
		return store.MetricsExport{}, m.err
	}
	mm := m.metrics
	return store.MetricsExport{
		ServerAt: m.at,
		Server:   &mm,
		Runtime:  store.ServerRuntimeState{Epoch: 3, RestartedAt: m.at.Add(-time.Hour)},
		RestartEvents: 2,
		InfoAt: m.at,
		Info: &domain.ServerInfo{Version: "v1.2.3", ServerName: "TestWorld", WorldGUID: "guid-1"},
		SettingsAt: m.at,
		SettingsScalar: map[string]float64{"ExpRate": 2, "bEnableInvaderEnemy": 1},
		Save: &store.SaveMetricsExport{
			ImportID: 9, ImportedAt: m.at.Add(-2 * time.Hour), LevelSAVSize: 1024,
			PlayerCount: 1, GuildCount: 1, BaseCampCount: 1, BaseCampAreaSum: 50,
			Players: []store.SavePlayerMetric{{
				UserID: "u1", SaveUID: "sp1", Name: "Anu", Level: 63, Exp: 1000,
				HP: 90, ShieldHP: 20, FullStomach: 74.5, LastOnline: m.at.Add(-time.Minute),
			}},
			Guilds: []store.SaveGuildMetric{{
				GuildID: "g1", Name: "Base", BaseCampLevel: 10, MemberCount: 2, BaseCampCount: 1, BaseCampArea: 50,
			}},
		},
	}, nil
}

func TestPrometheusMetricsExposesServerAndPerPlayerSeries(t *testing.T) {
	server := testServer()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	server.now = func() time.Time { return now }
	server.adminStore = metricsAdminStore{
		at: now,
		metrics: domain.ServerMetrics{
			ServerFPS: 58, CurrentPlayerNum: 2, ServerFrameTime: 17.2,
			MaxPlayerNum: 32, UptimeSeconds: 3600, BaseCampNum: 3, Days: 100,
		},
	}
	server.snapshots = fakeSnapshots{[]domain.PlayerSnapshot{
		{
			Player: domain.Player{UserID: "u1", Name: "Anu", Ping: 42, Level: 10, LocationX: 3000, LocationY: 4000, LastOnline: now},
			Policy: domain.ResolvedPolicy{Enabled: true, Limit: 2 * time.Hour},
			Used:   30 * time.Minute, Remaining: 90 * time.Minute, Online: true,
			Enforcement: domain.EnforcementState{Status: "ok"},
		},
		{
			Player: domain.Player{UserID: "u2", Name: `Bo "Lag"`, Ping: 180, Level: 20, LocationX: 0, LocationY: 0},
			Policy: domain.ResolvedPolicy{Enabled: true, Exempt: true, Limit: time.Hour},
			Used:   10 * time.Minute, Remaining: 50 * time.Minute, Online: true,
			Warnings: []domain.WarningState{{Status: "pending"}},
			Enforcement: domain.EnforcementState{Status: "warn"},
		},
		{
			Player: domain.Player{UserID: "u3", Name: "Offline"},
			Policy: domain.ResolvedPolicy{Limit: time.Hour},
			Used:   5 * time.Minute, Remaining: 55 * time.Minute, Online: false,
		},
	}}
	server.status = fakeStatus{domain.PollStatus{OnlineCount: 2, LastSuccess: now, StartedAt: now.Add(-time.Hour), ConfigVersion: 7}}

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, want := range []string{
		"palrest_up 1",
		"palrest_online_players 2",
		"palrest_server_fps 58",
		"palrest_server_frame_time_milliseconds 17.2",
		"palrest_server_runtime_epoch 3",
		"palrest_server_restarts_total 2",
		`palrest_server_info{server_name="TestWorld",version="v1.2.3",world_guid="guid-1"} 1`,
		`palrest_server_setting{key="ExpRate"} 2`,
		`palrest_player_online{name="Anu",user_id="u1"} 1`,
		`palrest_player_ping_milliseconds{name="Anu",user_id="u1"} 42`,
		`palrest_player_ping_milliseconds{name="Bo \"Lag\"",user_id="u2"} 180`,
		`palrest_player_online{name="Offline",user_id="u3"} 0`,
		`palrest_player_used_seconds{name="Anu",user_id="u1"} 1800`,
		`palrest_player_policy_exempt{name="Bo \"Lag\"",user_id="u2"} 1`,
		`palrest_player_warning_active{name="Bo \"Lag\"",user_id="u2"} 1`,
		`palrest_player_enforcement{name="Anu",status="ok",user_id="u1"} 1`,
		`palrest_player_location_x{name="Anu",user_id="u1"} 3000`,
		`palrest_player_location_y{name="Anu",user_id="u1"} 4000`,
		`palrest_player_distance_from_origin{name="Anu",user_id="u1"} 5000`,
		"palrest_save_player_count 1",
		`palrest_save_player_level{name="Anu",save_uid="sp1",user_id="u1"} 63`,
		`palrest_save_player_full_stomach{name="Anu",save_uid="sp1",user_id="u1"} 74.5`,
		`palrest_save_guild_members{guild="Base",guild_id="g1"} 2`,
		"palrest_known_players 3",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, `palrest_player_ping_milliseconds{name="Offline"`) {
		t.Fatalf("offline ping should be absent:\n%s", body)
	}
}

func TestPrometheusMetricsWorksWithoutServerMetrics(t *testing.T) {
	server := testServer()
	server.adminStore = metricsAdminStore{err: store.ErrNotFound, export: &store.MetricsExport{}}
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, "palrest_up 1") || strings.Contains(body, "palrest_server_fps ") {
		t.Fatalf("body=%s", body)
	}
}
