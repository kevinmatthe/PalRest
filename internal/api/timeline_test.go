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
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type observationQueriesFake struct {
	timeline       store.SensitivePlayerTimeline
	metrics        []store.ServerMetricSample
	documents      []store.ServerDocumentOccurrence
	documentPage   store.ServerDocumentPage
	documentCursor *store.ServerDocumentCursor
	err            error
	actor          string
	calls          int
}

func (f *observationQueriesFake) ResetPlayerPolicyState(context.Context, string) error { return nil }
func (f *observationQueriesFake) ReadSensitivePlayerTimeline(_ context.Context, actor, _ string, _, _ time.Time, _ int) (store.SensitivePlayerTimeline, error) {
	f.calls++
	f.actor = actor
	return f.timeline, f.err
}
func (f *observationQueriesFake) ReadServerMetrics(_ context.Context, actor string, _ time.Time, _ time.Time, _ int) ([]store.ServerMetricSample, error) {
	f.calls++
	f.actor = actor
	return f.metrics, f.err
}
func (f *observationQueriesFake) ReadServerDocuments(_ context.Context, actor, _ string, _ int, cursor *store.ServerDocumentCursor) (store.ServerDocumentPage, error) {
	f.calls++
	f.actor = actor
	f.documentCursor = cursor
	if f.documentPage.Documents != nil || f.documentPage.Next != nil {
		return f.documentPage, f.err
	}
	return store.ServerDocumentPage{Documents: f.documents}, f.err
}

func TestAdminDocumentsCursorRoundTripsAndRejectsMalformedValues(t *testing.T) {
	at := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	hash := strings.Repeat("a", 64)
	next := &store.ServerDocumentCursor{ObservedAt: at, ContentHash: hash}
	repo := &observationQueriesFake{documentPage: store.ServerDocumentPage{Documents: []store.ServerDocumentOccurrence{{Kind: "settings", ObservedAt: at, ContentHash: hash, Canonical: []byte(`{"value":"A"}`)}}, Next: next}}
	server := timelineServer(repo)
	first := httptest.NewRecorder()
	server.Handler().ServeHTTP(first, adminRequest(t, server, "/api/v1/admin/server/documents?kind=settings&limit=1"))
	if first.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", first.Code, first.Body.String())
	}
	var response struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil || response.NextCursor == "" {
		t.Fatalf("response=%+v err=%v body=%s", response, err, first.Body.String())
	}
	repo.documentPage = store.ServerDocumentPage{Documents: []store.ServerDocumentOccurrence{}}
	second := httptest.NewRecorder()
	server.Handler().ServeHTTP(second, adminRequest(t, server, "/api/v1/admin/server/documents?kind=settings&limit=1&cursor="+response.NextCursor))
	if second.Code != http.StatusOK || repo.documentCursor == nil || !repo.documentCursor.ObservedAt.Equal(at) || repo.documentCursor.ContentHash != hash {
		t.Fatalf("code=%d cursor=%+v body=%s", second.Code, repo.documentCursor, second.Body.String())
	}
	calls := repo.calls
	for _, cursor := range []string{"", "not-base64", response.NextCursor + "x"} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, adminRequest(t, server, "/api/v1/admin/server/documents?kind=info&cursor="+cursor))
		if res.Code != http.StatusBadRequest {
			t.Errorf("cursor=%q code=%d body=%s", cursor, res.Code, res.Body.String())
		}
	}
	if repo.calls != calls {
		t.Fatalf("malformed cursors reached repository: calls=%d want=%d", repo.calls, calls)
	}
}

