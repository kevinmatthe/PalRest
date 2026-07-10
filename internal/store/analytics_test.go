package store

import (
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

func TestRecordAnalyticsObservationPersistsLifecycleAndAggregates(t *testing.T) {
	repo, _ := openTemp(t)
	ctx := t.Context()
	base := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	players := []domain.Player{{UserID: "u1", Name: "One"}, {UserID: "u2", Name: "Two"}}

	if err := repo.RecordAnalyticsObservation(ctx, AnalyticsObservation{
		At: base, Players: players, JoinedUserIDs: []string{"u1", "u2"},
		Intervals: []AnalyticsInterval{{Start: base, End: base.Add(30 * time.Second), OnlineUserIDs: []string{"u1", "u2"}, LocalDate: "2026-07-11"}},
	}); err != nil {
		t.Fatal(err)
	}

	var weighted, observed int64
	var maxCount int
	var peak string
	if err := repo.db.QueryRowContext(ctx, `SELECT weighted_count_ms, observed_ms, max_count, max_observed_at FROM concurrency_buckets WHERE bucket_start=?`, formatTime(base)).Scan(&weighted, &observed, &maxCount, &peak); err != nil {
		t.Fatal(err)
	}
	if weighted != 60000 || observed != 30000 || maxCount != 2 || peak != formatTime(base) {
		t.Fatalf("bucket=(%d,%d,%d,%s)", weighted, observed, maxCount, peak)
	}

	second := base.Add(30 * time.Second)
	if err := repo.RecordAnalyticsObservation(ctx, AnalyticsObservation{
		At: second, Players: players,
		Intervals: []AnalyticsInterval{{Start: second, End: second.Add(10 * time.Second), OnlineUserIDs: []string{"u1"}, LocalDate: "2026-07-11"}},
	}); err != nil {
		t.Fatal(err)
	}
	third := base.Add(40 * time.Second)
	if err := repo.RecordAnalyticsObservation(ctx, AnalyticsObservation{
		At: third, Players: players,
		Intervals: []AnalyticsInterval{{Start: third, End: third.Add(10 * time.Second), OnlineUserIDs: []string{"u1", "u2"}, LocalDate: "2026-07-11"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRowContext(ctx, `SELECT weighted_count_ms, observed_ms, max_count, max_observed_at FROM concurrency_buckets WHERE bucket_start=?`, formatTime(base)).Scan(&weighted, &observed, &maxCount, &peak); err != nil {
		t.Fatal(err)
	}
	if weighted != 90000 || observed != 50000 || maxCount != 2 || peak != formatTime(base) {
		t.Fatalf("accumulated bucket=(%d,%d,%d,%s)", weighted, observed, maxCount, peak)
	}

	var lastObserved string
	if err := repo.db.QueryRowContext(ctx, `SELECT last_observed_at FROM player_sessions WHERE user_id='u1' AND ended_at IS NULL`).Scan(&lastObserved); err != nil {
		t.Fatal(err)
	}
	if lastObserved != formatTime(third) {
		t.Fatalf("continuing last_observed_at=%s", lastObserved)
	}
	leaveAt := base.Add(time.Minute)
	if err := repo.RecordAnalyticsObservation(ctx, AnalyticsObservation{At: leaveAt, Players: []domain.Player{{UserID: "u2", Name: "Two"}}, LeftUserIDs: []string{"u1"}}); err != nil {
		t.Fatal(err)
	}
	var ended, closedLast, reason string
	if err := repo.db.QueryRowContext(ctx, `SELECT ended_at, last_observed_at, close_reason FROM player_sessions WHERE user_id='u1'`).Scan(&ended, &closedLast, &reason); err != nil {
		t.Fatal(err)
	}
	if ended != formatTime(leaveAt) || closedLast != formatTime(leaveAt) || reason != "observed_offline" {
		t.Fatalf("closed=(%s,%s,%s)", ended, closedLast, reason)
	}

	for userID, wantObserved := range map[string]int64{"u1": 50000, "u2": 40000} {
		var gotObserved int64
		var sessions int
		var first, last string
		if err := repo.db.QueryRowContext(ctx, `SELECT observed_ms, session_count, first_observed_at, last_observed_at FROM player_daily_stats WHERE user_id=? AND local_date='2026-07-11'`, userID).Scan(&gotObserved, &sessions, &first, &last); err != nil {
			t.Fatal(err)
		}
		if gotObserved != wantObserved || sessions != 1 || first != formatTime(base) || last != formatTime(third.Add(10*time.Second)) {
			t.Fatalf("daily %s=(%d,%d,%s,%s)", userID, gotObserved, sessions, first, last)
		}
	}
}

func TestRecordAnalyticsObservationCountsJoinWithoutInterval(t *testing.T) {
	repo, _ := openTemp(t)
	at := time.Date(2026, 7, 11, 23, 30, 0, 0, time.FixedZone("reporting", 8*60*60))
	if err := repo.RecordAnalyticsObservation(t.Context(), AnalyticsObservation{At: at, Players: []domain.Player{{UserID: "u1"}}, JoinedUserIDs: []string{"u1"}}); err != nil {
		t.Fatal(err)
	}
	var observed int64
	var sessions int
	var first, last string
	if err := repo.db.QueryRow(`SELECT observed_ms, session_count, first_observed_at, last_observed_at FROM player_daily_stats WHERE user_id='u1' AND local_date='2026-07-11'`).Scan(&observed, &sessions, &first, &last); err != nil {
		t.Fatal(err)
	}
	if observed != 0 || sessions != 1 || first != formatTime(at) || last != formatTime(at) {
		t.Fatalf("daily=(%d,%d,%s,%s)", observed, sessions, first, last)
	}
}

func TestRecordAnalyticsObservationInvalidIntervalRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start time.Time
		end   time.Time
	}{
		{name: "nonpositive", start: time.Date(2026, 7, 11, 0, 0, 1, 0, time.UTC), end: time.Date(2026, 7, 11, 0, 0, 1, 0, time.UTC)},
		{name: "cross bucket", start: time.Date(2026, 7, 11, 0, 4, 59, 0, time.UTC), end: time.Date(2026, 7, 11, 0, 5, 1, 0, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := openTemp(t)
			err := repo.RecordAnalyticsObservation(t.Context(), AnalyticsObservation{
				At: tc.start, Players: []domain.Player{{UserID: "u1"}}, JoinedUserIDs: []string{"u1"},
				Intervals: []AnalyticsInterval{{Start: tc.start, End: tc.end, OnlineUserIDs: []string{"u1"}, LocalDate: "2026-07-11"}},
			})
			if err == nil {
				t.Fatal("expected validation error")
			}
			assertAnalyticsTableCounts(t, repo, 0, 0, 0, 0)
		})
	}
}

func TestRecordAnalyticsObservationUnknownOnlinePlayerRollsBack(t *testing.T) {
	repo, _ := openTemp(t)
	base := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	err := repo.RecordAnalyticsObservation(t.Context(), AnalyticsObservation{
		At: base, Players: []domain.Player{{UserID: "u1"}}, JoinedUserIDs: []string{"u1"},
		Intervals: []AnalyticsInterval{{Start: base, End: base.Add(time.Second), OnlineUserIDs: []string{"missing"}, LocalDate: "2026-07-11"}},
	})
	if err == nil {
		t.Fatal("expected unknown player error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("error %q does not report a foreign-key constraint failure", err)
	}
	assertAnalyticsTableCounts(t, repo, 0, 0, 0, 0)
}

func assertAnalyticsTableCounts(t *testing.T, repo *Repository, players, sessions, buckets, daily int) {
	t.Helper()
	for table, want := range map[string]int{"players": players, "player_sessions": sessions, "concurrency_buckets": buckets, "player_daily_stats": daily} {
		var got int
		if err := repo.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s count=%d want=%d", table, got, want)
		}
	}
}
