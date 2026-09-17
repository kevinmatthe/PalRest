package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/config"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

const progressRange = "?start=2026-07-01T00:00:00Z&end=2026-08-01T00:00:00Z"

type progressQueriesFake struct {
	result     store.PlayerProgress
	err        error
	calls      int
	userID     string
	start, end time.Time
	limit      int
}

func (f *progressQueriesFake) ReadPlayerProgress(_ context.Context, userID string, start, end time.Time, limit int) (store.PlayerProgress, error) {
	f.calls++
	f.userID, f.start, f.end, f.limit = userID, start, end, limit
	return f.result, f.err
}

type progressAdminStore struct {
	fakeAdminStore
	*progressQueriesFake
}

func progressServer(admin AdminStore, options ...any) *Server {
	return New(fakeHealth{}, fakeStatus{}, fakeSnapshots{}, fakeAnalyticsQueries{}, fakeAnalyticsOnline{}, fakePolicies{}, fakeResetter{}, admin, "root", "secret", func() config.Config { return config.Config{} }, options...)
}

func TestPublicPlayerProgressWiringAndTypedJSON(t *testing.T) {
	for _, wiring := range []string{"admin_store", "option"} {
		t.Run(wiring, func(t *testing.T) {
			count := int64(0)
			baseline := &store.ProgressCheckpoint{ID: 1, WorldID: "world-1", ObservedAt: "2026-06-30T23:00:00Z", Source: "save_import", SourceTimeKind: "file_mtime", SchemaVersion: 1, Consistent: true, Boundary: "baseline", Metrics: map[string]store.ProgressMetric{"owned_pals": {State: "known", Value: &count}, "paldeck": {State: "unknown", Reason: "not_collected"}}}
			want := store.PlayerProgress{UserID: "steam_1", Status: "available", Baseline: baseline,
				Checkpoints:     []store.ProgressCheckpoint{{ID: 2, WorldID: "world-1", ObservedAt: "2026-07-01T01:00:00Z", Source: "save_import", SchemaVersion: 1, Consistent: true}},
				Changes:         []store.ProgressChange{{ID: 3, Metric: "owned_pals", Before: 0, After: 1, Delta: 1, Added: []string{"pal-1"}, Removed: []string{}, IntervalStart: "2026-06-30T23:00:00Z", IntervalEnd: "2026-07-01T01:00:00Z", PreviousCheckpointID: 1, CheckpointID: 2, RuleVersion: 1, Confidence: "observed", Source: "save_import"}},
				CheckpointTotal: 10, ChangeTotal: 8}
			repo := &progressQueriesFake{result: want}
			server := progressServer(progressAdminStore{progressQueriesFake: repo})
			if wiring == "option" {
				server = progressServer(fakeAdminStore{}, repo)
			}
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/players/steam_1/progress"+progressRange+"&limit=500", nil))
			if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
			}
			var got store.PlayerProgress
			if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("progress=%+v want=%+v", got, want)
			}
			if repo.calls != 1 || repo.userID != "steam_1" || repo.limit != 500 || repo.start.Format(time.RFC3339) != "2026-07-01T00:00:00Z" || repo.end.Sub(repo.start) != 31*24*time.Hour {
				t.Fatalf("query=%+v", repo)
			}
			for _, privateField := range []string{"level_sav", "source_path", "fingerprint", "parser_name", "password", "error"} {
				if strings.Contains(res.Body.String(), `"`+privateField+`"`) {
					t.Errorf("private field %q in public response: %s", privateField, res.Body.String())
				}
			}
		})
	}
}

func TestPublicPlayerProgressEmptyArraysAndDefaultLimit(t *testing.T) {
	repo := &progressQueriesFake{result: store.PlayerProgress{Status: "not_collected"}}
	server := progressServer(fakeAdminStore{}, repo)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/players/steam_1/progress"+progressRange, nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	var got store.PlayerProgress
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.UserID != "steam_1" || got.Status != "not_collected" || got.Baseline != nil || got.Checkpoints == nil || got.Changes == nil || len(got.Checkpoints) != 0 || len(got.Changes) != 0 || repo.limit != 200 {
		t.Fatalf("result=%+v limit=%d", got, repo.limit)
	}
}

