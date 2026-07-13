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
		"/players":  `{"players":[{"name":"Kevin","accountName":"kevin","playerId":"ABC","userId":"steam_1","ip":"192.0.2.1","ping":28.5,"location_x":123.25,"location_y":-99.5,"level":41`,
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
		"Difficulty": "None", "ExpRate": json.Number("1.0"), "ServerPlayerMaxNum": json.Number("32"),
		"RESTAPIEnabled": true, "Nested": map[string]any{"Limit": json.Number("12")},
		"Modes": []any{json.Number("1"), json.Number("2.5")},
	}}
	if !reflect.DeepEqual(settings, wantSettings) {
		t.Fatalf("settings=%#v, want %#v", settings, wantSettings)
	}
}

func TestSettingsPreservesIntegersBeyondIEEE754SafeRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"large_a":9007199254740992,"large_b":9007199254740993,"large_exp":9.007199254740993e15,"safe":9007199254740991,"official":1.0}`))
	}))
	defer server.Close()
	settings, err := New(server.URL, "secret", time.Second).Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"large_a": "9007199254740992", "large_b": "9007199254740993", "large_exp": "9.007199254740993e15",
	} {
		number, ok := settings.Values[key].(json.Number)
		if !ok || number.String() != want {
			t.Fatalf("%s=%#v want json.Number(%q)", key, settings.Values[key], want)
		}
	}
	for key, want := range map[string]string{"safe": "9007199254740991", "official": "1.0"} {
		number, ok := settings.Values[key].(json.Number)
		if !ok || number.String() != want {
			t.Fatalf("%s=%#v want json.Number(%q)", key, settings.Values[key], want)
		}
	}
}

func TestReadOnlyEndpointsRejectNonObjectTopLevelJSON(t *testing.T) {
	tests := []struct {
		name string
		path string
		call func(*Client) error
	}{
		{name: "players", path: "/players", call: func(client *Client) error {
			_, err := client.ListPlayers(context.Background())
			return err
		}},
		{name: "metrics", path: "/metrics", call: func(client *Client) error {
			_, err := client.Metrics(context.Background())
			return err
		}},
		{name: "info", path: "/info", call: func(client *Client) error {
			_, err := client.Info(context.Background())
			return err
		}},
		{name: "settings", path: "/settings", call: func(client *Client) error {
			_, err := client.Settings(context.Background())
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, payload := range []string{"null", "[]"} {
				t.Run(payload, func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path != test.path {
							t.Errorf("path=%s, want %s", r.URL.Path, test.path)
						}
						_, _ = w.Write([]byte(payload))
					}))
					defer server.Close()
					if err := test.call(New(server.URL, "secret", time.Second)); err == nil {
						t.Fatal("expected top-level schema error")
					}
				})
			}
		})
	}
}

func TestReadOnlyEndpointsRequireOfficialFields(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		valid    map[string]any
		required []string
		call     func(*Client) error
	}{
		{
			name: "players", path: "/players",
			valid: map[string]any{"players": []any{map[string]any{
				"name": "Kevin", "accountName": "kevin", "playerId": "ABC", "userId": "steam_1",
				"ip": "192.0.2.1", "ping": 28.5, "location_x": 123.25, "location_y": -99.5,
				"level": 41,
			}}},
			required: []string{"players"},
			call: func(client *Client) error {
				_, err := client.ListPlayers(t.Context())
				return err
			},
		},
		{
			name: "metrics", path: "/metrics",
			valid: map[string]any{
				"serverfps": 58, "currentplayernum": 1, "serverframetime": 17.2, "maxplayernum": 32,
				"uptime": 3600, "basecampnum": 2, "days": 126,
			},
			required: []string{"serverfps", "currentplayernum", "serverframetime", "maxplayernum", "uptime", "basecampnum", "days"},
			call: func(client *Client) error {
				_, err := client.Metrics(t.Context())
				return err
			},
		},
		{
			name: "info", path: "/info",
			valid: map[string]any{
				"version": "v0.7.2", "servername": "Home", "description": "Family", "worldguid": "WORLD",
			},
			required: []string{"version", "servername", "description", "worldguid"},
			call: func(client *Client) error {
				_, err := client.Info(t.Context())
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			type invalidPayload struct {
				value     any
				wantError string
			}
			payloads := map[string]invalidPayload{
				"empty object": {value: map[string]any{}, wantError: "decode Palworld " + test.name + ": missing required field " + test.required[0]},
			}
			for _, field := range test.required {
				copy := cloneJSONMap(t, test.valid)
				delete(copy, field)
				payloads["missing "+field] = invalidPayload{
					value: copy, wantError: "decode Palworld " + test.name + ": missing required field " + field,
				}
			}
			if test.name == "players" {
				payloads["null players"] = invalidPayload{
					value: map[string]any{"players": nil}, wantError: "decode Palworld players: missing required field players",
				}
				playerFields := []string{"name", "accountName", "playerId", "userId", "ip", "ping", "location_x", "location_y", "level"}
				for _, field := range playerFields {
					copy := cloneJSONMap(t, test.valid)
					player := copy["players"].([]any)[0].(map[string]any)
					delete(player, field)
					payloads["player missing "+field] = invalidPayload{
						value: copy, wantError: "decode Palworld players: player 0 missing required field " + field,
					}
				}
				copy := cloneJSONMap(t, test.valid)
				copy["players"].([]any)[0].(map[string]any)["userId"] = ""
				payloads["player empty userId"] = invalidPayload{
					value: copy, wantError: "decode Palworld players: player 0 has empty userId",
				}
			}

			for name, invalid := range payloads {
				t.Run(name, func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path != test.path {
							t.Errorf("path=%s, want %s", r.URL.Path, test.path)
						}
						_ = json.NewEncoder(w).Encode(invalid.value)
					}))
					defer server.Close()
					err := test.call(New(server.URL, "secret", time.Second))
					if err == nil || !strings.Contains(err.Error(), invalid.wantError) {
						t.Fatalf("err=%v, want error containing %q", err, invalid.wantError)
					}
				})
			}
		})
	}
}

func cloneJSONMap(t *testing.T, input map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestReadOnlyEndpointRejectsResponseOverOneMiB(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"description":"` + strings.Repeat("x", maxResponseBytes) + `"}`))
	}))
	defer server.Close()
	client := New(server.URL, "secret", time.Second)
	_, err := client.Info(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err=%v, want response size error", err)
	}
}

