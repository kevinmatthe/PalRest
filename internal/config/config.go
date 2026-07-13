package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"go.yaml.in/yaml/v3"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	value, err := parseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", node.Value, err)
	}
	d.Duration = value
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		parsed, parseErr := parseDuration(text)
		if parseErr != nil {
			return fmt.Errorf("invalid duration %q: %w", text, parseErr)
		}
		d.Duration = parsed
		return nil
	}
	var ms int64
	if err := json.Unmarshal(data, &ms); err != nil {
		return fmt.Errorf("duration must be a Go duration string or milliseconds")
	}
	if ms > int64(time.Duration(1<<63-1))/int64(time.Millisecond) || ms < int64(time.Duration(-1<<63))/int64(time.Millisecond) {
		return fmt.Errorf("duration milliseconds %d overflow time.Duration", ms)
	}
	d.Duration = time.Duration(ms) * time.Millisecond
	return nil
}

func parseDuration(value string) (time.Duration, error) {
	if strings.HasSuffix(value, "d") {
		text := strings.TrimSuffix(value, "d")
		if !signedDecimalInteger(text) {
			return 0, fmt.Errorf("time: invalid duration %q", value)
		}
		days, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("time: invalid duration %q", value)
		}
		const day = 24 * time.Hour
		if days > int64(time.Duration(1<<63-1))/int64(day) || days < int64(time.Duration(-1<<63))/int64(day) {
			return 0, fmt.Errorf("time: invalid duration %q", value)
		}
		return time.Duration(days) * day, nil
	}
	return time.ParseDuration(value)
}

func signedDecimalInteger(value string) bool {
	if value == "" {
		return false
	}
	start := 0
	if value[0] == '+' || value[0] == '-' {
		start = 1
	}
	if start == len(value) {
		return false
	}
	for i := start; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

type Config struct {
	Version     int         `yaml:"version" json:"version"`
	Server      Server      `yaml:"server" json:"server"`
	Policy      Policy      `yaml:"policy" json:"policy"`
	Enforcement Enforcement `yaml:"enforcement" json:"enforcement"`
	HTTP        HTTP        `yaml:"http" json:"http"`
	Storage     Storage     `yaml:"storage" json:"storage"`
	Observation Observation `yaml:"observation" json:"observation"`
	password    string
	adminUser   string
	adminPass   string
}

type Server struct {
	BaseURL           string   `yaml:"base_url" json:"base_url"`
	PasswordEnv       string   `yaml:"password_env" json:"password_env"`
	PollInterval      Duration `yaml:"poll_interval" json:"poll_interval"`
	RequestTimeout    Duration `yaml:"request_timeout" json:"request_timeout"`
	MaxObservationGap Duration `yaml:"max_observation_gap" json:"max_observation_gap"`
}

type Policy struct {
	Timezone  string                  `yaml:"timezone" json:"timezone"`
	Default   Rule                    `yaml:"default" json:"default"`
	Overrides map[string]RuleOverride `yaml:"overrides" json:"overrides"`
}

// UnmarshalYAML treats the configuration-file policy as bootstrap defaults.
// Stored policy documents bypass this method in ParsePolicy so they can retain
// per-player overrides.
func (p *Policy) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("policy must be a mapping")
	}
	for i := 0; i < len(node.Content); i += 2 {
		name := node.Content[i].Value
		if name != "timezone" && name != "default" {
			return fmt.Errorf("field %s not found in type config policy defaults", name)
		}
		if name == "default" {
			if err := validateBootstrapRuleFields(node.Content[i+1]); err != nil {
				return err
			}
		}
	}

	value := DefaultPolicy()
	type ruleYAML Rule
	raw := struct {
		Timezone string   `yaml:"timezone"`
		Default  ruleYAML `yaml:"default"`
	}{Timezone: value.Timezone, Default: ruleYAML(value.Default)}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	value.Timezone = raw.Timezone
	value.Default = Rule(raw.Default)
	*p = value
	return nil
}