func TestPublicPlayerProgressRejectsInvalidQueriesBeforeRepository(t *testing.T) {
	for _, query := range []string{
		"", "?start=2026-07-01T00:00:00Z", "?end=2026-08-01T00:00:00Z",
		"?start=invalid&end=2026-08-01T00:00:00Z",
		"?start=2026-07-01T00:00:00Z&end=invalid",
		"?start=2026-07-01&end=2026-08-01",
		"?start=2026-08-01T00:00:00Z&end=2026-08-01T00:00:00Z",
		"?start=2026-08-02T00:00:00Z&end=2026-08-01T00:00:00Z",
		"?start=2026-07-01T00:00:00Z&end=2026-08-01T00:00:01Z",
		"?start=0001-01-01T00:00:00Z&end=0001-01-02T00:00:00Z",
		progressRange + "&limit=0", progressRange + "&limit=-1", progressRange + "&limit=501",
		progressRange + "&limit=2001", progressRange + "&limit=x", progressRange + "&limit=",
		progressRange + "&limit=1&limit=2", progressRange + "&start=2026-07-01T00:00:00Z",
		progressRange + "&end=2026-08-01T00:00:00Z", progressRange + "&extra=value",
		progressRange + "&secret=one;extra=two", progressRange + "&extra=%zz",
	} {
		t.Run(query, func(t *testing.T) {
			repo := &progressQueriesFake{}
			res := httptest.NewRecorder()
			progressServer(fakeAdminStore{}, repo).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/players/steam_1/progress"+query, nil))
			if res.Code != http.StatusBadRequest || repo.calls != 0 {
				t.Fatalf("code=%d calls=%d body=%s", res.Code, repo.calls, res.Body.String())
			}
		})
	}
}

func TestPublicPlayerProgressRejectsBlankUserID(t *testing.T) {
	repo := &progressQueriesFake{}
	res := httptest.NewRecorder()
	progressServer(fakeAdminStore{}, repo).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/players/%20/progress"+progressRange, nil))
	if res.Code != http.StatusBadRequest || repo.calls != 0 {
		t.Fatalf("code=%d calls=%d body=%s", res.Code, repo.calls, res.Body.String())
	}
}

func TestPublicPlayerProgressInvalidLimitReportsEndpointMaximum(t *testing.T) {
	for _, limit := range []string{"0", "501", "2001", "x", ""} {
		res := httptest.NewRecorder()
		progressServer(fakeAdminStore{}, &progressQueriesFake{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/players/steam_1/progress"+progressRange+"&limit="+limit, nil))
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"message":"limit must be between 1 and 500"`) {
			t.Errorf("limit=%q code=%d body=%s", limit, res.Code, res.Body.String())
		}
	}
}

func TestPublicPlayerProgressMapsErrorsWithoutInternalDetails(t *testing.T) {
	for _, tt := range []struct {
		name, code, message string
		err                 error
		status              int
	}{
		{"not_found", "not_found", "player not found", fmt.Errorf("query /private/Level.sav: %w", store.ErrNotFound), http.StatusNotFound},
		{"query_failure", "query_failed", "progress query failed", errors.New("SQL failed: /private/Level.sav password=hunter2"), http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &progressQueriesFake{err: tt.err}
			res := httptest.NewRecorder()
			progressServer(fakeAdminStore{}, repo).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/players/steam_1/progress"+progressRange, nil))
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if res.Code != tt.status || body.Error.Code != tt.code || body.Error.Message != tt.message || repo.calls != 1 {
				t.Fatalf("code=%d calls=%d body=%s", res.Code, repo.calls, res.Body.String())
			}
			for _, secret := range []string{"/private", "Level.sav", "SQL", "password", "hunter2"} {
				if strings.Contains(res.Body.String(), secret) {
					t.Errorf("internal detail %q leaked: %s", secret, res.Body.String())
				}
			}
		})
	}
}

func TestPublicPlayerProgressUnavailable(t *testing.T) {
	res := httptest.NewRecorder()
	progressServer(fakeAdminStore{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/players/steam_1/progress"+progressRange, nil))
	if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), `"message":"progress query unavailable"`) {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
}
