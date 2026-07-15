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

	analyticssvc "github.com/kevinmatt/palworld-playtime-guard/internal/analytics"
	"github.com/kevinmatt/palworld-playtime-guard/internal/config"
	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type fakeAnalyticsQueries struct {
	ranking  []store.RankingRow
	buckets  []store.ConcurrencyBucket
	daily    []store.DailyActivity
	player   domain.Player
	fps      []store.ServerFPSPoint
	ping     []store.PingSummaryPoint
	err      error
	dailyErr error
}

func (f fakeAnalyticsQueries) Ranking(context.Context, string, string) ([]store.RankingRow, error) {
	return f.ranking, f.err
}
func (f fakeAnalyticsQueries) Concurrency(context.Context, time.Time, time.Time) ([]store.ConcurrencyBucket, error) {
	return f.buckets, f.err
}
func (f fakeAnalyticsQueries) PlayerDailyActivity(context.Context, string, string, string) ([]store.DailyActivity, error) {
	return f.daily, f.dailyErr
}
func (f fakeAnalyticsQueries) Player(context.Context, string) (domain.Player, error) {
	return f.player, f.dailyErr
}
func (f fakeAnalyticsQueries) ServerFPSSeries(context.Context, time.Time, time.Time, int) ([]store.ServerFPSPoint, error) {
	return f.fps, f.err
}
func (f fakeAnalyticsQueries) PingSummarySeries(context.Context, time.Time, time.Time, int) ([]store.PingSummaryPoint, error) {
	return f.ping, f.err
}

type fakeAnalyticsOnline struct {
	ids []string
	at  time.Time
}

type dynamicAnalyticsOnline struct{ location *time.Location }

func (f *dynamicAnalyticsOnline) Current() ([]string, time.Time) { return nil, time.Time{} }
func (f *dynamicAnalyticsOnline) SetLocation(location *time.Location) error {
	f.location = location
	return nil
}

type observationRecorder struct{ observations []store.AnalyticsObservation }

func (r *observationRecorder) RecordAnalyticsObservation(_ context.Context, observation store.AnalyticsObservation) error {
	r.observations = append(r.observations, observation)
	return nil
}
func (*observationRecorder) CleanupAnalytics(context.Context, time.Time, string, int) error {
	return nil
}
func (*observationRecorder) RecordPingSummary(context.Context, store.PingSummaryInput) error {
	return nil
}

type mutablePolicies struct {
	value config.Policy
	err   error
	calls int
}

func (f *mutablePolicies) Policy() config.Policy { return f.value }
func (f *mutablePolicies) SetPolicy(_ context.Context, value config.Policy) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	f.value = value
	return nil
}

type currentOnlyAnalytics struct{}

func (currentOnlyAnalytics) Current() ([]string, time.Time) { return nil, time.Time{} }

func TestDirectPolicyUpdaterRejectsMissingLocationSetterBeforeUpdate(t *testing.T) {
	policies := &mutablePolicies{}
	err := (directPolicyUpdater{analytics: currentOnlyAnalytics{}}).ApplyPolicyTimezone(func() error {
		return policies.SetPolicy(t.Context(), config.DefaultPolicy())
	}, time.UTC)
	if err == nil || policies.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, policies.calls)
	}
}

type captureAnalyticsQueries struct {
	rankingCalls     [][2]string
	concurrencyCalls [][2]time.Time
	player           domain.Player
}

func (f *captureAnalyticsQueries) Ranking(_ context.Context, start, end string) ([]store.RankingRow, error) {
	f.rankingCalls = append(f.rankingCalls, [2]string{start, end})
	return []store.RankingRow{}, nil
}
func (f *captureAnalyticsQueries) Concurrency(_ context.Context, start, end time.Time) ([]store.ConcurrencyBucket, error) {
	f.concurrencyCalls = append(f.concurrencyCalls, [2]time.Time{start, end})
	return []store.ConcurrencyBucket{}, nil
}
func (f *captureAnalyticsQueries) PlayerDailyActivity(context.Context, string, string, string) ([]store.DailyActivity, error) {
	return []store.DailyActivity{}, nil
}
func (f *captureAnalyticsQueries) Player(context.Context, string) (domain.Player, error) {
	if f.player.UserID == "" {
		return domain.Player{}, store.ErrNotFound
	}
	return f.player, nil
}
func (f *captureAnalyticsQueries) ServerFPSSeries(context.Context, time.Time, time.Time, int) ([]store.ServerFPSPoint, error) {
	return nil, nil
}
func (f *captureAnalyticsQueries) PingSummarySeries(context.Context, time.Time, time.Time, int) ([]store.PingSummaryPoint, error) {
	return nil, nil
}

