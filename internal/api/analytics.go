package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type analyticsRankingItem struct {
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	ObservedMS int64  `json:"observed_ms"`
	Online     bool   `json:"online"`
}

func (s *Server) analyticsBounds(days int) (*time.Location, time.Time, time.Time, error) {
	loc, err := time.LoadLocation(s.policies.Policy().Timezone)
	if err != nil {
		return nil, time.Time{}, time.Time{}, err
	}
	n := s.now().In(loc)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	return loc, today.AddDate(0, 0, -(days - 1)), today.AddDate(0, 0, 1), nil
}

func (s *Server) getAnalyticsSummary(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "today"
	}
	if period != "today" && period != "week" {
		writeError(w, http.StatusBadRequest, "invalid_request", "period must be today or week")
		return
	}
	_, today, tomorrow, err := s.analyticsBounds(1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "analytics query failed")
		return
	}
	start := today
	if period == "week" {
		days := (int(today.Weekday()) + 6) % 7
		start = today.AddDate(0, 0, -days)
		// The selected week includes the complete local calendar week.
		tomorrow = start.AddDate(0, 0, 7)
	}
	ranking, err := s.analytics.Ranking(r.Context(), start.Format(time.DateOnly), tomorrow.Format(time.DateOnly))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "analytics query failed")
		return
	}
	todayRows := ranking
	if period == "week" {
		todayRows, err = s.analytics.Ranking(r.Context(), today.Format(time.DateOnly), today.AddDate(0, 0, 1).Format(time.DateOnly))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "query_failed", "analytics query failed")
			return
		}
	}
	buckets, err := s.analytics.Concurrency(r.Context(), today.UTC(), today.AddDate(0, 0, 1).UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "analytics query failed")
		return
	}
	ids, asOf := s.analyticsOnline.Current()
	online := make(map[string]bool, len(ids))
	for _, id := range ids {
		online[id] = true
	}
	items := make([]analyticsRankingItem, 0, len(ranking))
	for _, row := range ranking {
		items = append(items, analyticsRankingItem{row.UserID, row.Name, row.Observed.Milliseconds(), online[row.UserID]})
	}
	var observed int64
	for _, row := range todayRows {
		observed += row.Observed.Milliseconds()
	}
	peak := 0
	var peakAt *time.Time
	for _, b := range buckets {
		if b.Max == nil || b.MaxObservedAt == nil {
			continue
		}
		if *b.Max > peak || (*b.Max == peak && (peakAt == nil || b.MaxObservedAt.Before(*peakAt))) {
			peak = *b.Max
			v := b.MaxObservedAt.UTC()
			peakAt = &v
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"online_count": len(ids), "as_of": asOf, "today_observed_ms": observed, "peak_count": peak, "peak_at": peakAt, "active_players": len(todayRows), "ranking_period": period, "ranking": items})
}

type activityPoint struct {
	At       time.Time `json:"at"`
	Average  *float64  `json:"average_count"`
	Max      *int      `json:"max_count"`
	Coverage float64   `json:"coverage"`
}
type dailyPoint struct {
	Date       string `json:"date"`
	ObservedMS int64  `json:"observed_ms"`
}
type activityPlayer struct {
	UserID string       `json:"user_id"`
	Name   string       `json:"name"`
	Daily  []dailyPoint `json:"daily"`
}

func (s *Server) getAnalyticsActivity(w http.ResponseWriter, r *http.Request) {
	rangeName := r.URL.Query().Get("range")
	if rangeName == "" {
		rangeName = "7d"
	}
	days := 7
	if rangeName == "30d" {
		days = 30
	} else if rangeName != "7d" {
		writeError(w, http.StatusBadRequest, "invalid_request", "range must be 7d or 30d")
		return
	}
	loc, start, end, err := s.analyticsBounds(days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "analytics query failed")
		return
	}
	buckets, err := s.analytics.Concurrency(r.Context(), start.UTC(), end.UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "analytics query failed")
		return
	}
	byAt := make(map[int64]store.ConcurrencyBucket, len(buckets))
	for _, b := range buckets {
		byAt[b.Start.UTC().UnixNano()] = b
	}
	series := make([]activityPoint, 0, int(end.UTC().Sub(start.UTC())/(5*time.Minute)))
	for at := start.UTC(); at.Before(end.UTC()); at = at.Add(5 * time.Minute) {
		p := activityPoint{At: at}
		if b, ok := byAt[at.UnixNano()]; ok {
			p.Average, p.Max, p.Coverage = b.Average, b.Max, b.Coverage
		}
		series = append(series, p)
	}
	var player *activityPlayer
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if userID != "" {
		daily, e := s.analytics.PlayerDailyActivity(r.Context(), userID, start.Format(time.DateOnly), end.Format(time.DateOnly))
		if errors.Is(e, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "player not found")
			return
		}
		if e != nil {
			writeError(w, http.StatusInternalServerError, "query_failed", "analytics query failed")
			return
		}
		p, e := s.analytics.Player(r.Context(), userID)
		if errors.Is(e, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "player not found")
			return
		}
		if e != nil {
			writeError(w, http.StatusInternalServerError, "query_failed", "analytics query failed")
			return
		}
		filled := make([]dailyPoint, 0)
		if len(daily) > 0 {
			known := make(map[string]int64, len(daily))
			for _, d := range daily {
				known[d.Date] = d.Observed.Milliseconds()
			}
			for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
				date := d.Format(time.DateOnly)
				filled = append(filled, dailyPoint{date, known[date]})
			}
		}
		player = &activityPlayer{UserID: p.UserID, Name: p.Name, Daily: filled}
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": rangeName, "timezone": loc.String(), "start": start.Format(time.DateOnly), "end": end.Format(time.DateOnly), "concurrency": series, "player": player})
}
