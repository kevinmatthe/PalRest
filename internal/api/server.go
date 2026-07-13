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
	AllSnapshots(context.Context) ([]domain.PlayerSnapshot, error)
	OnlineSnapshots(context.Context) ([]domain.PlayerSnapshot, error)
	Snapshot(context.Context, string) (domain.PlayerSnapshot, error)
}

type AnalyticsQueries interface {
	Ranking(context.Context, string, string) ([]store.RankingRow, error)
	Concurrency(context.Context, time.Time, time.Time) ([]store.ConcurrencyBucket, error)
	PlayerDailyActivity(context.Context, string, string, string) ([]store.DailyActivity, error)
	Player(context.Context, string) (domain.Player, error)
}

type AnalyticsOnline interface {
	Current() ([]string, time.Time)
}

type PolicyUpdater interface {
	ApplyPolicyTimezone(func() error, *time.Location) error
}

type Policies interface {
	Policy() config.Policy
	SetPolicy(context.Context, config.Policy) error
}

type Resetter interface {
	ResetUser(string)
}

type AdminStore interface {
	ResetPlayerPolicyState(context.Context, string) error
}

type ObservationQueries interface {
	ReadSensitivePlayerTimeline(context.Context, string, string, time.Time, time.Time, int) (store.SensitivePlayerTimeline, error)
	ReadServerMetrics(context.Context, string, time.Time, time.Time, int) ([]store.ServerMetricSample, error)
	ReadServerDocuments(context.Context, string, string, int) ([]store.ServerDocumentOccurrence, error)
}

type Server struct {
	health          Health
	status          Status
	snapshots       Snapshots
	analytics       AnalyticsQueries
	analyticsOnline AnalyticsOnline
	policies        Policies
	policyUpdater   PolicyUpdater
	resetter        Resetter
	adminStore      AdminStore
	observations    ObservationQueries
	auth            *adminAuth
	config          func() config.Config
	handler         http.Handler
	now             func() time.Time
}

func New(health Health, status Status, snapshots Snapshots, analytics AnalyticsQueries, analyticsOnline AnalyticsOnline, policies Policies, resetter Resetter, adminStore AdminStore, adminUser, adminPass string, configFn func() config.Config, updater ...PolicyUpdater) *Server {
	var auth *adminAuth
	if adminUser != "" || adminPass != "" {
		auth = newAdminAuth(adminUser, adminPass)
	}
	policyUpdater := PolicyUpdater(directPolicyUpdater{analytics: analyticsOnline})
	if len(updater) != 0 {
		policyUpdater = updater[0]
	}
	observations, _ := adminStore.(ObservationQueries)
	server := &Server{health: health, status: status, snapshots: snapshots, analytics: analytics, analyticsOnline: analyticsOnline, policies: policies, policyUpdater: policyUpdater, resetter: resetter, adminStore: adminStore, observations: observations, auth: auth, config: configFn, now: time.Now}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.healthz)
	mux.HandleFunc("GET /readyz", server.readyz)
	mux.HandleFunc("GET /api/v1/admin/session", server.getAdminSession)
	mux.HandleFunc("POST /api/v1/admin/login", server.login)
	mux.HandleFunc("POST /api/v1/admin/logout", server.logout)
	mux.HandleFunc("GET /api/v1/admin/players/{userID}/timeline", server.getAdminPlayerTimeline)
	mux.HandleFunc("GET /api/v1/admin/server/metrics", server.getAdminServerMetrics)
	mux.HandleFunc("GET /api/v1/admin/server/documents", server.getAdminServerDocuments)
	mux.HandleFunc("GET /api/v1/status", server.getStatus)
	mux.HandleFunc("GET /api/v1/players", server.getPlayers)
	mux.HandleFunc("GET /api/v1/players/{userID}", server.getPlayer)
	mux.HandleFunc("GET /api/v1/analytics/summary", server.getAnalyticsSummary)
	mux.HandleFunc("GET /api/v1/analytics/activity", server.getAnalyticsActivity)
	mux.HandleFunc("POST /api/v1/players/{userID}/reset", server.resetPlayer)
	mux.HandleFunc("GET /api/v1/policies", server.getPolicies)
	mux.HandleFunc("PUT /api/v1/policies", server.putPolicies)
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

func (s *Server) getAdminSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":       s.auth.enabled(),
		"authenticated": s.isAdmin(r),
		"passkey":       false,
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.auth.enabled() {
		writeError(w, http.StatusUnauthorized, "admin_disabled", "admin login is not configured")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid login request")
		return
	}
	token, ok := s.auth.login(req.Username, req.Password)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	http.SetCookie(w, sessionCookie(token, int(s.auth.ttl.Seconds())))
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "passkey": false})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(adminSessionCookie); err == nil {
		s.auth.logout(cookie.Value)
	}
	http.SetCookie(w, sessionCookie("", -1))
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
}

