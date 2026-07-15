package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/behavior"
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
	period := r.URL.Query().Get("ranking")
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
	var asOfJSON *time.Time
	if !asOf.IsZero() {
		value := asOf.UTC()
		asOfJSON = &value
	}
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
	writeJSON(w, http.StatusOK, map[string]any{"online_count": len(ids), "as_of": asOfJSON, "today_observed_ms": observed, "peak_count": peak, "peak_at": peakAt, "active_players": len(todayRows), "ranking_period": period, "ranking": items})
}

type behaviorRankItem struct {
	UserID           string  `json:"user_id"`
	Name             string  `json:"name"`
	SampleCount      int     `json:"sample_count"`
	ObservedActiveMs int64   `json:"observed_active_ms"`
	PathLength       float64 `json:"path_length"`
	Radius           float64 `json:"radius"`
	MeanSpeed        float64 `json:"mean_speed"`
	PeakSpeed        float64 `json:"peak_speed"`
	TravelingShare   float64 `json:"traveling_share"`
	LocalShare       float64 `json:"local_share"`
	StationaryShare  float64 `json:"stationary_share"`
	DominantClass    string  `json:"dominant_class"`
	Online           bool    `json:"online"`
}

// GET /api/v1/analytics/behavior?range=today|7d&sort=traveling|stationary|radius|path|active&limit=25
func (s *Server) getAnalyticsBehavior(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.adminStore.(interface {
		ListUsersWithTrajectories(ctx context.Context, start, end time.Time, limit int) ([]string, error)
		ListTrajectoryPointsAsc(ctx context.Context, userID string, start, end time.Time, limit int) ([]behavior.Point, error)
		PlayerDisplayNames(ctx context.Context, userIDs []string) (map[string]string, error)
	})
	if !ok || s.adminStore == nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "behavior ranking unavailable")
		return
	}
	rangeKey := r.URL.Query().Get("range")
	if rangeKey == "" {
		rangeKey = "today"
	}
	if rangeKey != "today" && rangeKey != "7d" {
		writeError(w, http.StatusBadRequest, "invalid_request", "range must be today or 7d")
		return
	}
	sortKey := r.URL.Query().Get("sort")
	if sortKey == "" {
		sortKey = "traveling"
	}
	switch sortKey {
	case "traveling", "stationary", "radius", "path", "active":
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "sort must be traveling, stationary, radius, path, or active")
		return
	}
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be 1–100")
			return
		}
		limit = n
	}
	days := 1
	if rangeKey == "7d" {
		days = 7
	}
	_, start, end, err := s.analyticsBounds(days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "behavior ranking failed")
		return
	}
	// Scan more candidates than limit so sorting is meaningful.
	userIDs, err := repo.ListUsersWithTrajectories(r.Context(), start.UTC(), end.UTC(), 150)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "behavior ranking failed")
		return
	}
	items := make([]behaviorRankItem, 0, len(userIDs))
	for _, userID := range userIDs {
		points, err := repo.ListTrajectoryPointsAsc(r.Context(), userID, start.UTC(), end.UTC(), behavior.MaxSamples)
		if err != nil || len(points) < 2 {
			continue
		}
		sum := behavior.Summarize(points, start.UTC(), end.UTC())
		if sum.SampleCount < 2 {
			continue
		}
		items = append(items, behaviorRankItem{
			UserID:           userID,
			SampleCount:      sum.SampleCount,
			ObservedActiveMs: sum.ObservedActiveMs,
			PathLength:       sum.PathLength,
			Radius:           sum.Radius,
			MeanSpeed:        sum.MeanSpeed,
			PeakSpeed:        sum.PeakSpeed,
			TravelingShare:   sum.TravelingShare,
			LocalShare:       sum.LocalShare,
			StationaryShare:  sum.StationaryShare,
			DominantClass:    sum.DominantClass,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		switch sortKey {
		case "stationary":
			if a.StationaryShare != b.StationaryShare {
				return a.StationaryShare > b.StationaryShare
			}
		case "radius":
			if a.Radius != b.Radius {
				return a.Radius > b.Radius
			}
		case "path":
			if a.PathLength != b.PathLength {
				return a.PathLength > b.PathLength
			}
		case "active":
			if a.ObservedActiveMs != b.ObservedActiveMs {
				return a.ObservedActiveMs > b.ObservedActiveMs
			}
		default: // traveling
			if a.TravelingShare != b.TravelingShare {
				return a.TravelingShare > b.TravelingShare
			}
		}
		return a.UserID < b.UserID
	})
	if len(items) > limit {
		items = items[:limit]
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.UserID
	}
	names, _ := repo.PlayerDisplayNames(r.Context(), ids)
	onlineIDs, _ := s.analyticsOnline.Current()
	online := make(map[string]bool, len(onlineIDs))
	for _, id := range onlineIDs {
		online[id] = true
	}
	for i := range items {
		if n := names[items[i].UserID]; n != "" {
			items[i].Name = n
		} else {
			items[i].Name = items[i].UserID
		}
		items[i].Online = online[items[i].UserID]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range":      rangeKey,
		"sort":       sortKey,
		"start":      start.UTC(),
		"end":        end.UTC(),
		"timezone":   s.policies.Policy().Timezone,
		"ranking":    items,
		"note":       "Trajectory-derived motion metrics; not policy playtime. Limited to recent samples per player.",
	})
}

