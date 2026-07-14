package api

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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

type fakeSaveImporter struct {
	result store.SaveImportResult
	err    error
	path   string
	calls  int
}

func (f *fakeSaveImporter) Import(_ context.Context, path string) (store.SaveImportResult, error) {
	f.calls++
	f.path = path
	return f.result, f.err
}

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
	}}}, fakeAnalyticsQueries{}, fakeAnalyticsOnline{}, fakePolicies{cfg.Policy}, fakeResetter{}, fakeAdminStore{}, "", "", func() config.Config { return cfg })
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
	server := New(fakeHealth{errors.New("disk failure")}, fakeStatus{}, fakeSnapshots{}, fakeAnalyticsQueries{}, fakeAnalyticsOnline{}, fakePolicies{}, fakeResetter{}, fakeAdminStore{}, "", "", func() config.Config { return config.Config{} })
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
}

func TestHealthIsDegradedAfterInvalidConfigReload(t *testing.T) {
	server := New(fakeHealth{}, fakeStatus{domain.PollStatus{ConfigReloadErr: "invalid timezone"}}, fakeSnapshots{}, fakeAnalyticsQueries{}, fakeAnalyticsOnline{}, fakePolicies{}, fakeResetter{}, fakeAdminStore{}, "", "", func() config.Config { return config.Config{} })
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"status":"degraded"`) {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
}

func TestLivePositionsReturnsOnlineCoords(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	server := New(
		fakeHealth{},
		fakeStatus{domain.PollStatus{StartedAt: now.Add(-time.Hour), LastSuccess: now, OnlineCount: 2, ConfigVersion: 1}},
		fakeSnapshots{[]domain.PlayerSnapshot{
			{
				Player: domain.Player{UserID: "steam_online", Name: "Avery", LocationX: 1000, LocationY: -2000, Ping: 22, Level: 12},
				Online: true,
			},
			{
				Player: domain.Player{UserID: "steam_offline", Name: "Bo", LocationX: 9, LocationY: 9},
				Online: false,
			},
			{
				Player: domain.Player{UserID: "steam_nocoords", Name: "Cy", LocationX: math.NaN(), LocationY: 1},
				Online: true,
			},
		}},
		fakeAnalyticsQueries{},
		fakeAnalyticsOnline{},
		fakePolicies{},
		fakeResetter{},
		fakeAdminStore{},
		"",
		"",
		func() config.Config { return config.Config{} },
	)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/live/positions", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	var body struct {
		AsOf        string `json:"as_of"`
		OnlineCount int    `json:"online_count"`
		Positioned  int    `json:"positioned"`
		Players     []struct {
			UserID string  `json:"user_id"`
			Name   string  `json:"name"`
			X      float64 `json:"x"`
			Y      float64 `json:"y"`
		} `json:"players"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.OnlineCount != 2 || body.Positioned != 1 || len(body.Players) != 1 {
		t.Fatalf("body=%+v", body)
	}
	if body.Players[0].UserID != "steam_online" || body.Players[0].X != 1000 || body.Players[0].Y != -2000 {
		t.Fatalf("player=%+v", body.Players[0])
	}
	if body.AsOf == "" {
		t.Fatal("expected as_of from last success")
	}
}

func TestReadOnlyRoutesAndUnknownPlayer(t *testing.T) {
	server := testServer()
	paths := map[string]int{
		"/api/v1/status":             http.StatusOK,
		"/api/v1/live/positions":     http.StatusOK,
		"/api/v1/players":            http.StatusOK,
		"/api/v1/players/steam_1":    http.StatusOK,
		"/api/v1/players/missing":    http.StatusNotFound,
		"/api/v1/policies":           http.StatusOK,
		"/api/v1/analytics/summary":  http.StatusOK,
		"/api/v1/analytics/activity": http.StatusOK,
		"/api/v1/unknown":            http.StatusNotFound,
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

func TestCreditPlayerResponseIncludesAvailableAndLastRecovery(t *testing.T) {
	snapshot := domain.PlayerSnapshot{
		Player:              domain.Player{UserID: "steam_credit", Name: "Credit"},
		Policy:              domain.ResolvedPolicy{Enabled: true, Strategy: "credit", CreditMax: time.Hour},
		Remaining:           45 * time.Minute,
		LastCreditRecovered: 15 * time.Minute,
	}
	payload, err := json.Marshal(toPlayerDTO(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	body := string(payload)
	if !strings.Contains(body, `"credit_available_ms":2700000`) || !strings.Contains(body, `"last_credit_recovered_ms":900000`) {
		t.Fatalf("credit response is missing recovery state: %s", body)
	}

	snapshot.Policy.Strategy = "fixed_window"
	payload, err = json.Marshal(toPlayerDTO(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "credit_available_ms") || strings.Contains(string(payload), "last_credit_recovered_ms") {
		t.Fatalf("fixed-window response contains credit-only fields: %s", payload)
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
	server := New(fakeHealth{}, fakeStatus{domain.PollStatus{LastSuccess: now}}, fakeSnapshots{}, fakeAnalyticsQueries{}, fakeAnalyticsOnline{}, fakePolicies{}, fakeResetter{}, fakeAdminStore{}, "admin", "secret", func() config.Config { return cfg })

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

func TestAdminSaveImportRequiresAuthAndEnabledImporter(t *testing.T) {
	cfg := config.Config{Version: 1}
	server := New(fakeHealth{}, fakeStatus{}, fakeSnapshots{}, fakeAnalyticsQueries{}, fakeAnalyticsOnline{}, fakePolicies{}, fakeResetter{}, fakeAdminStore{}, "admin", "secret", func() config.Config { return cfg })
	path := "/api/v1/admin/save/import"

	unauthorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, path, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized code=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	disabled := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.AddCookie(adminCookie(t, server))
	server.Handler().ServeHTTP(disabled, req)
	if disabled.Code != http.StatusNotFound {
		t.Fatalf("disabled code=%d body=%s", disabled.Code, disabled.Body.String())
	}

	importer := &fakeSaveImporter{result: store.SaveImportResult{ImportID: 7, Fingerprint: strings.Repeat("a", 64), Inserted: true}}
	cfg.Save.Enabled = true
	cfg.Save.Path = "/data/pal-saves/Level.sav"
	server = New(fakeHealth{}, fakeStatus{}, fakeSnapshots{}, fakeAnalyticsQueries{}, fakeAnalyticsOnline{}, fakePolicies{}, fakeResetter{}, fakeAdminStore{}, "admin", "secret", func() config.Config { return cfg }, importer)
	ok := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, path, nil)
	req.AddCookie(adminCookie(t, server))
	server.Handler().ServeHTTP(ok, req)
	if ok.Code != http.StatusOK || importer.calls != 1 || importer.path != cfg.Save.Path || !strings.Contains(ok.Body.String(), `"import_id":7`) {
		t.Fatalf("code=%d calls=%d path=%q body=%s", ok.Code, importer.calls, importer.path, ok.Body.String())
	}
}

func adminCookie(t *testing.T, server *Server) *http.Cookie {
	t.Helper()
	token, ok := server.auth.login("admin", "secret")
	if !ok {
		t.Fatal("login failed")
	}
	return sessionCookie(token, 3600)
}