type playerDTO struct {
	UserID                string       `json:"user_id"`
	PlayerID              string       `json:"player_id"`
	Name                  string       `json:"name"`
	AccountName           string       `json:"account_name"`
	Online                bool         `json:"online"`
	Enabled               bool         `json:"enabled"`
	Exempt                bool         `json:"exempt"`
	Strategy              string       `json:"strategy"`
	Period                string       `json:"period"`
	UsedMS                int64        `json:"used_ms"`
	RemainingMS           int64        `json:"remaining_ms"`
	CreditAvailableMS     *int64       `json:"credit_available_ms,omitempty"`
	LastCreditRecoveredMS *int64       `json:"last_credit_recovered_ms,omitempty"`
	LimitMS               int64        `json:"limit_ms"`
	PeriodStart           time.Time    `json:"period_start"`
	NextReset             time.Time    `json:"next_reset"`
	WarningBeforeMS       []int64      `json:"warning_before_ms"`
	EnforcementState      string       `json:"enforcement_state,omitempty"`
	Warnings              []warningDTO `json:"warnings"`
}

type warningDTO struct {
	ThresholdMS int64     `json:"threshold_ms"`
	Status      string    `json:"status"`
	Attempts    int       `json:"attempts"`
	NextAttempt time.Time `json:"next_attempt,omitempty"`
}

func (s *Server) getPlayers(w http.ResponseWriter, r *http.Request) {
	snapshots, err := s.snapshots.AllSnapshots(r.Context())
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

func (s *Server) resetPlayer(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	userID := strings.TrimSpace(r.PathValue("userID"))
	if userID == "" {
		writeError(w, http.StatusBadRequest, "invalid_user_id", "user ID is required")
		return
	}
	if err := s.adminStore.ResetPlayerPolicyState(r.Context(), userID); err != nil {
		writeError(w, http.StatusInternalServerError, "reset_failed", "could not reset player policy state")
		return
	}
	s.resetter.ResetUser(userID)
	writeJSON(w, http.StatusOK, map[string]any{"status": "reset", "user_id": userID})
}

func (s *Server) getPolicies(w http.ResponseWriter, _ *http.Request) {
	policy := s.policies.Policy()
	writeJSON(w, http.StatusOK, policyResponse(s.config().Version, policy))
}

func (s *Server) putPolicies(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var payload policyPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_policy", "invalid policy payload")
		return
	}
	policy := payload.toConfig()
	if policy.Overrides == nil {
		policy.Overrides = map[string]config.RuleOverride{}
	}
	location, err := time.LoadLocation(policy.Timezone)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_policy", err.Error())
		return
	}
	if err := s.policyUpdater.ApplyPolicyTimezone(func() error { return s.policies.SetPolicy(r.Context(), policy) }, location); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_policy", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, policyResponse(s.config().Version, policy))
}

type analyticsLocationSetter interface{ SetLocation(*time.Location) error }
type directPolicyUpdater struct{ analytics AnalyticsOnline }

func (u directPolicyUpdater) ApplyPolicyTimezone(update func() error, location *time.Location) error {
	if location == nil {
		return errors.New("analytics location is nil")
	}
	setter, ok := u.analytics.(analyticsLocationSetter)
	if !ok {
		return errors.New("analytics location update is unavailable")
	}
	if err := update(); err != nil {
		return err
	}
	return setter.SetLocation(location)
}

type policyPayload struct {
	Timezone  string                 `json:"timezone"`
	Default   ruleDTO                `json:"default"`
	Overrides map[string]overrideDTO `json:"overrides"`
}

func (p policyPayload) toConfig() config.Policy {
	overrides := make(map[string]config.RuleOverride, len(p.Overrides))
	for userID, override := range p.Overrides {
		overrides[userID] = override.toConfig()
	}
	return config.Policy{Timezone: p.Timezone, Default: p.Default.toConfig(), Overrides: overrides}
}

func policyResponse(version int, policy config.Policy) map[string]any {
	overrides := make(map[string]overrideDTO, len(policy.Overrides))
	for userID, override := range policy.Overrides {
		overrides[userID] = toOverrideDTO(override)
	}
	return map[string]any{
		"version":   version,
		"source":    "database",
		"timezone":  policy.Timezone,
		"default":   toRuleDTO(policy.Default),
		"overrides": overrides,
	}
}