func (s *Server) getAnalyticsHealth(w http.ResponseWriter, r *http.Request) {
	rangeName := r.URL.Query().Get("range")
	if rangeName == "" {
		rangeName = "24h"
	}
	hours := 24
	switch rangeName {
	case "6h":
		hours = 6
	case "24h":
		hours = 24
	case "7d":
		hours = 24 * 7
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "range must be 6h, 24h, or 7d")
		return
	}
	end := s.now().UTC()
	start := end.Add(-time.Duration(hours) * time.Hour)
	const limit = 10_000
	fpsSeries, err := s.analytics.ServerFPSSeries(r.Context(), start, end, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "server health query failed")
		return
	}
	fpsOut := make([]map[string]any, 0, len(fpsSeries))
	for _, p := range fpsSeries {
		fpsOut = append(fpsOut, map[string]any{
			"at":         p.At.UTC(),
			"fps":        p.FPS,
			"frame_time": p.FrameTime,
			"players":    p.Players,
		})
	}
	// Latest snapshot cards (server-level)
	var latestFPS any
	var latestPlayers any
	if n := len(fpsSeries); n > 0 {
		latestFPS = fpsSeries[n-1].FPS
		latestPlayers = fpsSeries[n-1].Players
	}

	// Per-player latest ping ranking (ops: who is lagging), no IP.
	latestPings, err := s.analytics.LatestPlayerPings(r.Context(), start, end, 25)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "player latency ranking query failed")
		return
	}
	rankOut := make([]map[string]any, 0, len(latestPings))
	for _, p := range latestPings {
		rankOut = append(rankOut, map[string]any{
			"user_id": p.UserID,
			"name":    p.Name,
			"at":      p.At.UTC(),
			"ping":    p.Ping,
		})
	}

	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	var playerLatency []map[string]any
	var playerName string
	if userID != "" {
		series, err := s.analytics.PlayerPingSeries(r.Context(), userID, start, end, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "query_failed", "player latency series query failed")
			return
		}
		playerLatency = make([]map[string]any, 0, len(series))
		for _, p := range series {
			playerLatency = append(playerLatency, map[string]any{
				"at":   p.At.UTC(),
				"ping": p.Ping,
			})
		}
		if player, err := s.analytics.Player(r.Context(), userID); err == nil {
			playerName = player.Name
			if playerName == "" {
				playerName = player.UserID
			}
		} else if err != store.ErrNotFound {
			writeError(w, http.StatusInternalServerError, "query_failed", "player lookup failed")
			return
		} else {
			playerName = userID
		}
	} else {
		playerLatency = []map[string]any{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"range":              rangeName,
		"start":              start,
		"end":                end,
		"latest_fps":         latestFPS,
		"latest_players":     latestPlayers,
		"fps":                fpsOut,
		"player_ping_rank":   rankOut,
		"user_id":            userID,
		"player_name":        playerName,
		"player_latency":     playerLatency,
		"note":               "FPS from server_metric_samples; latency is per-player poll samples (no IP).",
	})
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
	includeConcurrency := true
	switch value := r.URL.Query().Get("include_concurrency"); value {
	case "", "true":
	case "false":
		includeConcurrency = false
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "include_concurrency must be true or false")
		return
	}
	loc, start, end, err := s.analyticsBounds(days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "analytics query failed")
		return
	}
	series := make([]activityPoint, 0)
	if includeConcurrency {
		buckets, queryErr := s.analytics.Concurrency(r.Context(), start.UTC(), end.UTC())
		if queryErr != nil {
			writeError(w, http.StatusInternalServerError, "query_failed", "analytics query failed")
			return
		}
		byAt := make(map[int64]store.ConcurrencyBucket, len(buckets))
		for _, b := range buckets {
			byAt[b.Start.UTC().UnixNano()] = b
		}
		series = make([]activityPoint, 0, int(end.UTC().Sub(start.UTC())/(5*time.Minute)))
		for at := start.UTC(); at.Before(end.UTC()); at = at.Add(5 * time.Minute) {
			p := activityPoint{At: at}
			if b, ok := byAt[at.UnixNano()]; ok {
				p.Average, p.Max, p.Coverage = b.Average, b.Max, b.Coverage
			}
			series = append(series, p)
		}
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
