package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/config"
	"github.com/kevinmatt/palworld-playtime-guard/internal/overlay"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type overlayProviderFake struct {
	snapshot overlay.Snapshot
	err      error
	calls    int
	gameID   string
	userID   string
}

func (f *overlayProviderFake) Snapshot(_ context.Context, gameID, userID string) (overlay.Snapshot, error) {
	f.calls++
	f.gameID = gameID
	f.userID = userID
	return f.snapshot, f.err
}

func overlayTestSnapshot() overlay.Snapshot {
	return overlay.Snapshot{
		Schema:       overlay.SchemaV1,
		GameID:       "palworld",
		UserID:       "steam_1",
		ObservedAt:   time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC),
		FreshUntil:   time.Date(2026, 7, 16, 1, 7, 3, 0, time.UTC),
		SourceStatus: "online",
		Capabilities: []string{"identity", "latency", "timers"},
		Identity:     overlay.Identity{DisplayName: "Kevin", AccountName: "safe-account"},
		Latency:      &overlay.Latency{Milliseconds: 23.5},
		Timers:       []overlay.Timer{{ID: "today", Label: "Today", ValueMS: 1234, Semantic: "duration", Tone: "normal"}},
	}
}

func newOverlayTestServer(provider *overlayProviderFake) *Server {
	options := []any{}
	if provider != nil {
		options = append(options, WithOverlayProvider(provider))
	}
	return New(fakeHealth{}, fakeStatus{}, fakeSnapshots{}, fakeAnalyticsQueries{}, fakeAnalyticsOnline{}, fakePolicies{}, fakeResetter{}, fakeAdminStore{}, "", "", func() config.Config { return config.Config{} }, options...)
}

func overlayRequest(t *testing.T, server *Server, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	return res
}

func requireOverlayError(t *testing.T, res *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if res.Code != status {
		t.Fatalf("code=%d want=%d body=%s", res.Code, status, res.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, res.Body.String())
	}
	if body.Error.Code != code {
		t.Fatalf("error code=%q want=%q body=%s", body.Error.Code, code, res.Body.String())
	}
}

func TestOverlaySnapshotRejectsMissingParameters(t *testing.T) {
	provider := &overlayProviderFake{}
	res := overlayRequest(t, newOverlayTestServer(provider), "/api/v1/overlay/snapshot", nil)
	requireOverlayError(t, res, http.StatusBadRequest, "invalid_request")
	if provider.calls != 0 {
		t.Fatalf("provider calls=%d want=0", provider.calls)
	}
}

func TestOverlaySnapshotMapsProviderErrors(t *testing.T) {
	tests := []struct {
		name   string
		target string
		err    error
		status int
		code   string
	}{
		{name: "unsupported game", target: "/api/v1/overlay/snapshot?game_id=x&user_id=u", err: overlay.ErrGameNotSupported, status: http.StatusNotFound, code: "game_not_supported"},
		{name: "missing player", target: "/api/v1/overlay/snapshot?game_id=palworld&user_id=missing", err: store.ErrNotFound, status: http.StatusNotFound, code: "player_not_found"},
		{name: "invalid request", target: "/api/v1/overlay/snapshot?game_id=palworld&user_id=steam_1", err: overlay.ErrInvalidRequest, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "generic failure", target: "/api/v1/overlay/snapshot?game_id=palworld&user_id=steam_1", err: errors.New("database password=secret"), status: http.StatusServiceUnavailable, code: "snapshot_unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &overlayProviderFake{err: tt.err}
			res := overlayRequest(t, newOverlayTestServer(provider), tt.target, nil)
			requireOverlayError(t, res, tt.status, tt.code)
			if provider.calls != 1 {
				t.Fatalf("provider calls=%d want=1", provider.calls)
			}
			if strings.Contains(res.Body.String(), "password=secret") {
				t.Fatalf("response leaked provider error: %s", res.Body.String())
			}
		})
	}
}