type ruleDTO struct {
	Enabled               bool    `json:"enabled"`
	Strategy              string  `json:"strategy"`
	Period                string  `json:"period"`
	ResetAt               string  `json:"reset_at"`
	ResetWeekday          string  `json:"reset_weekday,omitempty"`
	LimitMS               int64   `json:"limit_ms"`
	CooldownEveryMS       int64   `json:"cooldown_every_ms,omitempty"`
	CooldownRestMS        int64   `json:"cooldown_rest_ms,omitempty"`
	CreditRecoverEveryMS  int64   `json:"credit_recover_every_ms,omitempty"`
	CreditRecoverAmountMS int64   `json:"credit_recover_amount_ms,omitempty"`
	CreditMaxMS           int64   `json:"credit_max_ms,omitempty"`
	WarningBeforeMS       []int64 `json:"warning_before_ms"`
}

func (r ruleDTO) toConfig() config.Rule {
	warnings := make([]config.Duration, len(r.WarningBeforeMS))
	for i, warning := range r.WarningBeforeMS {
		warnings[i] = config.Duration{Duration: time.Duration(warning) * time.Millisecond}
	}
	return config.Rule{
		Enabled:             r.Enabled,
		Strategy:            r.Strategy,
		Period:              r.Period,
		ResetAt:             r.ResetAt,
		ResetWeekday:        r.ResetWeekday,
		Limit:               config.Duration{Duration: time.Duration(r.LimitMS) * time.Millisecond},
		CooldownEvery:       config.Duration{Duration: time.Duration(r.CooldownEveryMS) * time.Millisecond},
		CooldownRest:        config.Duration{Duration: time.Duration(r.CooldownRestMS) * time.Millisecond},
		CreditRecoverEvery:  config.Duration{Duration: time.Duration(r.CreditRecoverEveryMS) * time.Millisecond},
		CreditRecoverAmount: config.Duration{Duration: time.Duration(r.CreditRecoverAmountMS) * time.Millisecond},
		CreditMax:           config.Duration{Duration: time.Duration(r.CreditMaxMS) * time.Millisecond},
		WarningBefore:       warnings,
	}
}

type overrideDTO struct {
	Enabled               *bool   `json:"enabled,omitempty"`
	Strategy              *string `json:"strategy,omitempty"`
	Period                *string `json:"period,omitempty"`
	ResetAt               *string `json:"reset_at,omitempty"`
	ResetWeekday          *string `json:"reset_weekday,omitempty"`
	LimitMS               *int64  `json:"limit_ms,omitempty"`
	CooldownEveryMS       *int64  `json:"cooldown_every_ms,omitempty"`
	CooldownRestMS        *int64  `json:"cooldown_rest_ms,omitempty"`
	CreditRecoverEveryMS  *int64  `json:"credit_recover_every_ms,omitempty"`
	CreditRecoverAmountMS *int64  `json:"credit_recover_amount_ms,omitempty"`
	CreditMaxMS           *int64  `json:"credit_max_ms,omitempty"`
	WarningBeforeMS       []int64 `json:"warning_before_ms,omitempty"`
	Exempt                bool    `json:"exempt"`
}

func (o overrideDTO) toConfig() config.RuleOverride {
	override := config.RuleOverride{
		Enabled:      o.Enabled,
		Strategy:     o.Strategy,
		Period:       o.Period,
		ResetAt:      o.ResetAt,
		ResetWeekday: o.ResetWeekday,
		Exempt:       o.Exempt,
	}
	if o.LimitMS != nil {
		override.Limit = &config.Duration{Duration: time.Duration(*o.LimitMS) * time.Millisecond}
	}
	if o.CooldownEveryMS != nil {
		override.CooldownEvery = &config.Duration{Duration: time.Duration(*o.CooldownEveryMS) * time.Millisecond}
	}
	if o.CooldownRestMS != nil {
		override.CooldownRest = &config.Duration{Duration: time.Duration(*o.CooldownRestMS) * time.Millisecond}
	}
	if o.CreditRecoverEveryMS != nil {
		override.CreditRecoverEvery = &config.Duration{Duration: time.Duration(*o.CreditRecoverEveryMS) * time.Millisecond}
	}
	if o.CreditRecoverAmountMS != nil {
		override.CreditRecoverAmount = &config.Duration{Duration: time.Duration(*o.CreditRecoverAmountMS) * time.Millisecond}
	}
	if o.CreditMaxMS != nil {
		override.CreditMax = &config.Duration{Duration: time.Duration(*o.CreditMaxMS) * time.Millisecond}
	}
	if o.WarningBeforeMS != nil {
		warnings := make([]config.Duration, len(o.WarningBeforeMS))
		for i, warning := range o.WarningBeforeMS {
			warnings[i] = config.Duration{Duration: time.Duration(warning) * time.Millisecond}
		}
		override.WarningBefore = &warnings
	}
	return override
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
	dto := playerDTO{
		UserID: snapshot.Player.UserID, PlayerID: snapshot.Player.PlayerID, Name: snapshot.Player.Name,
		AccountName: snapshot.Player.AccountName, Online: snapshot.Online, Enabled: snapshot.Policy.Enabled,
		Exempt: snapshot.Policy.Exempt, Strategy: snapshot.Policy.Strategy, Period: snapshot.Policy.PeriodType, UsedMS: snapshot.Used.Milliseconds(),
		RemainingMS: snapshot.Remaining.Milliseconds(), LimitMS: snapshot.Policy.Limit.Milliseconds(),
		PeriodStart: snapshot.Period.Start, NextReset: snapshot.Period.End, WarningBeforeMS: warnings,
		EnforcementState: snapshot.Enforcement.Status, Warnings: states,
	}
	if snapshot.Policy.Strategy == "credit" {
		available := snapshot.Remaining.Milliseconds()
		lastRecovered := snapshot.LastCreditRecovered.Milliseconds()
		dto.CreditAvailableMS = &available
		dto.LastCreditRecoveredMS = &lastRecovered
	}
	return dto
}

