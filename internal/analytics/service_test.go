package analytics

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type fakeRecorder struct {
	observations []store.AnalyticsObservation
	err          error
	cleanups     []struct {
		cutoff time.Time
		date   string
		batch  int
	}
	cleanupErr error
}

func (r *fakeRecorder) RecordAnalyticsObservation(_ context.Context, observation store.AnalyticsObservation) error {
	r.observations = append(r.observations, observation)
	return r.err
}

func (r *fakeRecorder) CleanupAnalytics(_ context.Context, cutoff time.Time, date string, batch int) error {
	r.cleanups = append(r.cleanups, struct {
		cutoff time.Time
		date   string
		batch  int
	}{cutoff, date, batch})
	return r.cleanupErr
}

func TestObserveSchedulesCleanupDailyWithLocalCutoffAndIgnoresFailure(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	repo := &fakeRecorder{cleanupErr: errors.New("cleanup failed")}
	s := New(repo, time.Minute, loc)
	at := mustTime(t, "2026-07-11T16:30:00Z")
	if err := s.Observe(t.Context(), at, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Observe(t.Context(), at.Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	if len(repo.cleanups) != 1 {
		t.Fatalf("cleanups=%d", len(repo.cleanups))
	}
	if repo.cleanups[0].cutoff != at.AddDate(0, 0, -90) || repo.cleanups[0].date != "2026-04-13" || repo.cleanups[0].batch != 500 {
		t.Fatalf("cleanup=%#v", repo.cleanups[0])
	}
	if err := s.Observe(t.Context(), at.Add(24*time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	if len(repo.cleanups) != 2 {
		t.Fatalf("cleanups=%d", len(repo.cleanups))
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestNewRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name     string
		repo     Recorder
		maxGap   time.Duration
		location *time.Location
	}{
		{name: "nil recorder", maxGap: time.Minute, location: time.UTC},
		{name: "non-positive max gap", repo: &fakeRecorder{}, location: time.UTC},
		{name: "nil location", repo: &fakeRecorder{}, maxGap: time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("New did not panic")
				}
			}()
			New(tt.repo, tt.maxGap, tt.location)
		})
	}
}

func TestSetLocationAppliesProspectivelyWithoutRebucketingHistory(t *testing.T) {
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	repo := &fakeRecorder{}
	s := New(repo, time.Hour, shanghai)
	first := mustTime(t, "2026-07-11T16:30:00Z")
	if err := s.Observe(t.Context(), first, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLocation(nil); err == nil {
		t.Fatal("SetLocation(nil) succeeded")
	}
	if err := s.SetLocation(time.UTC); err != nil {
		t.Fatal(err)
	}
	if err := s.Observe(t.Context(), first.Add(30*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if got := []string{repo.observations[0].LocalDate, repo.observations[1].LocalDate}; !reflect.DeepEqual(got, []string{"2026-07-12", "2026-07-11"}) {
		t.Fatalf("local dates=%v; timezone changes must not rebucket historical rows", got)
	}
}

func TestSetLocationDropsCrossTimezoneIntervalButRetainsOnlineState(t *testing.T) {
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	repo := &fakeRecorder{}
	s := New(repo, time.Hour, shanghai)
	u1 := domain.Player{UserID: "u1", Name: "One"}
	first := mustTime(t, "2026-07-11T16:30:00Z")
	if err := s.Observe(t.Context(), first, []domain.Player{u1}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLocation(time.UTC); err != nil {
		t.Fatal(err)
	}
	ids, asOf := s.Current()
	if !reflect.DeepEqual(ids, []string{"u1"}) || !asOf.IsZero() {
		t.Fatalf("current ids=%v as_of=%s", ids, asOf)
	}

	if err := s.Observe(t.Context(), first.Add(30*time.Minute), []domain.Player{u1}); err != nil {
		t.Fatal(err)
	}
	transition := repo.observations[1]
	if len(transition.Intervals) != 0 || len(transition.JoinedUserIDs) != 0 || len(transition.LeftUserIDs) != 0 || len(transition.Players) != 1 || transition.LocalDate != "2026-07-11" {
		t.Fatalf("transition observation=%+v", transition)
	}
	if err := s.Observe(t.Context(), first.Add(time.Hour), []domain.Player{u1}); err != nil {
		t.Fatal(err)
	}
	if got := repo.observations[2].Intervals; len(got) == 0 || got[0].LocalDate != "2026-07-11" {
		t.Fatalf("new-zone intervals=%+v", got)
	}
	if err := s.Observe(t.Context(), first.Add(90*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if got := repo.observations[3].LeftUserIDs; !reflect.DeepEqual(got, []string{"u1"}) {
		t.Fatalf("left=%v", got)
	}
}

func TestSetLocationSameZoneRetainsContinuity(t *testing.T) {
	repo := &fakeRecorder{}
	s := New(repo, time.Hour, time.UTC)
	first := mustTime(t, "2026-07-11T16:30:00Z")
	if err := s.Observe(t.Context(), first, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLocation(time.UTC); err != nil {
		t.Fatal(err)
	}
	_, asOf := s.Current()
	if !asOf.Equal(first) {
		t.Fatalf("same-zone change cleared as_of=%s", asOf)
	}
	if err := s.Observe(t.Context(), first.Add(30*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if len(repo.observations[1].Intervals) == 0 {
		t.Fatal("same-zone change broke interval continuity")
	}
}

func TestObserveFirstObservationJoinsEveryoneWithoutInterval(t *testing.T) {
	repo := &fakeRecorder{}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip(err)
	}
	service := New(repo, time.Minute, location)
	at := mustTime(t, "2026-07-11T01:02:03+08:00")
	players := []domain.Player{{UserID: "z", Name: "Zed"}, {UserID: "a", Name: "Ada"}}

	if err := service.Observe(t.Context(), at, players); err != nil {
		t.Fatal(err)
	}
	want := store.AnalyticsObservation{
		At: at.UTC(), LocalDate: "2026-07-11", Players: []domain.Player{players[1], players[0]}, JoinedUserIDs: []string{"a", "z"}, LeftUserIDs: []string{},
	}
	if len(repo.observations) != 1 || !reflect.DeepEqual(repo.observations[0], want) {
		t.Fatalf("observation = %#v, want %#v", repo.observations, want)
	}
}

func TestObserveContinuationAttributesGapToPreviousPlayers(t *testing.T) {
	repo := &fakeRecorder{}
	service := New(repo, time.Minute, time.UTC)
	start := mustTime(t, "2026-07-11T00:01:00Z")
	player := domain.Player{UserID: "u1", Name: "One"}
	if err := service.Observe(t.Context(), start, []domain.Player{player}); err != nil {
		t.Fatal(err)
	}
	if err := service.Observe(t.Context(), start.Add(30*time.Second), []domain.Player{player}); err != nil {
		t.Fatal(err)
	}

	got := repo.observations[1]
	if len(got.JoinedUserIDs) != 0 || len(got.LeftUserIDs) != 0 {
		t.Fatalf("unexpected lifecycle: joined=%v left=%v", got.JoinedUserIDs, got.LeftUserIDs)
	}
	wantIntervals := []store.AnalyticsInterval{{
		Start: start, End: start.Add(30 * time.Second), OnlineUserIDs: []string{"u1"}, LocalDate: "2026-07-11",
	}}
	if !reflect.DeepEqual(got.Intervals, wantIntervals) {
		t.Fatalf("intervals = %#v, want %#v", got.Intervals, wantIntervals)
	}
	if got.Players[0].LastOnline != player.LastOnline {
		t.Fatal("player was modified")
	}
}

func TestRestoreContinuesOpenSessionsAndClosesMissingPlayers(t *testing.T) {
	repo, _ := store.Open(t.Context(), t.TempDir()+"/guard.db")
	defer repo.Close()
	start := mustTime(t, "2026-07-11T00:01:00Z")
	players := []domain.Player{{UserID: "u1", Name: "One"}, {UserID: "u2", Name: "Two"}}
	first := New(repo, time.Minute, time.UTC)
	if err := first.Observe(t.Context(), start, players); err != nil {
		t.Fatal(err)
	}
	restoredPlayers, restoredAt, err := repo.OpenAnalyticsPlayers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(repo, time.Minute, time.UTC)
	if err := restarted.Restore(restoredAt, restoredPlayers); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Observe(t.Context(), start.Add(30*time.Second), []domain.Player{{UserID: "u1", Name: "One"}}); err != nil {
		t.Fatal(err)
	}

	open, at, err := repo.OpenAnalyticsPlayers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].UserID != "u1" || !at.Equal(start.Add(30*time.Second)) {
		t.Fatalf("open=%+v at=%v", open, at)
	}
}

func TestRestoreRejectsInvalidOrInitializedState(t *testing.T) {
	service := New(&fakeRecorder{}, time.Minute, time.UTC)
	if err := service.Restore(time.Time{}, []domain.Player{{UserID: "u1"}}); err == nil {
		t.Fatal("expected zero baseline error")
	}
	at := mustTime(t, "2026-07-11T00:01:00Z")
	if err := service.Restore(at, []domain.Player{{UserID: "u1"}}); err != nil {
		t.Fatal(err)
	}
	if err := service.Restore(at, nil); err == nil {
		t.Fatal("expected repeated restore error")
	}
	if err := service.Restore(time.Time{}, nil); err == nil {
		t.Fatal("expected empty repeated restore error")
	}
}

func TestObserveReportsJoinsAndLeavesInSortedOrder(t *testing.T) {
	repo := &fakeRecorder{}
	service := New(repo, time.Minute, time.UTC)
	start := mustTime(t, "2026-07-11T00:01:00Z")
	if err := service.Observe(t.Context(), start, []domain.Player{{UserID: "b"}, {UserID: "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := service.Observe(t.Context(), start.Add(time.Second), []domain.Player{{UserID: "d"}, {UserID: "c"}}); err != nil {
		t.Fatal(err)
	}
	got := repo.observations[1]
	if !reflect.DeepEqual(got.JoinedUserIDs, []string{"c", "d"}) || !reflect.DeepEqual(got.LeftUserIDs, []string{"a", "b"}) {
		t.Fatalf("joined=%v left=%v", got.JoinedUserIDs, got.LeftUserIDs)
	}
}

func TestObserveLongGapHasNoIntervalsAndEstablishesBaseline(t *testing.T) {
	repo := &fakeRecorder{}
	service := New(repo, time.Minute, time.UTC)
	start := mustTime(t, "2026-07-11T00:01:00Z")
	if err := service.Observe(t.Context(), start, []domain.Player{{UserID: "old"}}); err != nil {
		t.Fatal(err)
	}
	second := start.Add(2 * time.Minute)
	if err := service.Observe(t.Context(), second, []domain.Player{{UserID: "new"}}); err != nil {
		t.Fatal(err)
	}
	if len(repo.observations[1].Intervals) != 0 {
		t.Fatalf("intervals = %v", repo.observations[1].Intervals)
	}
	if err := service.Observe(t.Context(), second.Add(30*time.Second), []domain.Player{{UserID: "new"}}); err != nil {
		t.Fatal(err)
	}
	if got := repo.observations[2].Intervals; len(got) != 1 || !got[0].Start.Equal(second) {
		t.Fatalf("next intervals = %#v", got)
	}
}

func TestObserveRejectsInvalidInputBeforeRecorderAndStateChange(t *testing.T) {
	tests := []struct {
		name    string
		at      time.Time
		players []domain.Player
	}{
		{name: "zero time", players: []domain.Player{{UserID: "u1"}}},
		{name: "empty ID", at: time.Now(), players: []domain.Player{{UserID: ""}}},
		{name: "duplicate ID", at: time.Now(), players: []domain.Player{{UserID: "u1"}, {UserID: "u1"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRecorder{}
			service := New(repo, time.Minute, time.UTC)
			if err := service.Observe(t.Context(), tt.at, tt.players); err == nil {
				t.Fatal("Observe succeeded")
			}
			if len(repo.observations) != 0 {
				t.Fatal("recorder was called")
			}
			ids, asOf := service.Current()
			if ids != nil || !asOf.IsZero() {
				t.Fatalf("state = %v, %v", ids, asOf)
			}
		})
	}
}

func TestObserveRejectsNonIncreasingTimestamp(t *testing.T) {
	repo := &fakeRecorder{}
	service := New(repo, time.Minute, time.UTC)
	at := mustTime(t, "2026-07-11T00:00:00Z")
	if err := service.Observe(t.Context(), at, nil); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []time.Time{at, at.Add(-time.Second)} {
		if err := service.Observe(t.Context(), invalid, nil); err == nil {
			t.Fatalf("Observe(%v) succeeded", invalid)
		}
	}
	if len(repo.observations) != 1 {
		t.Fatalf("calls = %d", len(repo.observations))
	}
}

func TestObserveRejectsSubMillisecondAdvanceBeforeRecorder(t *testing.T) {
	repo := &fakeRecorder{}
	service := New(repo, time.Minute, time.UTC)
	start := mustTime(t, "2026-07-11T00:00:00Z")
	if err := service.Observe(t.Context(), start, []domain.Player{{UserID: "u1"}}); err != nil {
		t.Fatal(err)
	}

	err := service.Observe(t.Context(), start.Add(500*time.Microsecond), []domain.Player{{UserID: "u2"}})
	if err == nil || !strings.Contains(err.Error(), "observation must advance by at least 1ms") {
		t.Fatalf("error = %v, want minimum advance error", err)
	}
	if len(repo.observations) != 1 {
		t.Fatalf("recorder calls = %d, want 1", len(repo.observations))
	}
	ids, asOf := service.Current()
	if !reflect.DeepEqual(ids, []string{"u1"}) || !asOf.Equal(start) {
		t.Fatalf("Current = %v, %v; want [u1], %v", ids, asOf, start)
	}
}

func TestObserveRecorderFailureLeavesStateUnchangedAndCanRetry(t *testing.T) {
	repo := &fakeRecorder{}
	service := New(repo, time.Minute, time.UTC)
	start := mustTime(t, "2026-07-11T00:00:00Z")
	if err := service.Observe(t.Context(), start, []domain.Player{{UserID: "old"}}); err != nil {
		t.Fatal(err)
	}
	repo.err = errors.New("database unavailable")
	next := start.Add(30 * time.Second)
	if err := service.Observe(t.Context(), next, []domain.Player{{UserID: "new"}}); !errors.Is(err, repo.err) {
		t.Fatalf("error = %v", err)
	}
	ids, asOf := service.Current()
	if !reflect.DeepEqual(ids, []string{"old"}) || !asOf.Equal(start) {
		t.Fatalf("state = %v, %v", ids, asOf)
	}
	repo.err = nil
	if err := service.Observe(t.Context(), next, []domain.Player{{UserID: "new"}}); err != nil {
		t.Fatal(err)
	}
	got := repo.observations[len(repo.observations)-1]
	if !reflect.DeepEqual(got.JoinedUserIDs, []string{"new"}) || !reflect.DeepEqual(got.LeftUserIDs, []string{"old"}) {
		t.Fatalf("retry lifecycle = %#v", got)
	}
}

func TestObserveSplitsAtUTCBucketBoundaryIncludingExactEnd(t *testing.T) {
	repo := &fakeRecorder{}
	service := New(repo, 10*time.Minute, time.UTC)
	start := mustTime(t, "2026-07-11T00:04:30Z")
	if err := service.Observe(t.Context(), start, nil); err != nil {
		t.Fatal(err)
	}
	end := mustTime(t, "2026-07-11T00:10:00Z")
	if err := service.Observe(t.Context(), end, nil); err != nil {
		t.Fatal(err)
	}
	want := []store.AnalyticsInterval{
		{Start: start, End: mustTime(t, "2026-07-11T00:05:00Z"), OnlineUserIDs: []string{}, LocalDate: "2026-07-11"},
		{Start: mustTime(t, "2026-07-11T00:05:00Z"), End: end, OnlineUserIDs: []string{}, LocalDate: "2026-07-11"},
	}
	if !reflect.DeepEqual(repo.observations[1].Intervals, want) {
		t.Fatalf("intervals = %#v, want %#v", repo.observations[1].Intervals, want)
	}
}

func TestObserveOmitsSubMillisecondFragmentsAtUTCBucketBoundary(t *testing.T) {
	repo := &fakeRecorder{}
	service := New(repo, time.Minute, time.UTC)
	start := mustTime(t, "2026-07-11T00:04:59.999500Z")
	end := mustTime(t, "2026-07-11T00:05:00.000500Z")
	if err := service.Observe(t.Context(), start, []domain.Player{{UserID: "u1"}}); err != nil {
		t.Fatal(err)
	}
	if err := service.Observe(t.Context(), end, []domain.Player{{UserID: "u1"}}); err != nil {
		t.Fatal(err)
	}
	if len(repo.observations) != 2 {
		t.Fatalf("recorder calls = %d, want 2", len(repo.observations))
	}
	if got := repo.observations[1].Intervals; len(got) != 0 {
		t.Fatalf("intervals = %#v, want none", got)
	}
	ids, asOf := service.Current()
	if !reflect.DeepEqual(ids, []string{"u1"}) || !asOf.Equal(end) {
		t.Fatalf("Current = %v, %v; want [u1], %v", ids, asOf, end)
	}
}

func TestObserveOmitsSubMillisecondFragmentsAtLocalMidnight(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip(err)
	}
	repo := &fakeRecorder{}
	service := New(repo, time.Minute, location)
	start := mustTime(t, "2026-07-11T15:59:59.999500Z")
	end := mustTime(t, "2026-07-11T16:00:00.000500Z")
	if err := service.Observe(t.Context(), start, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Observe(t.Context(), end, nil); err != nil {
		t.Fatal(err)
	}
	if len(repo.observations) != 2 {
		t.Fatalf("recorder calls = %d, want 2", len(repo.observations))
	}
	if got := repo.observations[1].Intervals; len(got) != 0 {
		t.Fatalf("intervals = %#v, want none", got)
	}
	_, asOf := service.Current()
	if !asOf.Equal(end) {
		t.Fatalf("Current as-of = %v, want %v", asOf, end)
	}
}

func TestObservePreservesValidFragmentAfterTinyMidnightEdge(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip(err)
	}
	repo := &fakeRecorder{}
	service := New(repo, time.Minute, location)
	start := mustTime(t, "2026-07-11T15:59:59.999500Z")
	midnight := mustTime(t, "2026-07-11T16:00:00Z")
	end := mustTime(t, "2026-07-11T16:00:00.002500Z")
	if err := service.Observe(t.Context(), start, []domain.Player{{UserID: "u1"}}); err != nil {
		t.Fatal(err)
	}
	if err := service.Observe(t.Context(), end, []domain.Player{{UserID: "u1"}}); err != nil {
		t.Fatal(err)
	}
	want := []store.AnalyticsInterval{{
		Start: midnight, End: end, OnlineUserIDs: []string{"u1"}, LocalDate: "2026-07-12",
	}}
	if got := repo.observations[1].Intervals; !reflect.DeepEqual(got, want) {
		t.Fatalf("intervals = %#v, want %#v", got, want)
	}
}

func TestObserveSplitsAtAsiaShanghaiMidnight(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip(err)
	}
	repo := &fakeRecorder{}
	service := New(repo, 10*time.Minute, location)
	start := mustTime(t, "2026-07-11T15:59:30Z")
	end := mustTime(t, "2026-07-11T16:00:30Z")
	if err := service.Observe(t.Context(), start, []domain.Player{{UserID: "u1"}}); err != nil {
		t.Fatal(err)
	}
	if err := service.Observe(t.Context(), end, []domain.Player{{UserID: "u1"}}); err != nil {
		t.Fatal(err)
	}
	wantDates := []string{"2026-07-11", "2026-07-12"}
	got := repo.observations[1]
	if got.LocalDate != "2026-07-12" || len(got.Intervals) != 2 {
		t.Fatalf("observation = %#v", got)
	}
	for i, interval := range got.Intervals {
		if interval.LocalDate != wantDates[i] {
			t.Fatalf("interval %d date = %s", i, interval.LocalDate)
		}
	}
}

func TestNextLocalMidnightUsesCalendarBoundaryAcrossDST(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip(err)
	}
	tests := []struct {
		name     string
		start    time.Time
		want     time.Time
		dayWidth time.Duration
	}{
		{
			name:     "spring forward day",
			start:    time.Date(2026, 3, 8, 0, 0, 0, 0, location),
			want:     time.Date(2026, 3, 9, 0, 0, 0, 0, location),
			dayWidth: 23 * time.Hour,
		},
		{
			name:     "fall back day",
			start:    time.Date(2026, 11, 1, 0, 0, 0, 0, location),
			want:     time.Date(2026, 11, 2, 0, 0, 0, 0, location),
			dayWidth: 25 * time.Hour,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextLocalMidnight(tt.start, location)
			if !got.Equal(tt.want) {
				t.Fatalf("nextLocalMidnight() = %v, want %v", got, tt.want)
			}
			if elapsed := got.Sub(tt.start); elapsed != tt.dayWidth {
				t.Fatalf("elapsed = %v, want %v", elapsed, tt.dayWidth)
			}
		})
	}
}

func TestCurrentReturnsSortedIndependentCopyAndAsOf(t *testing.T) {
	repo := &fakeRecorder{}
	service := New(repo, time.Minute, time.UTC)
	at := mustTime(t, "2026-07-11T00:00:00Z")
	if err := service.Observe(t.Context(), at, []domain.Player{{UserID: "z"}, {UserID: "a"}}); err != nil {
		t.Fatal(err)
	}
	ids, asOf := service.Current()
	if !reflect.DeepEqual(ids, []string{"a", "z"}) || !asOf.Equal(at) {
		t.Fatalf("Current = %v, %v", ids, asOf)
	}
	ids[0] = "changed"
	again, _ := service.Current()
	if !reflect.DeepEqual(again, []string{"a", "z"}) {
		t.Fatalf("Current shared storage: %v", again)
	}
}
