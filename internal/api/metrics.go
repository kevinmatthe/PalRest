package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

// ServerMetricSource supplies the latest polled Palworld /metrics sample.
type ServerMetricSource interface {
	LatestServerMetrics(context.Context) (time.Time, domain.ServerMetrics, error)
}

// getPrometheusMetrics exposes live gauges for VictoriaMetrics / Prometheus scrape.
// Format is Prometheus text exposition 0.0.4 (no third-party client dependency).
//
// Labels intentionally stay small: user_id + name only. No IPs, coordinates, or
// other sensitive attributes.
func (s *Server) getPrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	writePromHelp(&b, "palrest_up", "1 when the metrics handler is serving")
	writePromType(&b, "palrest_up", "gauge")
	writePromSample(&b, "palrest_up", nil, 1)

	status := s.status.Status()
	writePromHelp(&b, "palrest_online_players", "Players currently online according to the last successful poll")
	writePromType(&b, "palrest_online_players", "gauge")
	writePromSample(&b, "palrest_online_players", nil, float64(status.OnlineCount))

	writePromHelp(&b, "palrest_poll_success_timestamp_seconds", "Unix timestamp of the last successful player poll")
	writePromType(&b, "palrest_poll_success_timestamp_seconds", "gauge")
	if !status.LastSuccess.IsZero() {
		writePromSample(&b, "palrest_poll_success_timestamp_seconds", nil, float64(status.LastSuccess.UTC().Unix()))
	}

	writePromHelp(&b, "palrest_poll_error", "1 when the last poll attempt failed")
	writePromType(&b, "palrest_poll_error", "gauge")
	pollErr := 0.0
	if status.LastError != "" {
		pollErr = 1
	}
	writePromSample(&b, "palrest_poll_error", nil, pollErr)

	if src, ok := s.adminStore.(ServerMetricSource); ok {
		at, metrics, err := src.LatestServerMetrics(r.Context())
		if err == nil {
			writePromHelp(&b, "palrest_server_metrics_timestamp_seconds", "Unix timestamp of the latest server metrics sample")
			writePromType(&b, "palrest_server_metrics_timestamp_seconds", "gauge")
			writePromSample(&b, "palrest_server_metrics_timestamp_seconds", nil, float64(at.UTC().Unix()))

			writePromHelp(&b, "palrest_server_fps", "Server FPS from Palworld REST /metrics")
			writePromType(&b, "palrest_server_fps", "gauge")
			writePromSample(&b, "palrest_server_fps", nil, float64(metrics.ServerFPS))

			writePromHelp(&b, "palrest_server_frame_time_milliseconds", "Server frame time in milliseconds")
			writePromType(&b, "palrest_server_frame_time_milliseconds", "gauge")
			writePromSample(&b, "palrest_server_frame_time_milliseconds", nil, metrics.ServerFrameTime)

			writePromHelp(&b, "palrest_server_current_players", "currentplayernum from Palworld REST /metrics")
			writePromType(&b, "palrest_server_current_players", "gauge")
			writePromSample(&b, "palrest_server_current_players", nil, float64(metrics.CurrentPlayerNum))

			writePromHelp(&b, "palrest_server_max_players", "maxplayernum from Palworld REST /metrics")
			writePromType(&b, "palrest_server_max_players", "gauge")
			writePromSample(&b, "palrest_server_max_players", nil, float64(metrics.MaxPlayerNum))

			writePromHelp(&b, "palrest_server_uptime_seconds", "Server uptime in seconds from Palworld REST /metrics")
			writePromType(&b, "palrest_server_uptime_seconds", "gauge")
			writePromSample(&b, "palrest_server_uptime_seconds", nil, float64(metrics.UptimeSeconds))

			writePromHelp(&b, "palrest_server_game_days", "In-game day count from Palworld REST /metrics")
			writePromType(&b, "palrest_server_game_days", "gauge")
			writePromSample(&b, "palrest_server_game_days", nil, float64(metrics.Days))

			writePromHelp(&b, "palrest_server_basecamps", "Base camp count from Palworld REST /metrics")
			writePromType(&b, "palrest_server_basecamps", "gauge")
			writePromSample(&b, "palrest_server_basecamps", nil, float64(metrics.BaseCampNum))
		} else if !errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, "query_failed", "server metrics unavailable")
			return
		}
	}

	snapshots, err := s.snapshots.AllSnapshots(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "player state unavailable")
		return
	}
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].Player.Name != snapshots[j].Player.Name {
			return snapshots[i].Player.Name < snapshots[j].Player.Name
		}
		return snapshots[i].Player.UserID < snapshots[j].Player.UserID
	})

	writePromHelp(&b, "palrest_player_online", "1 if the player is online in the last successful poll")
	writePromType(&b, "palrest_player_online", "gauge")
	writePromHelp(&b, "palrest_player_ping_milliseconds", "Player network ping in milliseconds (online players only)")
	writePromType(&b, "palrest_player_ping_milliseconds", "gauge")
	writePromHelp(&b, "palrest_player_level", "Player level (online players only)")
	writePromType(&b, "palrest_player_level", "gauge")
	writePromHelp(&b, "palrest_player_used_seconds", "Policy playtime used in the current period")
	writePromType(&b, "palrest_player_used_seconds", "gauge")
	writePromHelp(&b, "palrest_player_remaining_seconds", "Policy playtime remaining in the current period")
	writePromType(&b, "palrest_player_remaining_seconds", "gauge")
	writePromHelp(&b, "palrest_player_limit_seconds", "Policy playtime limit for the current period")
	writePromType(&b, "palrest_player_limit_seconds", "gauge")

	for _, snap := range snapshots {
		labels := playerLabels(snap.Player)
		online := 0.0
		if snap.Online {
			online = 1
		}
		writePromSample(&b, "palrest_player_online", labels, online)

		if snap.Online {
			if validPing(snap.Player.Ping) {
				writePromSample(&b, "palrest_player_ping_milliseconds", labels, snap.Player.Ping)
			}
			if snap.Player.Level >= 0 {
				writePromSample(&b, "palrest_player_level", labels, float64(snap.Player.Level))
			}
		}

		writePromSample(&b, "palrest_player_used_seconds", labels, snap.Used.Seconds())
		if snap.Remaining >= 0 {
			writePromSample(&b, "palrest_player_remaining_seconds", labels, snap.Remaining.Seconds())
		}
		if snap.Policy.Limit > 0 {
			writePromSample(&b, "palrest_player_limit_seconds", labels, snap.Policy.Limit.Seconds())
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, b.String())
}

