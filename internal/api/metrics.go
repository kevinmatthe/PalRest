package api

import (
	"context"
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

// MetricsExportSource supplies durable server/save facts for /metrics.
type MetricsExportSource interface {
	LoadMetricsExport(context.Context) (store.MetricsExport, error)
}

// getPrometheusMetrics exposes live + durable gauges for VictoriaMetrics scrape.
// Prometheus text exposition 0.0.4. Labels stay small; no IPs or raw coordinates.
func (s *Server) getPrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	now := s.now().UTC()
	var b strings.Builder

	// --- process ---
	metricMeta(&b, "palrest_up", "gauge", "1 when the metrics handler is serving")
	writePromSample(&b, "palrest_up", nil, 1)

	status := s.status.Status()
	metricMeta(&b, "palrest_online_players", "gauge", "Players online per last successful Guard poll")
	writePromSample(&b, "palrest_online_players", nil, float64(status.OnlineCount))

	metricMeta(&b, "palrest_poll_success_timestamp_seconds", "gauge", "Unix time of last successful player poll")
	if !status.LastSuccess.IsZero() {
		writePromSample(&b, "palrest_poll_success_timestamp_seconds", nil, float64(status.LastSuccess.UTC().Unix()))
		metricMeta(&b, "palrest_poll_success_age_seconds", "gauge", "Seconds since last successful player poll")
		writePromSample(&b, "palrest_poll_success_age_seconds", nil, math.Max(0, now.Sub(status.LastSuccess.UTC()).Seconds()))
	}
	metricMeta(&b, "palrest_poll_attempt_timestamp_seconds", "gauge", "Unix time of last poll attempt")
	if !status.LastAttempt.IsZero() {
		writePromSample(&b, "palrest_poll_attempt_timestamp_seconds", nil, float64(status.LastAttempt.UTC().Unix()))
	}
	metricMeta(&b, "palrest_poll_error", "gauge", "1 when the last poll attempt failed")
	pollErr := 0.0
	if status.LastError != "" {
		pollErr = 1
	}
	writePromSample(&b, "palrest_poll_error", nil, pollErr)
	metricMeta(&b, "palrest_config_version", "gauge", "Loaded config version counter")
	writePromSample(&b, "palrest_config_version", nil, float64(status.ConfigVersion))
	metricMeta(&b, "palrest_config_reload_error", "gauge", "1 when config hot-reload last failed")
	cfgErr := 0.0
	if status.ConfigReloadErr != "" {
		cfgErr = 1
	}
	writePromSample(&b, "palrest_config_reload_error", nil, cfgErr)
	if !status.StartedAt.IsZero() {
		metricMeta(&b, "palrest_process_start_timestamp_seconds", "gauge", "Unix time when the process started")
		writePromSample(&b, "palrest_process_start_timestamp_seconds", nil, float64(status.StartedAt.UTC().Unix()))
		metricMeta(&b, "palrest_process_uptime_seconds", "gauge", "Guard process uptime seconds")
		writePromSample(&b, "palrest_process_uptime_seconds", nil, math.Max(0, now.Sub(status.StartedAt.UTC()).Seconds()))
	}

	// --- durable store export ---
	if src, ok := s.adminStore.(MetricsExportSource); ok {
		exp, err := src.LoadMetricsExport(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "query_failed", "metrics export failed")
			return
		}
		writeServerExport(&b, now, exp)
	} else if src, ok := s.adminStore.(ServerMetricSource); ok {
		// Backward-compatible fallback for tests that only implement metrics.
		at, metrics, err := src.LatestServerMetrics(r.Context())
		if err == nil {
			writeLegacyServerMetrics(&b, now, at, metrics)
		}
	}

	// --- live players ---
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
	writePlayerLiveMetrics(&b, snapshots)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, b.String())
}

// ServerMetricSource is the minimal interface used by older tests.
type ServerMetricSource interface {
	LatestServerMetrics(context.Context) (time.Time, domain.ServerMetrics, error)
}