func validateBootstrapRuleFields(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("policy.default must be a mapping")
	}
	allowed := map[string]bool{
		"enabled": true, "strategy": true, "period": true, "reset_at": true, "reset_weekday": true,
		"limit": true, "cooldown_every": true, "cooldown_rest": true, "credit_recover_every": true,
		"credit_recover_amount": true, "credit_max": true, "warning_before": true,
	}
	for i := 0; i < len(node.Content); i += 2 {
		if name := node.Content[i].Value; !allowed[name] {
			return fmt.Errorf("field %s not found in type config policy default", name)
		}
	}
	return nil
}

type Rule struct {
	Enabled             bool       `yaml:"enabled" json:"enabled"`
	Strategy            string     `yaml:"strategy,omitempty" json:"strategy,omitempty"`
	Period              string     `yaml:"period" json:"period"`
	ResetAt             string     `yaml:"reset_at" json:"reset_at"`
	ResetWeekday        string     `yaml:"reset_weekday,omitempty" json:"reset_weekday,omitempty"`
	Limit               Duration   `yaml:"limit" json:"limit"`
	CooldownEvery       Duration   `yaml:"cooldown_every,omitempty" json:"cooldown_every,omitempty"`
	CooldownRest        Duration   `yaml:"cooldown_rest,omitempty" json:"cooldown_rest,omitempty"`
	CreditRecoverEvery  Duration   `yaml:"credit_recover_every,omitempty" json:"credit_recover_every,omitempty"`
	CreditRecoverAmount Duration   `yaml:"credit_recover_amount,omitempty" json:"credit_recover_amount,omitempty"`
	CreditMax           Duration   `yaml:"credit_max,omitempty" json:"credit_max,omitempty"`
	WarningBefore       []Duration `yaml:"warning_before" json:"warning_before"`
}