func TestAdminMetricsAndDocumentsValidateAndReturnTypedJSON(t *testing.T) {
	at := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	repo := &observationQueriesFake{metrics: []store.ServerMetricSample{{ObservedAt: at}}, documents: []store.ServerDocumentOccurrence{{Kind: "settings", ObservedAt: at, ContentHash: "hash", Canonical: []byte(`{"Difficulty":"Hard"}`)}}}
	server := timelineServer(repo)
	metrics := httptest.NewRecorder()
	server.Handler().ServeHTTP(metrics, adminRequest(t, server, "/api/v1/admin/server/metrics?start=2026-07-13T08:00:00Z&end=2026-07-13T09:00:00Z"))
	if metrics.Code != http.StatusOK || !strings.Contains(metrics.Body.String(), `"observed_at"`) {
		t.Fatalf("metrics code=%d body=%s", metrics.Code, metrics.Body.String())
	}
	docs := httptest.NewRecorder()
	server.Handler().ServeHTTP(docs, adminRequest(t, server, "/api/v1/admin/server/documents?kind=settings"))
	if docs.Code != http.StatusOK || !strings.Contains(docs.Body.String(), `"canonical":{"Difficulty":"Hard"}`) {
		t.Fatalf("docs code=%d body=%s", docs.Code, docs.Body.String())
	}
	for _, path := range []string{"/api/v1/admin/server/metrics?start=x&end=x", "/api/v1/admin/server/documents?kind=secret", "/api/v1/admin/server/documents?kind=info&limit=0", "/api/v1/admin/server/documents?kind=info&kind=settings"} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, adminRequest(t, server, path))
		if res.Code != http.StatusBadRequest {
			t.Errorf("path=%s code=%d", path, res.Code)
		}
	}
}

func TestAdminDocumentsRedactSecretsFromLegacyCanonicalRows(t *testing.T) {
	at := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	legacy := []byte(`{"Difficulty":"Hard","AdminPassword":"old-secret","nested":{"apiKey":"key-secret","items":[{"token":"token-secret","kept":"yes"}]}}`)
	repo := &observationQueriesFake{documents: []store.ServerDocumentOccurrence{{Kind: "settings", ObservedAt: at, ContentHash: "legacy", Canonical: legacy}}}
	server := timelineServer(repo)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, adminRequest(t, server, "/api/v1/admin/server/documents?kind=settings"))
	body := res.Body.String()
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, body)
	}
	for _, secret := range []string{"old-secret", "key-secret", "token-secret"} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaked %q: %s", secret, body)
		}
	}
	for _, retained := range []string{`"Difficulty":"Hard"`, `"kept":"yes"`, `"AdminPassword":"[REDACTED]"`} {
		if !strings.Contains(body, retained) {
			t.Fatalf("response missing %s: %s", retained, body)
		}
	}
}

func TestAdminMetricsAndDocumentsMapNotFound(t *testing.T) {
	repo := &observationQueriesFake{err: store.ErrNotFound}
	server := timelineServer(repo)
	for _, path := range []string{"/api/v1/admin/server/metrics?start=2026-07-13T08:00:00Z&end=2026-07-13T09:00:00Z", "/api/v1/admin/server/documents?kind=info"} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, adminRequest(t, server, path))
		if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), `"code":"not_found"`) {
			t.Errorf("path=%s code=%d body=%s", path, res.Code, res.Body.String())
		}
	}
}

func timelineServer(repo *observationQueriesFake) *Server {
	cfg := config.Config{Version: 1}
	return New(fakeHealth{}, fakeStatus{}, fakeSnapshots{}, fakeAnalyticsQueries{}, fakeAnalyticsOnline{}, fakePolicies{}, fakeResetter{}, repo, "root", "secret", func() config.Config { return cfg })
}