func (f fakeAnalyticsOnline) Current() ([]string, time.Time)   { return f.ids, f.at }
func (f fakeAnalyticsOnline) SetLocation(*time.Location) error { return nil }

func analyticsServer(q AnalyticsQueries, online AnalyticsOnline) *Server {
	s := testServer()
	s.analytics = q
	s.analyticsOnline = online
	s.now = func() time.Time { return time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC) }
	return s
}

func TestAnalyticsSummaryIncludesCurrentTodayAndPeak(t *testing.T) {
	peakAt := time.Date(2026, 7, 8, 2, 5, 0, 0, time.UTC)
	maximum := 3
	avg := 2.0
	q := fakeAnalyticsQueries{
		ranking: []store.RankingRow{{UserID: "u1", Name: "One", Observed: 90 * time.Minute}},
		buckets: []store.ConcurrencyBucket{{Start: peakAt, Average: &avg, Max: &maximum, MaxObservedAt: &peakAt, Coverage: 1}},
	}
	s := analyticsServer(q, fakeAnalyticsOnline{[]string{"u1"}, peakAt})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	var got struct {
		OnlineCount int       `json:"online_count"`
		AsOf        time.Time `json:"as_of"`
		Today       int64     `json:"today_observed_ms"`
		Peak        int       `json:"peak_count"`
		Active      int       `json:"active_players"`
		Ranking     []struct {
			UserID string `json:"user_id"`
			Online bool   `json:"online"`
		} `json:"ranking"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OnlineCount != 1 || !got.AsOf.Equal(peakAt) || got.Today != int64((90*time.Minute)/time.Millisecond) || got.Peak != 3 || got.Active != 1 || len(got.Ranking) != 1 || !got.Ranking[0].Online {
		t.Fatalf("response=%+v", got)
	}
}

func TestAnalyticsSummaryUsesNullAsOfBeforeFirstObservation(t *testing.T) {
	res := httptest.NewRecorder()
	analyticsServer(fakeAnalyticsQueries{}, fakeAnalyticsOnline{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if string(got["as_of"]) != "null" {
		t.Fatalf("as_of=%s body=%s", got["as_of"], res.Body.String())
	}
}

func TestAnalyticsActivityFillsConcurrencyAndPartialDaily(t *testing.T) {
	at := time.Date(2026, 7, 2, 16, 5, 0, 0, time.UTC)
	avg, maximum := 1.5, 2
	q := fakeAnalyticsQueries{buckets: []store.ConcurrencyBucket{{Start: at, Average: &avg, Max: &maximum, Coverage: .5}}, daily: []store.DailyActivity{{Date: "2026-07-06", Observed: time.Hour}}, player: domain.Player{UserID: "u1", Name: "One"}}
	s := analyticsServer(q, fakeAnalyticsOnline{})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/activity?range=7d&user_id=%20u1%20", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	var got struct {
		Concurrency []json.RawMessage `json:"concurrency"`
		Player      *struct {
			Name  string `json:"name"`
			Daily []struct {
				Date     string `json:"date"`
				Observed int64  `json:"observed_ms"`
			} `json:"daily"`
		} `json:"player"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Concurrency) != 7*24*12 || got.Player == nil || got.Player.Name != "One" || len(got.Player.Daily) != 7 || got.Player.Daily[4].Observed != int64(time.Hour/time.Millisecond) {
		t.Fatalf("concurrency=%d player=%+v", len(got.Concurrency), got.Player)
	}
}

