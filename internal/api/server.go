package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/config"
	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type Health interface {
	Ping(context.Context) error
}

type Status interface {
	Status() domain.PollStatus
}

type Snapshots interface {
	OnlineSnapshots(context.Context) ([]domain.PlayerSnapshot, error)
	Snapshot(context.Context, string) (domain.PlayerSnapshot, error)
}

type Server struct {
	health    Health
	status    Status
	snapshots Snapshots
	config    func() config.Config
	handler   http.Handler
}

func New(health Health, status Status, snapshots Snapshots, configFn func() config.Config) *Server {
	server := &Server{health: health, status: status, snapshots: snapshots, config: configFn}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.healthz)
	mux.HandleFunc("GET /readyz", server.readyz)
	mux.HandleFunc("GET /api/v1/status", server.getStatus)
	mux.HandleFunc("GET /api/v1/players", server.getPlayers)
	mux.HandleFunc("GET /api/v1/players/{userID}", server.getPlayer)
	mux.HandleFunc("GET /api/v1/policies", server.getPolicies)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	})
	server.handler = requestMiddleware(mux)
	return server
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) HTTPServer(address string) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	status := s.status.Status()
	if err := s.health.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unhealthy", "sqlite": "unavailable", "last_success": status.LastSuccess})
		return
	}
	state := "healthy"
	if status.LastError != "" || status.ConfigReloadErr != "" {
		state = "degraded"
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": state, "sqlite": "available", "last_success": status.LastSuccess})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	status := s.status.Status()
	if s.config().Version != 1 || status.LastSuccess.IsZero() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	if err := s.health.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) getStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.status.Status())
}

type playerDTO struct {
	UserID           string       `json:"user_id"`
	PlayerID         string       `json:"player_id"`
	Name             string       `json:"name"`
	AccountName      string       `json:"account_name"`
	Online           bool         `json:"online"`
	Enabled          bool         `json:"enabled"`
	Exempt           bool         `json:"exempt"`
	Period           string       `json:"period"`
	UsedMS           int64        `json:"used_ms"`
	RemainingMS      int64        `json:"remaining_ms"`
	LimitMS          int64        `json:"limit_ms"`
	PeriodStart      time.Time    `json:"period_start"`
	NextReset        time.Time    `json:"next_reset"`
	WarningBeforeMS  []int64      `json:"warning_before_ms"`
	EnforcementState string       `json:"enforcement_state,omitempty"`
	Warnings         []warningDTO `json:"warnings"`
}

type warningDTO struct {
	ThresholdMS int64     `json:"threshold_ms"`
	Status      string    `json:"status"`
	Attempts    int       `json:"attempts"`
	NextAttempt time.Time `json:"next_attempt,omitempty"`
}

func (s *Server) getPlayers(w http.ResponseWriter, r *http.Request) {
	snapshots, err := s.snapshots.OnlineSnapshots(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "could not query player state")
		return
	}
	players := make([]playerDTO, 0, len(snapshots))
	for _, snapshot := range snapshots {
		players = append(players, toPlayerDTO(snapshot))
	}
	writeJSON(w, http.StatusOK, map[string]any{"players": players})
}

func (s *Server) getPlayer(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.PathValue("userID"))
	if userID == "" {
		writeError(w, http.StatusBadRequest, "invalid_user_id", "user ID is required")
		return
	}
	snapshot, err := s.snapshots.Snapshot(r.Context(), userID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "player_not_found", "player not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "could not query player state")
		return
	}
	writeJSON(w, http.StatusOK, toPlayerDTO(snapshot))
}

func (s *Server) getPolicies(w http.ResponseWriter, _ *http.Request) {
	cfg := s.config()
	overrides := make(map[string]overrideDTO, len(cfg.Policy.Overrides))
	for userID, override := range cfg.Policy.Overrides {
		overrides[userID] = toOverrideDTO(override)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":   cfg.Version,
		"timezone":  cfg.Policy.Timezone,
		"default":   toRuleDTO(cfg.Policy.Default),
		"overrides": overrides,
	})
}

type ruleDTO struct {
	Enabled         bool    `json:"enabled"`
	Period          string  `json:"period"`
	ResetAt         string  `json:"reset_at"`
	ResetWeekday    string  `json:"reset_weekday,omitempty"`
	LimitMS         int64   `json:"limit_ms"`
	WarningBeforeMS []int64 `json:"warning_before_ms"`
}

type overrideDTO struct {
	Enabled         *bool   `json:"enabled,omitempty"`
	Period          *string `json:"period,omitempty"`
	ResetAt         *string `json:"reset_at,omitempty"`
	ResetWeekday    *string `json:"reset_weekday,omitempty"`
	LimitMS         *int64  `json:"limit_ms,omitempty"`
	WarningBeforeMS []int64 `json:"warning_before_ms,omitempty"`
	Exempt          bool    `json:"exempt"`
}

func toPlayerDTO(snapshot domain.PlayerSnapshot) playerDTO {
	warnings := make([]int64, len(snapshot.Policy.WarningBefore))
	for i, warning := range snapshot.Policy.WarningBefore {
		warnings[i] = warning.Milliseconds()
	}
	states := make([]warningDTO, len(snapshot.Warnings))
	for i, warning := range snapshot.Warnings {
		states[i] = warningDTO{ThresholdMS: warning.Threshold.Milliseconds(), Status: warning.Status, Attempts: warning.Attempts, NextAttempt: warning.NextAttempt}
	}
	return playerDTO{
		UserID: snapshot.Player.UserID, PlayerID: snapshot.Player.PlayerID, Name: snapshot.Player.Name,
		AccountName: snapshot.Player.AccountName, Online: snapshot.Online, Enabled: snapshot.Policy.Enabled,
		Exempt: snapshot.Policy.Exempt, Period: snapshot.Policy.PeriodType, UsedMS: snapshot.Used.Milliseconds(),
		RemainingMS: snapshot.Remaining.Milliseconds(), LimitMS: snapshot.Policy.Limit.Milliseconds(),
		PeriodStart: snapshot.Period.Start, NextReset: snapshot.Period.End, WarningBeforeMS: warnings,
		EnforcementState: snapshot.Enforcement.Status, Warnings: states,
	}
}

func toRuleDTO(rule config.Rule) ruleDTO {
	warnings := make([]int64, len(rule.WarningBefore))
	for i, warning := range rule.WarningBefore {
		warnings[i] = warning.Duration.Milliseconds()
	}
	return ruleDTO{rule.Enabled, rule.Period, rule.ResetAt, rule.ResetWeekday, rule.Limit.Duration.Milliseconds(), warnings}
}

func toOverrideDTO(override config.RuleOverride) overrideDTO {
	result := overrideDTO{Enabled: override.Enabled, Period: override.Period, ResetAt: override.ResetAt, ResetWeekday: override.ResetWeekday, Exempt: override.Exempt}
	if override.Limit != nil {
		value := override.Limit.Duration.Milliseconds()
		result.LimitMS = &value
	}
	if override.WarningBefore != nil {
		result.WarningBeforeMS = make([]int64, len(*override.WarningBefore))
		for i, warning := range *override.WarningBefore {
			result.WarningBeforeMS[i] = warning.Duration.Milliseconds()
		}
	}
	return result
}

func requestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" || len(requestID) > 100 {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil && !errors.Is(err, context.Canceled) {
		return
	}
}

func newRequestID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(buffer)
}
