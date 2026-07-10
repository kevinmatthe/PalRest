package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/config"
	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type fakeHealth struct{ err error }

func (f fakeHealth) Ping(context.Context) error { return f.err }

type fakeStatus struct{ value domain.PollStatus }

func (f fakeStatus) Status() domain.PollStatus { return f.value }

type fakePolicies struct{ value config.Policy }

func (f fakePolicies) Policy() config.Policy                          { return f.value }
func (f fakePolicies) SetPolicy(context.Context, config.Policy) error { return nil }

type fakeResetter struct{}

func (fakeResetter) ResetUser(string) {}

type fakeAdminStore struct{}

func (fakeAdminStore) ResetPlayerPolicyState(context.Context, string) error { return nil }

type fakeSnapshots struct{ values []domain.PlayerSnapshot }

func (f fakeSnapshots) AllSnapshots(context.Context) ([]domain.PlayerSnapshot, error) {
	return append([]domain.PlayerSnapshot(nil), f.values...), nil
}

func (f fakeSnapshots) OnlineSnapshots(context.Context) ([]domain.PlayerSnapshot, error) {
	var result []domain.PlayerSnapshot
	for _, snapshot := range f.values {
		if snapshot.Online {
			result = append(result, snapshot)
		}
	}
	return result, nil
}

func (f fakeSnapshots) Snapshot(_ context.Context, userID string) (domain.PlayerSnapshot, error) {
	for _, snapshot := range f.values {
		if snapshot.Player.UserID == userID {
			return snapshot, nil
		}
	}
	return domain.PlayerSnapshot{}, store.ErrNotFound
}

func testServer() *Server {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	cfg := config.Config{
		Version: 1,
		Policy: config.Policy{
			Timezone:  "Asia/Shanghai",
			Default:   config.Rule{Enabled: false, Period: "daily", ResetAt: "04:00", Limit: config.Duration{Duration: 2 * time.Hour}},
			Overrides: map[string]config.RuleOverride{},
		},
	}
	return New(fakeHealth{}, fakeStatus{domain.PollStatus{StartedAt: now.Add(-time.Hour), LastSuccess: now, OnlineCount: 1, ConfigVersion: 1}}, fakeSnapshots{[]domain.PlayerSnapshot{{
		Player: domain.Player{UserID: "steam_1", PlayerID: "ABC", Name: "Kevin"},
		Policy: domain.ResolvedPolicy{Enabled: true, PeriodType: "daily", Timezone: "Asia/Shanghai", ResetAt: "04:00", Limit: 2 * time.Hour},
		Period: domain.Period{Key: "period", Start: now, End: now.Add(24 * time.Hour)},
		Used:   30 * time.Minute, Remaining: 90 * time.Minute, Online: true,
	}}}, fakePolicies{cfg.Policy}, fakeResetter{}, fakeAdminStore{}, "", "", func() config.Config { return cfg })
}

func TestHealthAndReadiness(t *testing.T) {
	server := testServer()
	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK || res.Header().Get("X-Request-ID") == "" {
			t.Fatalf("%s code=%d headers=%v body=%s", path, res.Code, res.Header(), res.Body.String())
		}
	}
}

func TestHealthFailsWhenSQLiteIsUnavailable(t *testing.T) {
	server := New(fakeHealth{errors.New("disk failure")}, fakeStatus{}, fakeSnapshots{}, fakePolicies{}, fakeResetter{}, fakeAdminStore{}, "", "", func() config.Config { return config.Config{} })
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
}

func TestHealthIsDegradedAfterInvalidConfigReload(t *testing.T) {
	server := New(fakeHealth{}, fakeStatus{domain.PollStatus{ConfigReloadErr: "invalid timezone"}}, fakeSnapshots{}, fakePolicies{}, fakeResetter{}, fakeAdminStore{}, "", "", func() config.Config { return config.Config{} })
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"status":"degraded"`) {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
}

func TestReadOnlyRoutesAndUnknownPlayer(t *testing.T) {
	server := testServer()
	paths := map[string]int{
		"/api/v1/status":          http.StatusOK,
		"/api/v1/players":         http.StatusOK,
		"/api/v1/players/steam_1": http.StatusOK,
		"/api/v1/players/missing": http.StatusNotFound,
		"/api/v1/policies":        http.StatusOK,
		"/api/v1/unknown":         http.StatusNotFound,
	}
	for path, want := range paths {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != want {
			t.Errorf("%s code=%d want=%d body=%s", path, res.Code, want, res.Body.String())
		}
		if contentType := res.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
			t.Errorf("%s content-type=%q", path, contentType)
		}
	}
}

func TestPoliciesReportDatabaseSource(t *testing.T) {
	server := testServer()
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/policies", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"source":"database"`) {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
}