func writeServerExport(b *strings.Builder, now time.Time, exp store.MetricsExport) {
	if exp.Server != nil {
		writeLegacyServerMetrics(b, now, exp.ServerAt, *exp.Server)
	}

	metricMeta(b, "palrest_server_runtime_epoch", "gauge", "Monotonic server runtime epoch (increments on detected restart)")
	writePromSample(b, "palrest_server_runtime_epoch", nil, float64(exp.Runtime.Epoch))
	if !exp.Runtime.RestartedAt.IsZero() {
		metricMeta(b, "palrest_server_last_restart_timestamp_seconds", "gauge", "Unix time of last detected server restart")
		writePromSample(b, "palrest_server_last_restart_timestamp_seconds", nil, float64(exp.Runtime.RestartedAt.UTC().Unix()))
	}
	metricMeta(b, "palrest_server_restarts_total", "counter", "Count of server_restarted activity events")
	writePromSample(b, "palrest_server_restarts_total", nil, float64(exp.RestartEvents))

	if exp.Info != nil {
		metricMeta(b, "palrest_server_info", "gauge", "Server identity from REST /info (labels only)")
		writePromSample(b, "palrest_server_info", map[string]string{
			"version":     exp.Info.Version,
			"server_name": exp.Info.ServerName,
			"world_guid":  exp.Info.WorldGUID,
		}, 1)
		if !exp.InfoAt.IsZero() {
			metricMeta(b, "palrest_server_info_timestamp_seconds", "gauge", "Unix time of latest /info sample")
			writePromSample(b, "palrest_server_info_timestamp_seconds", nil, float64(exp.InfoAt.UTC().Unix()))
		}
	}

	if len(exp.SettingsScalar) > 0 {
		metricMeta(b, "palrest_server_setting", "gauge", "Selected numeric/bool WorldSettings fields from REST /settings")
		keys := make([]string, 0, len(exp.SettingsScalar))
		for k := range exp.SettingsScalar {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			writePromSample(b, "palrest_server_setting", map[string]string{"key": k}, exp.SettingsScalar[k])
		}
		if !exp.SettingsAt.IsZero() {
			metricMeta(b, "palrest_server_settings_timestamp_seconds", "gauge", "Unix time of latest /settings sample")
			writePromSample(b, "palrest_server_settings_timestamp_seconds", nil, float64(exp.SettingsAt.UTC().Unix()))
		}
	}

	if exp.Save != nil {
		writeSaveMetrics(b, now, exp.Save)
	}
}

func writeLegacyServerMetrics(b *strings.Builder, now, at time.Time, metrics domain.ServerMetrics) {
	if !at.IsZero() {
		metricMeta(b, "palrest_server_metrics_timestamp_seconds", "gauge", "Unix time of latest REST /metrics sample")
		writePromSample(b, "palrest_server_metrics_timestamp_seconds", nil, float64(at.UTC().Unix()))
		metricMeta(b, "palrest_server_metrics_age_seconds", "gauge", "Seconds since latest REST /metrics sample")
		writePromSample(b, "palrest_server_metrics_age_seconds", nil, math.Max(0, now.Sub(at.UTC()).Seconds()))
	}
	metricMeta(b, "palrest_server_fps", "gauge", "Server FPS from Palworld REST /metrics")
	writePromSample(b, "palrest_server_fps", nil, float64(metrics.ServerFPS))
	metricMeta(b, "palrest_server_frame_time_milliseconds", "gauge", "Server frame time in milliseconds")
	writePromSample(b, "palrest_server_frame_time_milliseconds", nil, metrics.ServerFrameTime)
	metricMeta(b, "palrest_server_current_players", "gauge", "currentplayernum from Palworld REST /metrics")
	writePromSample(b, "palrest_server_current_players", nil, float64(metrics.CurrentPlayerNum))
	metricMeta(b, "palrest_server_max_players", "gauge", "maxplayernum from Palworld REST /metrics")
	writePromSample(b, "palrest_server_max_players", nil, float64(metrics.MaxPlayerNum))
	metricMeta(b, "palrest_server_uptime_seconds", "gauge", "Game server uptime seconds from REST /metrics")
	writePromSample(b, "palrest_server_uptime_seconds", nil, float64(metrics.UptimeSeconds))
	metricMeta(b, "palrest_server_game_days", "gauge", "In-game day count from REST /metrics")
	writePromSample(b, "palrest_server_game_days", nil, float64(metrics.Days))
	metricMeta(b, "palrest_server_basecamps", "gauge", "Base camp count from REST /metrics")
	writePromSample(b, "palrest_server_basecamps", nil, float64(metrics.BaseCampNum))
	if metrics.MaxPlayerNum > 0 {
		metricMeta(b, "palrest_server_fill_ratio", "gauge", "currentplayernum / maxplayernum")
		writePromSample(b, "palrest_server_fill_ratio", nil, float64(metrics.CurrentPlayerNum)/float64(metrics.MaxPlayerNum))
	}
}