func toRuleDTO(rule config.Rule) ruleDTO {
	warnings := make([]int64, len(rule.WarningBefore))
	for i, warning := range rule.WarningBefore {
		warnings[i] = warning.Duration.Milliseconds()
	}
	return ruleDTO{
		Enabled: rule.Enabled, Strategy: strategy(rule.Strategy), Period: rule.Period, ResetAt: rule.ResetAt,
		ResetWeekday: rule.ResetWeekday, LimitMS: rule.Limit.Duration.Milliseconds(),
		CooldownEveryMS: rule.CooldownEvery.Duration.Milliseconds(), CooldownRestMS: rule.CooldownRest.Duration.Milliseconds(),
		CreditRecoverEveryMS: rule.CreditRecoverEvery.Duration.Milliseconds(), CreditRecoverAmountMS: rule.CreditRecoverAmount.Duration.Milliseconds(),
		CreditMaxMS: rule.CreditMax.Duration.Milliseconds(), WarningBeforeMS: warnings,
	}
}

func toOverrideDTO(override config.RuleOverride) overrideDTO {
	result := overrideDTO{Enabled: override.Enabled, Strategy: override.Strategy, Period: override.Period, ResetAt: override.ResetAt, ResetWeekday: override.ResetWeekday, Exempt: override.Exempt}
	if override.Limit != nil {
		value := override.Limit.Duration.Milliseconds()
		result.LimitMS = &value
	}
	if override.CooldownEvery != nil {
		value := override.CooldownEvery.Duration.Milliseconds()
		result.CooldownEveryMS = &value
	}
	if override.CooldownRest != nil {
		value := override.CooldownRest.Duration.Milliseconds()
		result.CooldownRestMS = &value
	}
	if override.CreditRecoverEvery != nil {
		value := override.CreditRecoverEvery.Duration.Milliseconds()
		result.CreditRecoverEveryMS = &value
	}
	if override.CreditRecoverAmount != nil {
		value := override.CreditRecoverAmount.Duration.Milliseconds()
		result.CreditRecoverAmountMS = &value
	}
	if override.CreditMax != nil {
		value := override.CreditMax.Duration.Milliseconds()
		result.CreditMaxMS = &value
	}
	if override.WarningBefore != nil {
		result.WarningBeforeMS = make([]int64, len(*override.WarningBefore))
		for i, warning := range *override.WarningBefore {
			result.WarningBeforeMS[i] = warning.Duration.Milliseconds()
		}
	}
	return result
}

func strategy(value string) string {
	if value == "" {
		return "fixed_window"
	}
	return value
}

func ruleLimit(rule config.Rule) time.Duration {
	switch strategy(rule.Strategy) {
	case "cooldown":
		return rule.CooldownEvery.Duration
	case "credit":
		return rule.CreditMax.Duration
	default:
		return rule.Limit.Duration
	}
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

func (s *Server) isAdmin(r *http.Request) bool {
	if !s.auth.enabled() {
		return false
	}
	cookie, err := r.Cookie(adminSessionCookie)
	if err != nil {
		return false
	}
	return s.auth.valid(cookie.Value)
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.isAdmin(r) {
		return true
	}
	writeError(w, http.StatusUnauthorized, "admin_required", "admin login is required")
	return false
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
