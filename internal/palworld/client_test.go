package palworld

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