type RuleOverride struct {
	Enabled             *bool       `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Strategy            *string     `yaml:"strategy,omitempty" json:"strategy,omitempty"`
	Period              *string     `yaml:"period,omitempty" json:"period,omitempty"`
	ResetAt             *string     `yaml:"reset_at,omitempty" json:"reset_at,omitempty"`
	ResetWeekday        *string     `yaml:"reset_weekday,omitempty" json:"reset_weekday,omitempty"`
	Limit               *Duration   `yaml:"limit,omitempty" json:"limit,omitempty"`
	CooldownEvery       *Duration   `yaml:"cooldown_every,omitempty" json:"cooldown_every,omitempty"`
	CooldownRest        *Duration   `yaml:"cooldown_rest,omitempty" json:"cooldown_rest,omitempty"`
	CreditRecoverEvery  *Duration   `yaml:"credit_recover_every,omitempty" json:"credit_recover_every,omitempty"`
	CreditRecoverAmount *Duration   `yaml:"credit_recover_amount,omitempty" json:"credit_recover_amount,omitempty"`
	CreditMax           *Duration   `yaml:"credit_max,omitempty" json:"credit_max,omitempty"`
	WarningBefore       *[]Duration `yaml:"warning_before,omitempty" json:"warning_before,omitempty"`
	Exempt              bool        `yaml:"exempt,omitempty" json:"exempt,omitempty"`
}

type Enforcement struct {
	KickMessage      string   `yaml:"kick_message" json:"kick_message"`
	AnnounceMessage  string   `yaml:"announce_message" json:"announce_message"`
	KickRetryInitial Duration `yaml:"kick_retry_initial" json:"kick_retry_initial"`
	KickRetryMax     Duration `yaml:"kick_retry_max" json:"kick_retry_max"`
}

type HTTP struct {
	Listen           string `yaml:"listen" json:"listen"`
	AdminUsernameEnv string `yaml:"admin_username_env,omitempty" json:"admin_username_env,omitempty"`
	AdminPasswordEnv string `yaml:"admin_password_env,omitempty" json:"admin_password_env,omitempty"`
}

type Storage struct {
	Path string `yaml:"path" json:"path"`
}

type Observation struct {
	ServerDocumentInterval        Duration `yaml:"server_document_interval" json:"server_document_interval"`
	TrajectoryMinDistance         float64  `yaml:"trajectory_min_distance" json:"trajectory_min_distance"`
	TrajectoryPingChangeThreshold float64  `yaml:"trajectory_ping_change_threshold" json:"trajectory_ping_change_threshold"`
	TrajectoryMaxInterval         Duration `yaml:"trajectory_max_interval" json:"trajectory_max_interval"`
	RawRetention                  Duration `yaml:"raw_retention" json:"raw_retention"`
}

func (c Config) Password() string { return c.password }

func (c Config) AdminEnabled() bool { return c.adminUser != "" && c.adminPass != "" }

func (c Config) AdminCredentials() (string, string) { return c.adminUser, c.adminPass }

func Parse(data []byte, lookup func(string) (string, bool)) (Config, error) {
	cfg := defaults()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("decode config: multiple YAML documents are not allowed")
		}
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.validate(lookup); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func ParsePolicy(data []byte) (Policy, error) {
	type storedPolicy Policy
	policy := storedPolicy{Overrides: map[string]RuleOverride{}}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&policy); err != nil {
		return Policy{}, fmt.Errorf("decode policy: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return Policy{}, fmt.Errorf("decode policy: multiple YAML documents are not allowed")
		}
		return Policy{}, fmt.Errorf("decode policy: %w", err)
	}
	result := Policy(policy)
	if err := ValidatePolicy(result); err != nil {
		return Policy{}, err
	}
	return result, nil
}

func MarshalPolicy(policy Policy) ([]byte, error) {
	return yaml.Marshal(policy)
}

func defaults() Config {
	return Config{
		Version: 1,
		Server: Server{
			PollInterval:      Duration{30 * time.Second},
			RequestTimeout:    Duration{5 * time.Second},
			MaxObservationGap: Duration{75 * time.Second},
		},
		Policy: DefaultPolicy(),
		Enforcement: Enforcement{
			KickRetryInitial: Duration{15 * time.Second},
			KickRetryMax:     Duration{5 * time.Minute},
		},
		HTTP:    HTTP{Listen: "0.0.0.0:8080"},
		Storage: Storage{Path: "/data/guard.db"},
		Observation: Observation{
			ServerDocumentInterval:        Duration{5 * time.Minute},
			TrajectoryMinDistance:         100,
			TrajectoryPingChangeThreshold: 10,
			TrajectoryMaxInterval:         Duration{5 * time.Minute},
			RawRetention:                  Duration{90 * 24 * time.Hour},
		},
	}
}

func DefaultPolicy() Policy {
	return Policy{
		Timezone: "Asia/Shanghai",
		Default: Rule{
			Enabled:             false,
			Strategy:            "fixed_window",
			Period:              "daily",
			ResetAt:             "04:00",
			Limit:               Duration{2 * time.Hour},
			CooldownEvery:       Duration{2 * time.Hour},
			CooldownRest:        Duration{30 * time.Minute},
			CreditRecoverEvery:  Duration{time.Hour},
			CreditRecoverAmount: Duration{30 * time.Minute},
			CreditMax:           Duration{3 * time.Hour},
			WarningBefore: []Duration{
				{30 * time.Minute}, {10 * time.Minute}, {5 * time.Minute}, {time.Minute},
			},
		},
		Overrides: map[string]RuleOverride{},
	}
}

func (c *Config) validate(lookup func(string) (string, bool)) error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	if parsed, err := url.ParseRequestURI(c.Server.BaseURL); err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("server.base_url must be an absolute HTTP URL")
	}
	if c.Server.PollInterval.Duration <= 0 || c.Server.RequestTimeout.Duration <= 0 {
		return fmt.Errorf("server poll_interval and request_timeout must be positive")
	}
	if c.Server.MaxObservationGap.Duration < c.Server.PollInterval.Duration {
		return fmt.Errorf("server.max_observation_gap must be at least poll_interval")
	}
	if c.Server.PasswordEnv == "" {
		return fmt.Errorf("server.password_env is required")
	}
	password, ok := lookup(c.Server.PasswordEnv)
	if !ok || password == "" {
		return fmt.Errorf("environment variable %s is required", c.Server.PasswordEnv)
	}
	c.password = password
	if err := ValidatePolicy(c.Policy); err != nil {
		return err
	}
	if c.Enforcement.KickRetryInitial.Duration <= 0 || c.Enforcement.KickRetryMax.Duration < c.Enforcement.KickRetryInitial.Duration {
		return fmt.Errorf("enforcement retry durations are invalid")
	}
	data := struct{ PlayerName, Remaining, ResetAt string }{"player", "1h", "2026-07-10T04:00:00+08:00"}
	for name, text := range map[string]string{"kick_message": c.Enforcement.KickMessage, "announce_message": c.Enforcement.AnnounceMessage} {
		tpl, err := template.New(name).Option("missingkey=error").Parse(text)
		if err != nil {
			return fmt.Errorf("enforcement.%s: %w", name, err)
		}
		if err := tpl.Execute(io.Discard, data); err != nil {
			return fmt.Errorf("enforcement.%s: %w", name, err)
		}
	}
	if c.HTTP.Listen == "" || c.Storage.Path == "" {
		return fmt.Errorf("http.listen and storage.path are required")
	}
	if c.Observation.ServerDocumentInterval.Duration <= 0 {
		return fmt.Errorf("observation.server_document_interval must be positive")
	}
	if c.Observation.TrajectoryMaxInterval.Duration <= 0 {
		return fmt.Errorf("observation.trajectory_max_interval must be positive")
	}
	if c.Observation.RawRetention.Duration <= 0 {
		return fmt.Errorf("observation.raw_retention must be positive")
	}
	if c.Observation.TrajectoryMinDistance < 0 || math.IsNaN(c.Observation.TrajectoryMinDistance) || math.IsInf(c.Observation.TrajectoryMinDistance, 0) {
		return fmt.Errorf("observation.trajectory_min_distance must be nonnegative and finite")
	}
	if c.Observation.TrajectoryPingChangeThreshold <= 0 || math.IsNaN(c.Observation.TrajectoryPingChangeThreshold) || math.IsInf(c.Observation.TrajectoryPingChangeThreshold, 0) {
		return fmt.Errorf("observation.trajectory_ping_change_threshold must be positive and finite")
	}
	if c.HTTP.AdminUsernameEnv != "" || c.HTTP.AdminPasswordEnv != "" {
		if c.HTTP.AdminUsernameEnv == "" || c.HTTP.AdminPasswordEnv == "" {
			return fmt.Errorf("http admin username and password env must be configured together")
		}
		adminUser, ok := lookup(c.HTTP.AdminUsernameEnv)
		if !ok || adminUser == "" {
			return fmt.Errorf("environment variable %s is required", c.HTTP.AdminUsernameEnv)
		}
		adminPass, ok := lookup(c.HTTP.AdminPasswordEnv)
		if !ok || adminPass == "" {
			return fmt.Errorf("environment variable %s is required", c.HTTP.AdminPasswordEnv)
		}
		c.adminUser = adminUser
		c.adminPass = adminPass
	}
	return nil
}

func ValidatePolicy(policy Policy) error {
	loc, err := time.LoadLocation(policy.Timezone)
	if err != nil || loc == nil {
		return fmt.Errorf("policy.timezone is invalid: %s", policy.Timezone)
	}
	if policy.Overrides == nil {
		policy.Overrides = map[string]RuleOverride{}
	}
	if err := validateRule("policy.default", policy.Default); err != nil {
		return err
	}
	for userID, override := range policy.Overrides {
		if strings.TrimSpace(userID) == "" {
			return fmt.Errorf("policy override user ID cannot be empty")
		}
		if override.Exempt {
			continue
		}
		resolved := applyOverride(policy.Default, override)
		if err := validateRule("policy.overrides."+userID, resolved); err != nil {
			return err
		}
	}
	return nil
}

func validateRule(path string, rule Rule) error {
	strategy := normalizedStrategy(rule.Strategy)
	limit := ruleLimit(rule)
	switch strategy {
	case "fixed_window":
		if rule.Period != "daily" && rule.Period != "weekly" {
			return fmt.Errorf("%s.period must be daily or weekly", path)
		}
		if _, _, err := parseClock(rule.ResetAt); err != nil {
			return fmt.Errorf("%s.reset_at: %w", path, err)
		}
		if rule.Period == "weekly" {
			if _, err := parseWeekday(rule.ResetWeekday); err != nil {
				return fmt.Errorf("%s.reset_weekday: %w", path, err)
			}
		}
	case "cooldown":
		if rule.CooldownEvery.Duration <= 0 || rule.CooldownRest.Duration <= 0 {
			return fmt.Errorf("%s cooldown_every and cooldown_rest must be positive", path)
		}
	case "credit":
		if rule.CreditRecoverEvery.Duration <= 0 || rule.CreditRecoverAmount.Duration <= 0 || rule.CreditMax.Duration <= 0 {
			return fmt.Errorf("%s credit_recover_every, credit_recover_amount, and credit_max must be positive", path)
		}
	default:
		return fmt.Errorf("%s.strategy must be fixed_window, cooldown, or credit", path)
	}
	if limit <= 0 {
		return fmt.Errorf("%s.limit must be positive", path)
	}
	seen := map[time.Duration]bool{}
	last := time.Duration(1<<63 - 1)
	for _, warning := range rule.WarningBefore {
		if warning.Duration <= 0 || warning.Duration >= limit {
			return fmt.Errorf("%s warning thresholds must be positive and below limit", path)
		}
		if seen[warning.Duration] || warning.Duration >= last {
			return fmt.Errorf("%s warning thresholds must be unique and descending", path)
		}
		seen[warning.Duration] = true
		last = warning.Duration
	}
	return nil
}

func applyOverride(base Rule, override RuleOverride) Rule {
	if override.Enabled != nil {
		base.Enabled = *override.Enabled
	}
	if override.Strategy != nil {
		base.Strategy = *override.Strategy
	}
	if override.Period != nil {
		base.Period = *override.Period
	}
	if override.ResetAt != nil {
		base.ResetAt = *override.ResetAt
	}
	if override.ResetWeekday != nil {
		base.ResetWeekday = *override.ResetWeekday
	}
	if override.Limit != nil {
		base.Limit = *override.Limit
	}
	if override.CooldownEvery != nil {
		base.CooldownEvery = *override.CooldownEvery
	}
	if override.CooldownRest != nil {
		base.CooldownRest = *override.CooldownRest
	}
	if override.CreditRecoverEvery != nil {
		base.CreditRecoverEvery = *override.CreditRecoverEvery
	}
	if override.CreditRecoverAmount != nil {
		base.CreditRecoverAmount = *override.CreditRecoverAmount
	}
	if override.CreditMax != nil {
		base.CreditMax = *override.CreditMax
	}
	if override.WarningBefore != nil {
		base.WarningBefore = append([]Duration(nil), (*override.WarningBefore)...)
	}
	return base
}

func normalizedStrategy(strategy string) string {
	if strategy == "" {
		return "fixed_window"
	}
	return strategy
}

func ruleLimit(rule Rule) time.Duration {
	switch normalizedStrategy(rule.Strategy) {
	case "cooldown":
		return rule.CooldownEvery.Duration
	case "credit":
		return rule.CreditMax.Duration
	default:
		return rule.Limit.Duration
	}
}

func parseClock(value string) (int, int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, 0, fmt.Errorf("must use HH:MM")
	}
	return parsed.Hour(), parsed.Minute(), nil
}

func parseWeekday(value string) (time.Weekday, error) {
	weekdays := []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday}
	sort.SliceStable(weekdays, func(i, j int) bool { return weekdays[i] < weekdays[j] })
	for _, weekday := range weekdays {
		if strings.EqualFold(value, weekday.String()) {
			return weekday, nil
		}
	}
	return 0, fmt.Errorf("must be an English weekday name")
}
