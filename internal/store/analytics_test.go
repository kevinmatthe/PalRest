package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

func TestOpenAnalyticsPlayersSurvivesReopenAndUsesEarliestBaseline(t *testing.T) {
	repo, path := openTemp(t)
	ctx := t.Context()
	base := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	players := []domain.Player{{UserID: "u2", Name: "Two"}, {UserID: "u1", Name: "One"}}
	if err := repo.RecordAnalyticsObservation(ctx, AnalyticsObservation{
		At: base, LocalDate: "2026-07-11", Players: players, JoinedUserIDs: []string{"u1", "u2"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE player_sessions SET last_observed_at=? WHERE user_id='u2'`, formatTime(base.Add(time.Second))); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	got, at, err := repo.OpenAnalyticsPlayers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []domain.Player{{UserID: "u1", Name: "One", LastOnline: base}, {UserID: "u2", Name: "Two", LastOnline: base}}) || !at.Equal(base) {
		t.Fatalf("players=%+v at=%v", got, at)
	}
}

func TestAnalyticsQueries(t *testing.T) {
	repo, _ := openTemp(t)
	ctx := t.Context()
	at := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	for _, p := range []domain.Player{{UserID: "u1", Name: "bob"}, {UserID: "u2", Name: "Ada"}, {UserID: "u3", Name: "ada"}} {
		if err := repo.WithTx(ctx, func(tx *Tx) error { return tx.UpsertPlayer(p, at) }); err != nil {
			t.Fatal(err)
		}
	}
	for _, x := range []struct {
		id string
		ms int64
	}{{"u1", 2000}, {"u2", 1000}, {"u3", 1000}} {
		_, err := repo.db.ExecContext(ctx, `INSERT INTO player_daily_stats VALUES(?,?,?,?,?,?)`, x.id, "2026-07-09", x.ms, formatTime(at), formatTime(at), 1)
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err := repo.Ranking(ctx, "2026-07-09", "2026-07-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 3 || r[0].UserID != "u1" || r[1].UserID != "u2" || r[2].UserID != "u3" {
		t.Fatalf("ranking=%#v", r)
	}
	_, _ = repo.db.ExecContext(ctx, `INSERT INTO concurrency_buckets VALUES(?,?,?,?,?)`, formatTime(at), 900000, 600000, 2, formatTime(at.Add(time.Minute)))
	_, _ = repo.db.ExecContext(ctx, `INSERT INTO concurrency_buckets VALUES(?,?,?,?,?)`, formatTime(at.Add(5*time.Minute)), 0, 0, 0, "malformed")
	b, err := repo.Concurrency(ctx, at, at.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 2 || b[0].Average == nil || *b[0].Average != 1.5 || b[0].Coverage != 1 || b[0].Max == nil || *b[0].Max != 2 || b[0].MaxObservedAt == nil || !b[0].MaxObservedAt.Equal(at.Add(time.Minute)) || b[1].Average != nil || b[1].Max != nil || b[1].MaxObservedAt != nil || b[1].Coverage != 0 {
		t.Fatalf("buckets=%#v", b)
	}
	d, err := repo.PlayerDailyActivity(ctx, "u1", "2026-07-09", "2026-07-11")
	if err != nil || len(d) != 1 || d[0].Observed != 2*time.Second {
		t.Fatalf("daily=%#v err=%v", d, err)
	}
	if _, err = repo.PlayerDailyActivity(ctx, "missing", "2026-07-09", "2026-07-11"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	emptyRanking, _ := repo.Ranking(ctx, "2026-07-10", "2026-07-11")
	emptyBuckets, _ := repo.Concurrency(ctx, at.Add(time.Hour), at.Add(2*time.Hour))
	emptyDaily, _ := repo.PlayerDailyActivity(ctx, "u1", "2026-07-10", "2026-07-11")
	if emptyRanking == nil || emptyBuckets == nil || emptyDaily == nil {
		t.Fatal("empty query returned nil slice")
	}
}

func TestCleanupAnalyticsBatchesAndPreservesOpenSessions(t *testing.T) {
	repo, _ := openTemp(t)
	ctx := t.Context()
	at := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := repo.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []domain.Player{{UserID: "u1"}, {UserID: "u2"}, {UserID: "u3"}, {UserID: "open"}} {
		if err := repo.WithTx(ctx, func(tx *Tx) error { return tx.UpsertPlayer(p, at) }); err != nil {
			t.Fatal(err)
		}
	}
	cutoff := at.Add(10 * time.Minute)
	for _, id := range []string{"u1", "u2", "u3"} {
		mustExec(`INSERT INTO player_sessions(user_id,started_at,last_observed_at,ended_at) VALUES(?,?,?,?)`, id, formatTime(at.Add(-time.Hour)), formatTime(at), formatTime(at))
	}
	mustExec(`INSERT INTO player_sessions(user_id,started_at,last_observed_at,ended_at) VALUES(?,?,?,?)`, "open", formatTime(at), formatTime(at), nil)
	mustExec(`INSERT INTO player_sessions(user_id,started_at,last_observed_at,ended_at) VALUES(?,?,?,?)`, "open", formatTime(at.Add(-time.Hour)), formatTime(at), formatTime(cutoff))
	for i := 0; i < 3; i++ {
		mustExec(`INSERT INTO concurrency_buckets VALUES(?,?,?,?,?)`, formatTime(at.Add(time.Duration(i)*time.Minute)), 1, 1, 1, formatTime(at))
		mustExec(`INSERT INTO player_daily_stats VALUES(?,?,?,?,?,?)`, []string{"u1", "u2", "u3"}[i], "2026-07-0"+string(rune('6'+i)), 1, formatTime(at), formatTime(at), 1)
	}
	mustExec(`INSERT INTO concurrency_buckets VALUES(?,?,?,?,?)`, formatTime(cutoff), 1, 1, 1, formatTime(at))
	mustExec(`INSERT INTO player_daily_stats VALUES(?,?,?,?,?,?)`, "open", "2026-07-10", 1, formatTime(at), formatTime(at), 1)
	if err := repo.CleanupAnalytics(ctx, cutoff, "2026-07-10", 1); err != nil {
		t.Fatal(err)
	}
	for q, w := range map[string]int{`SELECT count(*) FROM player_sessions WHERE ended_at<?`: 2, `SELECT count(*) FROM player_sessions WHERE ended_at IS NULL`: 1, `SELECT count(*) FROM concurrency_buckets WHERE bucket_start<?`: 2, `SELECT count(*) FROM player_daily_stats WHERE local_date<'2026-07-10'`: 2} {
		var n int
		if arg, ok := map[string]any{`SELECT count(*) FROM player_sessions WHERE ended_at<?`: formatTime(cutoff), `SELECT count(*) FROM concurrency_buckets WHERE bucket_start<?`: formatTime(cutoff)}[q]; ok {
			_ = repo.db.QueryRowContext(ctx, q, arg).Scan(&n)
		} else {
			_ = repo.db.QueryRowContext(ctx, q).Scan(&n)
		}
		if n != w {
			t.Fatalf("%s=%d", q, n)
		}
	}
	for i := 0; i < 2; i++ {
		if err := repo.CleanupAnalytics(ctx, cutoff, "2026-07-10", 1); err != nil {
			t.Fatal(err)
		}
	}
	for q, w := range map[string]int{`SELECT count(*) FROM player_sessions WHERE ended_at<?`: 0, `SELECT count(*) FROM concurrency_buckets WHERE bucket_start<?`: 0, `SELECT count(*) FROM player_daily_stats WHERE local_date<'2026-07-10'`: 0, `SELECT count(*) FROM player_sessions WHERE ended_at=?`: 1, `SELECT count(*) FROM concurrency_buckets WHERE bucket_start=?`: 1, `SELECT count(*) FROM player_daily_stats WHERE local_date='2026-07-10'`: 1} {
		var n int
		if arg, ok := map[string]any{`SELECT count(*) FROM player_sessions WHERE ended_at<?`: formatTime(cutoff), `SELECT count(*) FROM concurrency_buckets WHERE bucket_start<?`: formatTime(cutoff), `SELECT count(*) FROM player_sessions WHERE ended_at=?`: formatTime(cutoff), `SELECT count(*) FROM concurrency_buckets WHERE bucket_start=?`: formatTime(cutoff)}[q]; ok {
			_ = repo.db.QueryRowContext(ctx, q, arg).Scan(&n)
		} else {
			_ = repo.db.QueryRowContext(ctx, q).Scan(&n)
		}
		if n != w {
			t.Fatalf("%s=%d", q, n)
		}
	}
}

func TestPlayerDailyActivityPreservesCanceledContext(t *testing.T) {
	repo, _ := openTemp(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := repo.PlayerDailyActivity(ctx, "u1", "2026-01-01", "2026-01-02")
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestConcurrencyAcceptsSemanticUTCAndRejectsNonzeroOffset(t *testing.T) {
	repo, _ := openTemp(t)
	zero := time.FixedZone("UTC", 0)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, zero)
	if _, err := repo.Concurrency(t.Context(), start, start.Add(time.Minute)); err != nil {
		t.Fatalf("zero offset: %v", err)
	}
	plusOne := time.FixedZone("plus-one", 3600)
	start = time.Date(2026, 1, 1, 0, 0, 0, 0, plusOne)
	if _, err := repo.Concurrency(t.Context(), start, start.Add(time.Minute)); err == nil {
		t.Fatal("accepted nonzero offset")
	}
}

func TestAnalyticsQueryValidation(t *testing.T) {
	repo, _ := openTemp(t)
	if _, e := repo.Ranking(t.Context(), "bad", "2026-01-02"); e == nil {
		t.Fatal("ranking")
	}
	if _, e := repo.Concurrency(t.Context(), time.Time{}, time.Now()); e == nil {
		t.Fatal("concurrency")
	}
	if _, e := repo.PlayerDailyActivity(t.Context(), "", "2026-01-01", "2026-01-02"); e == nil {
		t.Fatal("activity")
	}
	if e := repo.CleanupAnalytics(t.Context(), time.Now(), "2026-01-01", 0); e == nil {
		t.Fatal("cleanup")
	}
}

func TestOpenAnalyticsPlayersEmpty(t *testing.T) {
	repo, _ := openTemp(t)
	players, at, err := repo.OpenAnalyticsPlayers(t.Context())
	if err != nil || len(players) != 0 || !at.IsZero() {
		t.Fatalf("players=%v at=%v err=%v", players, at, err)
	}
}

func TestRecordAnalyticsObservationPersistsLifecycleAndAggregates(t *testing.T) {
	repo, _ := openTemp(t)
	ctx := t.Context()
	base := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	players := []domain.Player{{UserID: "u1", Name: "One"}, {UserID: "u2", Name: "Two"}}

	if err := repo.RecordAnalyticsObservation(ctx, AnalyticsObservation{
		At: base, LocalDate: "2026-07-11", Players: players, JoinedUserIDs: []string{"u1", "u2"},
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
		At: second, LocalDate: "2026-07-11", Players: players,
		Intervals: []AnalyticsInterval{{Start: second, End: second.Add(10 * time.Second), OnlineUserIDs: []string{"u1"}, LocalDate: "2026-07-11"}},
	}); err != nil {
		t.Fatal(err)
	}
	third := base.Add(40 * time.Second)
	if err := repo.RecordAnalyticsObservation(ctx, AnalyticsObservation{
		At: third, LocalDate: "2026-07-11", Players: players,
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
	if err := repo.RecordAnalyticsObservation(ctx, AnalyticsObservation{At: leaveAt, LocalDate: "2026-07-11", Players: []domain.Player{{UserID: "u2", Name: "Two"}}, LeftUserIDs: []string{"u1"}}); err != nil {
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
	at := time.Date(2026, 7, 11, 16, 30, 0, 0, time.UTC)
	if err := repo.RecordAnalyticsObservation(t.Context(), AnalyticsObservation{At: at, LocalDate: "2026-07-12", Players: []domain.Player{{UserID: "u1"}}, JoinedUserIDs: []string{"u1"}}); err != nil {
		t.Fatal(err)
	}
	var observed int64
	var sessions int
	var first, last string
	if err := repo.db.QueryRow(`SELECT observed_ms, session_count, first_observed_at, last_observed_at FROM player_daily_stats WHERE user_id='u1' AND local_date='2026-07-12'`).Scan(&observed, &sessions, &first, &last); err != nil {
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
				At: tc.start, LocalDate: "2026-07-11", Players: []domain.Player{{UserID: "u1"}}, JoinedUserIDs: []string{"u1"},
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
		At: base, LocalDate: "2026-07-11", Players: []domain.Player{{UserID: "u1"}}, JoinedUserIDs: []string{"u1"},
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

func TestRecordAnalyticsObservationRejectsSubMillisecondInterval(t *testing.T) {
	repo, _ := openTemp(t)
	base := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	err := repo.RecordAnalyticsObservation(t.Context(), AnalyticsObservation{
		At: base, LocalDate: "2026-07-11", Players: []domain.Player{{UserID: "u1"}}, JoinedUserIDs: []string{"u1"},
		Intervals: []AnalyticsInterval{{Start: base, End: base.Add(time.Microsecond), OnlineUserIDs: []string{"u1"}, LocalDate: "2026-07-11"}},
	})
	if err == nil || !strings.Contains(err.Error(), "whole millisecond") {
		t.Fatalf("error=%v", err)
	}
	assertAnalyticsTableCounts(t, repo, 0, 0, 0, 0)
}

func TestRecordAnalyticsObservationRejectsInvalidOnlineUserIDs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		userIDs []string
	}{
		{name: "empty", userIDs: []string{""}},
		{name: "duplicate", userIDs: []string{"u1", "u1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := openTemp(t)
			base := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
			err := repo.RecordAnalyticsObservation(t.Context(), AnalyticsObservation{
				At: base, LocalDate: "2026-07-11", Players: []domain.Player{{UserID: "u1"}}, JoinedUserIDs: []string{"u1"},
				Intervals: []AnalyticsInterval{{Start: base, End: base.Add(time.Second), OnlineUserIDs: tc.userIDs, LocalDate: "2026-07-11"}},
			})
			if err == nil {
				t.Fatal("expected user ID validation error")
			}
			assertAnalyticsTableCounts(t, repo, 0, 0, 0, 0)
		})
	}
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