func TestOverlaySnapshotReturnsETagAndSupportsConditionalGET(t *testing.T) {
	snapshot := overlayTestSnapshot()
	provider := &overlayProviderFake{snapshot: snapshot}
	server := newOverlayTestServer(provider)

	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	wantETag := `"` + hex.EncodeToString(digest[:]) + `"`

	res := overlayRequest(t, server, "/api/v1/overlay/snapshot?game_id=%20palworld%20&user_id=%20steam_1%20", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("ETag"); got != wantETag {
		t.Fatalf("ETag=%q want=%q", got, wantETag)
	}
	if got := res.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control=%q want=no-cache", got)
	}
	if got := res.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got, want := res.Body.String(), string(payload)+"\n"; got != want {
		t.Fatalf("body=%q want=%q", got, want)
	}
	if provider.calls != 1 || provider.gameID != "palworld" || provider.userID != "steam_1" {
		t.Fatalf("provider calls=%d gameID=%q userID=%q", provider.calls, provider.gameID, provider.userID)
	}

	notModified := overlayRequest(t, server, "/api/v1/overlay/snapshot?game_id=palworld&user_id=steam_1", map[string]string{"If-None-Match": wantETag})
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("code=%d body=%s", notModified.Code, notModified.Body.String())
	}
	if notModified.Body.Len() != 0 {
		t.Fatalf("304 body=%q want empty", notModified.Body.String())
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls=%d want=2 (one per request)", provider.calls)
	}

	nonExact := overlayRequest(t, server, "/api/v1/overlay/snapshot?game_id=palworld&user_id=steam_1", map[string]string{"If-None-Match": wantETag + ", " + wantETag})
	if nonExact.Code != http.StatusOK {
		t.Fatalf("non-exact If-None-Match code=%d want=200", nonExact.Code)
	}
	if provider.calls != 3 {
		t.Fatalf("provider calls=%d want=3 (one per request)", provider.calls)
	}
}

func TestOverlaySnapshotRejectsInvalidQueryBeforeProvider(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		{name: "duplicate game", target: "/api/v1/overlay/snapshot?game_id=palworld&game_id=x&user_id=u"},
		{name: "duplicate user", target: "/api/v1/overlay/snapshot?game_id=palworld&user_id=u&user_id=v"},
		{name: "long game", target: "/api/v1/overlay/snapshot?game_id=" + strings.Repeat("g", 65) + "&user_id=u"},
		{name: "long user", target: "/api/v1/overlay/snapshot?game_id=palworld&user_id=" + strings.Repeat("u", 257)},
		{name: "whitespace game", target: "/api/v1/overlay/snapshot?game_id=%20%09&user_id=u"},
		{name: "whitespace user", target: "/api/v1/overlay/snapshot?game_id=palworld&user_id=%20%09"},
		{name: "C0 control", target: "/api/v1/overlay/snapshot?game_id=palworld&user_id=u%00x"},
		{name: "C1 control", target: "/api/v1/overlay/snapshot?game_id=palworld&user_id=u%C2%85x"},
		{name: "unicode control", target: "/api/v1/overlay/snapshot?game_id=palworld&user_id=u%E2%80%AEx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &overlayProviderFake{}
			res := overlayRequest(t, newOverlayTestServer(provider), tt.target, nil)
			requireOverlayError(t, res, http.StatusBadRequest, "invalid_request")
			if provider.calls != 0 {
				t.Fatalf("provider calls=%d want=0", provider.calls)
			}
		})
	}
}

func TestOverlaySnapshotFailsSafelyWithoutProvider(t *testing.T) {
	res := overlayRequest(t, newOverlayTestServer(nil), "/api/v1/overlay/snapshot?game_id=palworld&user_id=steam_1", nil)
	requireOverlayError(t, res, http.StatusServiceUnavailable, "snapshot_unavailable")
}

func TestOverlaySnapshotDoesNotExposeForbiddenKeys(t *testing.T) {
	provider := &overlayProviderFake{snapshot: overlayTestSnapshot()}
	res := overlayRequest(t, newOverlayTestServer(provider), "/api/v1/overlay/snapshot?game_id=palworld&user_id=steam_1", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	var body any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]struct{}{
		"ip": {}, "privatesamples": {}, "password": {}, "authorization": {}, "settings": {},
	}
	var inspect func(any)
	inspect = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(key))
				if _, found := forbidden[normalized]; found {
					t.Errorf("forbidden JSON key %q", key)
				}
				inspect(child)
			}
		case []any:
			for _, child := range value {
				inspect(child)
			}
		}
	}
	inspect(body)
}
