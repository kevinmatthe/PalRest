package palworld

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

func TestReadOnlyEndpointsDecodeOfficialSchemas(t *testing.T) {
	fixtures := map[string]string{
		"/players":  `{"players":[{"name":"Kevin","accountName":"kevin","playerId":"ABC","userId":"steam_1","ip":"192.0.2.1","ping":28.5,"location_x":123.25,"location_y":-99.5,"level":41,"building_count":119}]}`,
		"/metrics":  `{"serverfps":58,"currentplayernum":1,"serverframetime":17.2,"maxplayernum":32,"uptime":3600,"basecampnum":2,"days":126}`,
		"/info":     `{"version":"v0.7.2","servername":"Home","description":"Family","worldguid":"WORLD"}`,
		"/settings": `{"Difficulty":"None","ExpRate":1.0,"ServerPlayerMaxNum":32,"RESTAPIEnabled":true,"Nested":{"Limit":12},"Modes":[1,2.5]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method=%s, want GET", r.Method)
		}
		fixture, ok := fixtures[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixture))
	}))
	defer server.Close()

	client := New(server.URL, "secret", time.Second)
	ctx := context.Background()
	players, err := client.ListPlayers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantPlayers := []domain.Player{{
		UserID: "steam_1", PlayerID: "ABC", Name: "Kevin", AccountName: "kevin",
		IP: "192.0.2.1", Ping: 28.5, LocationX: 123.25, LocationY: -99.5, Level: 41, BuildingCount: 119,
	}}
	if !reflect.DeepEqual(players, wantPlayers) {
		t.Fatalf("players=%#v, want %#v", players, wantPlayers)
	}

	metrics, err := client.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantMetrics := domain.ServerMetrics{
		ServerFPS: 58, CurrentPlayerNum: 1, ServerFrameTime: 17.2, MaxPlayerNum: 32,
		UptimeSeconds: 3600, BaseCampNum: 2, Days: 126,
	}
	if metrics != wantMetrics {
		t.Fatalf("metrics=%#v, want %#v", metrics, wantMetrics)
	}

	info, err := client.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantInfo := domain.ServerInfo{
		Version: "v0.7.2", ServerName: "Home", Description: "Family", WorldGUID: "WORLD",
	}
	if info != wantInfo {
		t.Fatalf("info=%#v, want %#v", info, wantInfo)
	}

	settings, err := client.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantSettings := domain.ServerSettings{Values: map[string]any{
		"Difficulty": "None", "ExpRate": 1.0, "ServerPlayerMaxNum": float64(32),
		"RESTAPIEnabled": true, "Nested": map[string]any{"Limit": float64(12)},
		"Modes": []any{float64(1), 2.5},
	}}
	if !reflect.DeepEqual(settings, wantSettings) {
		t.Fatalf("settings=%#v, want %#v", settings, wantSettings)
	}
}

func TestListPlayersUsesBasicAuthAndDecodesPlayers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "secret" {
			t.Errorf("auth=%q %q %v", username, password, ok)
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v1/api/players" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"players": []map[string]any{{
			"name": "Kevin", "accountName": "kevin", "playerId": "ABC", "userId": "steam_1", "ip": "127.0.0.1",
		}}})
	}))
	defer server.Close()
	client := New(server.URL+"/v1/api", "secret", time.Second)
	players, err := client.ListPlayers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(players) != 1 || players[0].UserID != "steam_1" || players[0].Name != "Kevin" {
		t.Fatalf("players=%+v", players)
	}
}

func TestAnnounceAndKickRequestBodies(t *testing.T) {
	requests := make(chan map[string]string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		body["path"] = r.URL.Path
		requests <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := New(server.URL, "secret", time.Second)
	if err := client.Announce(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if err := client.Kick(context.Background(), "steam_1", "limit reached"); err != nil {
		t.Fatal(err)
	}
	announce := <-requests
	kick := <-requests
	if announce["path"] != "/announce" || announce["message"] != "hello" {
		t.Fatalf("announce=%v", announce)
	}
	if kick["path"] != "/kick" || kick["userid"] != "steam_1" || kick["message"] != "limit reached" {
		t.Fatalf("kick=%v", kick)
	}
}

func TestClientErrorsAreBoundedAndDoNotLeakPassword(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("authorization failed for very-secret-password: " + strings.Repeat("x", 10000)))
	}))
	defer server.Close()
	client := New(server.URL, "very-secret-password", time.Second)
	_, err := client.ListPlayers(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "very-secret-password") || len(err.Error()) > 1200 {
		t.Fatalf("unsafe error length=%d: %v", len(err.Error()), err)
	}
}

func TestClientHonorsTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := New(server.URL, "secret", 10*time.Millisecond)
	if _, err := client.ListPlayers(context.Background()); err == nil {
		t.Fatal("expected timeout")
	}
}

func TestListPlayersRejectsMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"players":`))
	}))
	defer server.Close()
	client := New(server.URL, "secret", time.Second)
	if _, err := client.ListPlayers(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}