func TestAnalyticsActivityCanSkipConcurrencyQuery(t *testing.T) {
	q := &captureAnalyticsQueries{player: domain.Player{UserID: "u1", Name: "One"}}
	res := httptest.NewRecorder()
	analyticsServer(q, fakeAnalyticsOnline{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/activity?range=7d&user_id=u1&include_concurrency=false", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	var got struct {
		Concurrency []json.RawMessage `json:"concurrency"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(q.concurrencyCalls) != 0 || got.Concurrency == nil || len(got.Concurrency) != 0 {
		t.Fatalf("calls=%v concurrency=%v", q.concurrencyCalls, got.Concurrency)
	}
}

func TestAnalyticsHealthReturnsFPSAndLatencySeries(t *testing.T) {
	at := time.Date(2026, 7, 8, 11, 0, 0, 0, time.UTC)
	p50, p90 := 40.0, 90.0
	q := fakeAnalyticsQueries{
		fps: []store.ServerFPSPoint{{At: at, FPS: 58, FrameTime: 17.2, Players: 3}},
		ping: []store.PingSummaryPoint{{
			At: at, SampleCount: 3, MissingCount: 0,
			P50: &p50, P90: &p90,
		}},
	}
	res := httptest.NewRecorder()
	analyticsServer(q, fakeAnalyticsOnline{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/health?range=6h", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	var got struct {
		Range         string `json:"range"`
		LatestFPS     int    `json:"latest_fps"`
		LatestPlayers int    `json:"latest_players"`
		LatestP50     float64 `json:"latest_p50"`
		LatestP90     float64 `json:"latest_p90"`
		FPS           []struct {
			FPS     int `json:"fps"`
			Players int `json:"players"`
		} `json:"fps"`
		Latency []struct {
			SampleCount int      `json:"sample_count"`
			P50         *float64 `json:"p50"`
			P90         *float64 `json:"p90"`
		} `json:"latency"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Range != "6h" || got.LatestFPS != 58 || got.LatestPlayers != 3 || got.LatestP50 != 40 || got.LatestP90 != 90 {
		t.Fatalf("latest fields: %+v", got)
	}
	if len(got.FPS) != 1 || got.FPS[0].FPS != 58 || len(got.Latency) != 1 || got.Latency[0].SampleCount != 3 {
		t.Fatalf("series: fps=%+v latency=%+v", got.FPS, got.Latency)
	}
}

func TestAnalyticsValidationAndErrors(t *testing.T) {
	for _, path := range []string{
		"/api/v1/analytics/summary?ranking=year",
		"/api/v1/analytics/activity?range=8d",
		"/api/v1/analytics/activity?include_concurrency=sometimes",
		"/api/v1/analytics/health?range=1h",
	} {
		res := httptest.NewRecorder()
		analyticsServer(fakeAnalyticsQueries{}, fakeAnalyticsOnline{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("%s code=%d", path, res.Code)
		}
	}
	res := httptest.NewRecorder()
	analyticsServer(fakeAnalyticsQueries{dailyErr: store.ErrNotFound}, fakeAnalyticsOnline{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/activity?user_id=missing", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("not found code=%d", res.Code)
	}
	res = httptest.NewRecorder()
	analyticsServer(fakeAnalyticsQueries{dailyErr: errors.New("db")}, fakeAnalyticsOnline{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/activity?user_id=u1", nil))
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("db code=%d", res.Code)
	}
}

func TestAnalyticsActivityEmptyKnownPlayerHasEmptyDaily(t *testing.T) {
	q := fakeAnalyticsQueries{player: domain.Player{UserID: "u1", Name: "One"}}
	res := httptest.NewRecorder()
	analyticsServer(q, fakeAnalyticsOnline{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/activity?user_id=u1", nil))
	var got struct {
		Player struct {
			Daily []any `json:"daily"`
		} `json:"player"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &got)
	if res.Code != http.StatusOK || got.Player.Daily == nil || len(got.Player.Daily) != 0 {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
}

func TestAnalyticsSummaryUsesShanghaiMondayAndTodayBoundaries(t *testing.T) {
	q := &captureAnalyticsQueries{}
	s := analyticsServer(q, fakeAnalyticsOnline{})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary?ranking=week", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	wantRanking := [][2]string{{"2026-07-06", "2026-07-13"}, {"2026-07-08", "2026-07-09"}}
	if len(q.rankingCalls) != 2 || q.rankingCalls[0] != wantRanking[0] || q.rankingCalls[1] != wantRanking[1] {
		t.Fatalf("ranking calls=%v", q.rankingCalls)
	}
	if len(q.concurrencyCalls) != 1 || q.concurrencyCalls[0][0].Format(time.RFC3339) != "2026-07-07T16:00:00Z" || q.concurrencyCalls[0][1].Format(time.RFC3339) != "2026-07-08T16:00:00Z" {
		t.Fatalf("concurrency calls=%v", q.concurrencyCalls)
	}
}

func TestAnalyticsActivityDSTIteratesUTCInsteadOfAssumingPointCount(t *testing.T) {
	q := &captureAnalyticsQueries{}
	s := analyticsServer(q, fakeAnalyticsOnline{})
	s.policies = fakePolicies{value: config.Policy{Timezone: "America/New_York"}}
	s.now = func() time.Time { return time.Date(2026, 3, 8, 16, 0, 0, 0, time.UTC) }
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/activity?range=7d", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	var got struct {
		Concurrency []json.RawMessage `json:"concurrency"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Concurrency) != 167*12 {
		t.Fatalf("points=%d want=%d", len(got.Concurrency), 167*12)
	}
}

func TestAnalyticsActivityFallBackDSTProducesOrderedNullGaps(t *testing.T) {
	q := &captureAnalyticsQueries{}
	s := analyticsServer(q, fakeAnalyticsOnline{})
	s.policies = fakePolicies{value: config.Policy{Timezone: "America/New_York"}}
	s.now = func() time.Time { return time.Date(2026, 11, 1, 17, 0, 0, 0, time.UTC) }
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/activity?range=7d", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	var got struct {
		Concurrency []struct {
			At       time.Time `json:"at"`
			Average  *float64  `json:"average_count"`
			Max      *int      `json:"max_count"`
			Coverage float64   `json:"coverage"`
		} `json:"concurrency"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Concurrency) != 169*12 {
		t.Fatalf("points=%d want=%d", len(got.Concurrency), 169*12)
	}
	if got.Concurrency[0].At.Format(time.RFC3339) != "2026-10-26T04:00:00Z" || got.Concurrency[len(got.Concurrency)-1].At.Format(time.RFC3339) != "2026-11-02T04:55:00Z" {
		t.Fatalf("first=%s last=%s", got.Concurrency[0].At, got.Concurrency[len(got.Concurrency)-1].At)
	}
	for i, point := range got.Concurrency {
		if point.Average != nil || point.Max != nil || point.Coverage != 0 {
			t.Fatalf("point %d not a null gap: %+v", i, point)
		}
		if i > 0 && point.At.Sub(got.Concurrency[i-1].At) != 5*time.Minute {
			t.Fatalf("unordered points at %d", i)
		}
	}
}

func TestPolicyTimezoneChangeUpdatesAnalyticsAndSubsequentQueryBounds(t *testing.T) {
	q := &captureAnalyticsQueries{}
	recorder := &observationRecorder{}
	analyticsService := analyticssvc.New(recorder, time.Hour, mustLocation(t, "Asia/Shanghai"))
	policy := config.DefaultPolicy()
	policies := &mutablePolicies{value: policy}
	s := testServer()
	s.analytics, s.analyticsOnline, s.policies = q, analyticsService, policies
	s.policyUpdater = directPolicyUpdater{analytics: analyticsService}
	s.now = func() time.Time { return time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC) }
	s.auth = newAdminAuth("admin", "secret")
	login := httptest.NewRecorder()
	s.Handler().ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"username":"admin","password":"secret"}`)))
	payload := policyPayload{Timezone: "UTC", Default: toRuleDTO(policy.Default), Overrides: map[string]overrideDTO{}}
	body, _ := json.Marshal(payload)
	put := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/policies", strings.NewReader(string(body)))
	req.AddCookie(login.Result().Cookies()[0])
	s.Handler().ServeHTTP(put, req)
	if put.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", put.Code, put.Body.String())
	}
	if err := analyticsService.Observe(t.Context(), time.Date(2026, 7, 8, 16, 30, 0, 0, time.UTC), nil); err != nil {
		t.Fatal(err)
	}
	if len(recorder.observations) != 1 || recorder.observations[0].LocalDate != "2026-07-08" {
		t.Fatalf("observations=%+v; timezone change must apply prospectively", recorder.observations)
	}

	for _, path := range []string{"/api/v1/analytics/summary", "/api/v1/analytics/activity"} {
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusOK {
			t.Fatalf("%s code=%d body=%s", path, res.Code, res.Body.String())
		}
	}
	if len(q.concurrencyCalls) != 2 || q.concurrencyCalls[0][0].Format(time.RFC3339) != "2026-07-08T00:00:00Z" || q.concurrencyCalls[1][0].Format(time.RFC3339) != "2026-07-02T00:00:00Z" {
		t.Fatalf("concurrency calls=%v", q.concurrencyCalls)
	}
}

func TestFailedPolicySaveDoesNotChangeAnalyticsLocation(t *testing.T) {
	policy := config.DefaultPolicy()
	recorder := &dynamicAnalyticsOnline{location: mustLocation(t, "Asia/Shanghai")}
	s := testServer()
	s.analyticsOnline = recorder
	s.policies = &mutablePolicies{value: policy, err: errors.New("save failed")}
	s.policyUpdater = directPolicyUpdater{analytics: recorder}
	s.auth = newAdminAuth("admin", "secret")
	login := httptest.NewRecorder()
	s.Handler().ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"username":"admin","password":"secret"}`)))
	payload := policyPayload{Timezone: "UTC", Default: toRuleDTO(policy.Default), Overrides: map[string]overrideDTO{}}
	body, _ := json.Marshal(payload)
	put := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/policies", strings.NewReader(string(body)))
	req.AddCookie(login.Result().Cookies()[0])
	s.Handler().ServeHTTP(put, req)
	if put.Code != http.StatusBadRequest || recorder.location.String() != "Asia/Shanghai" {
		t.Fatalf("code=%d location=%v body=%s", put.Code, recorder.location, put.Body.String())
	}
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