func TestPoliciesPreserveInactiveStrategyValues(t *testing.T) {
	server := testServer()
	policies := server.policies.(fakePolicies)
	policies.value.Default.Strategy = "cooldown"
	policies.value.Default.Limit = config.Duration{Duration: time.Hour}
	policies.value.Default.CooldownEvery = config.Duration{Duration: 2 * time.Hour}
	policies.value.Default.CooldownRest = config.Duration{Duration: 30 * time.Minute}
	server.policies = policies
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/policies", nil))
	if !strings.Contains(res.Body.String(), `"limit_ms":3600000`) {
		t.Fatalf("inactive fixed-window limit was lost: %s", res.Body.String())
	}
}

func TestResponsesDoNotExposeSensitiveFields(t *testing.T) {
	server := testServer()
	for _, path := range []string{"/api/v1/status", "/api/v1/players", "/api/v1/policies"} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		body := strings.ToLower(res.Body.String())
		for _, forbidden := range []string{"admin_password", "authorization", "password", `"ip"`} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s leaked %q: %s", path, forbidden, body)
			}
		}
		var value any
		if err := json.Unmarshal(res.Body.Bytes(), &value); err != nil {
			t.Errorf("%s invalid JSON: %v", path, err)
		}
	}
}

func TestPlayersListsKnownOfflineSnapshots(t *testing.T) {
	server := testServer()
	online, _ := server.snapshots.OnlineSnapshots(t.Context())
	server.snapshots = fakeSnapshots{append(online, domain.PlayerSnapshot{Player: domain.Player{UserID: "offline"}, Online: false})}
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/players", nil))
	if !strings.Contains(res.Body.String(), "offline") || !strings.Contains(res.Body.String(), `"online":false`) {
		t.Fatalf("offline player missing: %s", res.Body.String())
	}
}

func TestPlayerIncludesPersistedWarningState(t *testing.T) {
	server := testServer()
	snapshot, err := server.snapshots.Snapshot(t.Context(), "steam_1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Warnings = []domain.WarningState{{Threshold: 5 * time.Minute, Status: "success", Attempts: 1}}
	server.snapshots = fakeSnapshots{[]domain.PlayerSnapshot{snapshot}}
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/players/steam_1", nil))
	body := res.Body.String()
	if !strings.Contains(body, `"threshold_ms":300000`) || !strings.Contains(body, `"status":"success"`) {
		t.Fatalf("body=%s", body)
	}
}

func TestAdminLoginUnlocksReset(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	cfg := config.Config{Version: 1}
	server := New(fakeHealth{}, fakeStatus{domain.PollStatus{LastSuccess: now}}, fakeSnapshots{}, fakePolicies{}, fakeResetter{}, fakeAdminStore{}, "admin", "secret", func() config.Config { return cfg })

	unauthorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/v1/players/steam_1/reset", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized code=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	login := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	server.Handler().ServeHTTP(login, req)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) == 0 {
		t.Fatalf("login code=%d cookies=%v body=%s", login.Code, login.Result().Cookies(), login.Body.String())
	}

	reset := httptest.NewRecorder()
	resetReq := httptest.NewRequest(http.MethodPost, "/api/v1/players/steam_1/reset", nil)
	resetReq.AddCookie(login.Result().Cookies()[0])
	server.Handler().ServeHTTP(reset, resetReq)
	if reset.Code != http.StatusOK || !strings.Contains(reset.Body.String(), `"status":"reset"`) {
		t.Fatalf("reset code=%d body=%s", reset.Code, reset.Body.String())
	}
}