func adminRequest(t *testing.T, server *Server, path string) *http.Request {
	t.Helper()
	token, ok := server.auth.login("root", "secret")
	if !ok {
		t.Fatal("login failed")
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(sessionCookie(token, 3600))
	return req
}

func TestAdminTimelineRequiresAuthAndReturnsOnlySafeDecodedEvents(t *testing.T) {
	at := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	repo := &observationQueriesFake{timeline: store.SensitivePlayerTimeline{
		Events:         []store.ActivityEvent{{ID: "e", EventType: "player_joined", OccurredAt: at, ObservedAt: at, Source: "palworld_rest", Confidence: "observed", SchemaVersion: 1, PayloadJSON: `{"name":"Kevin","account_name":"acct","secret":"no"}`}, {ID: "future", EventType: "future_event", OccurredAt: at, ObservedAt: at, Source: "palworld_rest", Confidence: "observed", SchemaVersion: 99, PayloadJSON: `{"credential":"must-not-leak"}`}},
		Trajectories:   []store.TrajectorySample{{UserID: "u", ObservedAt: at, X: 1, Y: 2}},
		PrivateSamples: []store.PlayerPrivateSample{{UserID: "u", ObservedAt: at, IP: "192.0.2.1:8211", Ping: 20}},
	}}
	server := timelineServer(repo)
	path := "/api/v1/admin/players/u/timeline?start=2026-07-13T08:00:00Z&end=2026-07-13T09:00:00Z&limit=500"
	unauth := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, path, nil))
	if unauth.Code != http.StatusUnauthorized || repo.calls != 0 {
		t.Fatalf("code=%d calls=%d", unauth.Code, repo.calls)
	}
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, adminRequest(t, server, path))
	body := res.Body.String()
	if res.Code != http.StatusOK || repo.actor != "root" || !strings.Contains(body, `"ip":"192.0.2.1:8211"`) || !strings.Contains(body, `"x":1`) {
		t.Fatalf("code=%d actor=%q body=%s", res.Code, repo.actor, body)
	}
	if strings.Contains(body, "payload_json") || strings.Contains(body, "secret") {
		t.Fatalf("raw payload leaked: %s", body)
	}
	if !strings.Contains(body, `"summary":"unsupported event payload"`) || strings.Contains(body, "must-not-leak") {
		t.Fatalf("future event was not safely summarized: %s", body)
	}
}

func TestAdminServerObservationRoutesRequireAuthentication(t *testing.T) {
	repo := &observationQueriesFake{}
	server := timelineServer(repo)
	for _, path := range []string{"/api/v1/admin/server/metrics?start=2026-07-13T08:00:00Z&end=2026-07-13T09:00:00Z", "/api/v1/admin/server/documents?kind=info"} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusUnauthorized {
			t.Errorf("path=%s code=%d body=%s", path, res.Code, res.Body.String())
		}
	}
	if repo.calls != 0 {
		t.Fatalf("unauthenticated requests reached repository: %d", repo.calls)
	}
}

func TestAdminTimelineValidatesRangeBeforeRepository(t *testing.T) {
	repo := &observationQueriesFake{}
	server := timelineServer(repo)
	for _, query := range []string{"", "?start=x&end=2026-07-13T09:00:00Z", "?start=2026-07-13T08:00:00Z&start=2026-07-13T08:00:00Z&end=2026-07-13T09:00:00Z", "?start=2026-01-01T00:00:00Z&end=2026-03-01T00:00:00Z", "?start=2026-07-13T08:00:00Z&end=2026-07-13T09:00:00Z&limit=2001", "?start=2026-07-13T08:00:00Z&end=2026-07-13T09:00:00Z&extra=1"} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, adminRequest(t, server, "/api/v1/admin/players/u/timeline"+query))
		if res.Code != http.StatusBadRequest {
			t.Errorf("query=%q code=%d body=%s", query, res.Code, res.Body.String())
		}
	}
	if repo.calls != 0 {
		t.Fatalf("repository calls=%d", repo.calls)
	}
}

func TestAdminTimelineMapsNotFoundAndInternalErrors(t *testing.T) {
	repo := &observationQueriesFake{err: store.ErrNotFound}
	server := timelineServer(repo)
	path := "/api/v1/admin/players/missing/timeline?start=2026-07-13T08:00:00Z&end=2026-07-13T09:00:00Z"
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, adminRequest(t, server, path))
	if res.Code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	repo.err = errors.New("database password=secret")
	res = httptest.NewRecorder()
	server.Handler().ServeHTTP(res, adminRequest(t, server, path))
	if res.Code != http.StatusInternalServerError || strings.Contains(res.Body.String(), "password") {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
}