func playerLabels(p domain.Player) map[string]string {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = strings.TrimSpace(p.AccountName)
	}
	if name == "" {
		name = p.UserID
	}
	return map[string]string{
		"user_id": p.UserID,
		"name":    name,
	}
}

func validPing(ping float64) bool {
	return !math.IsNaN(ping) && !math.IsInf(ping, 0) && ping >= 0
}

func writePromHelp(w *strings.Builder, name, help string) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
}

func writePromType(w *strings.Builder, name, typ string) {
	fmt.Fprintf(w, "# TYPE %s %s\n", name, typ)
}

func writePromSample(w *strings.Builder, name string, labels map[string]string, value float64) {
	w.WriteString(name)
	if len(labels) > 0 {
		keys := make([]string, 0, len(labels))
		for k := range labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		w.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				w.WriteByte(',')
			}
			w.WriteString(k)
			w.WriteString(`="`)
			w.WriteString(escapePromLabel(labels[k]))
			w.WriteByte('"')
		}
		w.WriteByte('}')
	}
	w.WriteByte(' ')
	w.WriteString(formatPromValue(value))
	w.WriteByte('\n')
}

func escapePromLabel(v string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
	return replacer.Replace(v)
}

func formatPromValue(v float64) string {
	if math.IsNaN(v) {
		return "NaN"
	}
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	if math.IsInf(v, -1) {
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}