func writePlayerLiveMetrics(b *strings.Builder, snapshots []domain.PlayerSnapshot) {
	metricMeta(b, "palrest_player_online", "gauge", "1 if player is online in last successful poll")
	metricMeta(b, "palrest_player_ping_milliseconds", "gauge", "Player ping ms (online only)")
	metricMeta(b, "palrest_player_level", "gauge", "Player level (online only, REST)")
	metricMeta(b, "palrest_player_used_seconds", "gauge", "Policy playtime used in current period")
	metricMeta(b, "palrest_player_remaining_seconds", "gauge", "Policy playtime remaining in current period")
	metricMeta(b, "palrest_player_limit_seconds", "gauge", "Policy playtime limit for current period")
	metricMeta(b, "palrest_player_policy_enabled", "gauge", "1 if playtime policy is enabled for player")
	metricMeta(b, "palrest_player_policy_exempt", "gauge", "1 if player is exempt from playtime policy")
	metricMeta(b, "palrest_player_warning_active", "gauge", "Count of warning thresholds currently active/pending")
	metricMeta(b, "palrest_player_enforcement", "gauge", "1 when enforcement status matches label")
	metricMeta(b, "palrest_player_last_online_timestamp_seconds", "gauge", "Last known online timestamp for player")
	metricMeta(b, "palrest_player_distance_from_origin", "gauge", "sqrt(x^2+y^2) world distance (online only)")
	metricMeta(b, "palrest_player_location_x", "gauge", "World X coordinate from REST poll (online only; game units, not lat/lon)")
	metricMeta(b, "palrest_player_location_y", "gauge", "World Y coordinate from REST poll (online only; game units, not lat/lon)")

	var online, limited int
	for _, snap := range snapshots {
		labels := playerLabels(snap.Player)
		if snap.Online {
			online++
		}
		writePromSample(b, "palrest_player_online", labels, bool01(snap.Online))

		if snap.Online {
			if validPing(snap.Player.Ping) {
				writePromSample(b, "palrest_player_ping_milliseconds", labels, snap.Player.Ping)
			}
			if snap.Player.Level >= 0 {
				writePromSample(b, "palrest_player_level", labels, float64(snap.Player.Level))
			}
			if finiteWorldCoord(snap.Player.LocationX, snap.Player.LocationY) {
				writePromSample(b, "palrest_player_location_x", labels, snap.Player.LocationX)
				writePromSample(b, "palrest_player_location_y", labels, snap.Player.LocationY)
				writePromSample(b, "palrest_player_distance_from_origin", labels, math.Hypot(snap.Player.LocationX, snap.Player.LocationY))
			}
		}

		writePromSample(b, "palrest_player_used_seconds", labels, snap.Used.Seconds())
		if snap.Remaining >= 0 {
			writePromSample(b, "palrest_player_remaining_seconds", labels, snap.Remaining.Seconds())
		}
		if snap.Policy.Limit > 0 {
			limited++
			writePromSample(b, "palrest_player_limit_seconds", labels, snap.Policy.Limit.Seconds())
		}
		writePromSample(b, "palrest_player_policy_enabled", labels, bool01(snap.Policy.Enabled))
		writePromSample(b, "palrest_player_policy_exempt", labels, bool01(snap.Policy.Exempt))

		warnActive := 0
		for _, w := range snap.Warnings {
			if w.Status != "" && w.Status != "clear" && w.Status != "none" {
				warnActive++
			}
		}
		writePromSample(b, "palrest_player_warning_active", labels, float64(warnActive))

		status := snap.Enforcement.Status
		if status == "" {
			status = "none"
		}
		writePromSample(b, "palrest_player_enforcement", map[string]string{
			"user_id": labels["user_id"],
			"name":    labels["name"],
			"status":  status,
		}, 1)

		if !snap.Player.LastOnline.IsZero() {
			writePromSample(b, "palrest_player_last_online_timestamp_seconds", labels, float64(snap.Player.LastOnline.UTC().Unix()))
		}
	}
	metricMeta(b, "palrest_known_players", "gauge", "Known players returned by Guard snapshot set")
	writePromSample(b, "palrest_known_players", nil, float64(len(snapshots)))
	metricMeta(b, "palrest_policy_limited_players", "gauge", "Players with a positive playtime limit")
	writePromSample(b, "palrest_policy_limited_players", nil, float64(limited))
	_ = online
}