func TestReadOnlyEndpointRejectsTrailingJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"serverfps":58}{"serverfps":59}`))
	}))
	defer server.Close()
	client := New(server.URL, "secret", time.Second)
	if _, err := client.Metrics(context.Background()); err == nil {
		t.Fatal("expected trailing JSON error")
	}
}

func TestPlayerSensitiveObservationFieldsDoNotMarshal(t *testing.T) {
	player := domain.Player{
		UserID: "steam_1", PlayerID: "ABC", Name: "Kevin", AccountName: "kevin",
		IP: "192.0.2.1", Ping: 28.5, LocationX: 123.25, LocationY: -99.5, Level: 41, BuildingCount: 119,
	}
	encoded, err := json.Marshal(player)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(encoded, &values); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"user_id": true, "player_id": true, "name": true, "account_name": true, "last_online": true,
	}
	for key := range values {
		if !allowed[key] {
			t.Errorf("unexpected field %q serialized in %s", key, encoded)
		}
	}
	for _, key := range []string{"ip", "ping", "location_x", "location_y", "level"} {
		if _, ok := values[key]; ok {
			t.Errorf("sensitive field %q serialized in %s", key, encoded)
		}
	}
	for key, want := range map[string]string{
		"user_id": "steam_1", "player_id": "ABC", "name": "Kevin", "account_name": "kevin",
	} {
		if values[key] != want {
			t.Errorf("%s=%v, want %q", key, values[key], want)
		}
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
			"ping": 0, "location_x": 0, "location_y": 0, "level": 0,
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