func writeSaveMetrics(b *strings.Builder, now time.Time, save *store.SaveMetricsExport) {
	metricMeta(b, "palrest_save_import_present", "gauge", "1 when at least one save import exists")
	writePromSample(b, "palrest_save_import_present", nil, 1)
	if !save.ImportedAt.IsZero() {
		metricMeta(b, "palrest_save_import_timestamp_seconds", "gauge", "Unix time of latest save import")
		writePromSample(b, "palrest_save_import_timestamp_seconds", nil, float64(save.ImportedAt.Unix()))
		metricMeta(b, "palrest_save_import_age_seconds", "gauge", "Seconds since latest save import")
		writePromSample(b, "palrest_save_import_age_seconds", nil, math.Max(0, now.Sub(save.ImportedAt).Seconds()))
	}
	metricMeta(b, "palrest_save_level_bytes", "gauge", "Level.sav size of latest import")
	writePromSample(b, "palrest_save_level_bytes", nil, float64(save.LevelSAVSize))
	metricMeta(b, "palrest_save_player_count", "gauge", "Players in latest save import")
	writePromSample(b, "palrest_save_player_count", nil, float64(save.PlayerCount))
	metricMeta(b, "palrest_save_guild_count", "gauge", "Guilds in latest save import")
	writePromSample(b, "palrest_save_guild_count", nil, float64(save.GuildCount))
	metricMeta(b, "palrest_save_basecamp_count", "gauge", "Base camps in latest save import")
	writePromSample(b, "palrest_save_basecamp_count", nil, float64(save.BaseCampCount))
	metricMeta(b, "palrest_save_basecamp_area_sum", "gauge", "Sum of base camp area from latest save import")
	writePromSample(b, "palrest_save_basecamp_area_sum", nil, save.BaseCampAreaSum)

	metricMeta(b, "palrest_save_player_level", "gauge", "Character level from latest save import")
	metricMeta(b, "palrest_save_player_exp", "gauge", "Character exp from latest save import")
	metricMeta(b, "palrest_save_player_hp", "gauge", "Character HP from latest save import")
	metricMeta(b, "palrest_save_player_shield_hp", "gauge", "Character shield HP from latest save import")
	metricMeta(b, "palrest_save_player_full_stomach", "gauge", "Character full_stomach from latest save import")
	metricMeta(b, "palrest_save_player_last_online_timestamp_seconds", "gauge", "Save last-online timestamp")

	for _, p := range save.Players {
		labels := savePlayerLabels(p)
		writePromSample(b, "palrest_save_player_level", labels, float64(p.Level))
		writePromSample(b, "palrest_save_player_exp", labels, float64(p.Exp))
		writePromSample(b, "palrest_save_player_hp", labels, float64(p.HP))
		writePromSample(b, "palrest_save_player_shield_hp", labels, float64(p.ShieldHP))
		writePromSample(b, "palrest_save_player_full_stomach", labels, p.FullStomach)
		if !p.LastOnline.IsZero() {
			writePromSample(b, "palrest_save_player_last_online_timestamp_seconds", labels, float64(p.LastOnline.Unix()))
		}
	}

	metricMeta(b, "palrest_save_guild_basecamp_level", "gauge", "Guild base camp level from latest save import")
	metricMeta(b, "palrest_save_guild_members", "gauge", "Guild member count from latest save import")
	metricMeta(b, "palrest_save_guild_basecamps", "gauge", "Guild base camp count from latest save import")
	metricMeta(b, "palrest_save_guild_basecamp_area", "gauge", "Sum of guild base camp area from latest save import")
	for _, g := range save.Guilds {
		labels := map[string]string{"guild_id": g.GuildID, "guild": g.Name}
		writePromSample(b, "palrest_save_guild_basecamp_level", labels, float64(g.BaseCampLevel))
		writePromSample(b, "palrest_save_guild_members", labels, float64(g.MemberCount))
		writePromSample(b, "palrest_save_guild_basecamps", labels, float64(g.BaseCampCount))
		writePromSample(b, "palrest_save_guild_basecamp_area", labels, g.BaseCampArea)
	}
}

func savePlayerLabels(p store.SavePlayerMetric) map[string]string {
	labels := map[string]string{
		"name":     p.Name,
		"save_uid": p.SaveUID,
	}
	if p.UserID != "" {
		labels["user_id"] = p.UserID
	}
	return labels
}

func playerLabels(p domain.Player) map[string]string {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = strings.TrimSpace(p.AccountName)
	}
	if name == "" {
		name = p.UserID
	}
	return map[string]string{"user_id": p.UserID, "name": name}
}

func bool01(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func validPing(ping float64) bool {
	return !math.IsNaN(ping) && !math.IsInf(ping, 0) && ping >= 0
}

func metricMeta(w *strings.Builder, name, typ, help string) {
	// HELP/TYPE once per metric family — callers may invoke repeatedly; Prometheus
	// allows duplicate HELP/TYPE in practice for simple exporters, but we only call
	// once per family at the start of each section.
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
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
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(v)
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
