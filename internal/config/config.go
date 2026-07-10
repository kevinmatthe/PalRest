package config

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"text/template"
	"time"

	"go.yaml.in/yaml/v3"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	value, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", node.Value, err)
	}
	d.Duration = value
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

type Config struct {
	Version     int         `yaml:"version" json:"version"`
	Server      Server      `yaml:"server" json:"server"`
	Policy      Policy      `yaml:"policy" json:"policy"`
	Enforcement Enforcement `yaml:"enforcement" json:"enforcement"`
	HTTP        HTTP        `yaml:"http" json:"http"`
	Storage     Storage     `yaml:"storage" json:"storage"`
	password    string
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
	Listen string `yaml:"listen" json:"listen"`
}

type Storage struct {
	Path string `yaml:"path" json:"path"`
}

func (c Config) Password() string { return c.password }

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

func defaults() Config {
	return Config{
		Version: 1,
		Server: Server{
			PollInterval:      Duration{30 * time.Second},
			RequestTimeout:    Duration{5 * time.Second},
			MaxObservationGap: Duration{75 * time.Second},
		},
		Policy: Policy{Overrides: map[string]RuleOverride{}},
		Enforcement: Enforcement{
			KickRetryInitial: Duration{15 * time.Second},
			KickRetryMax:     Duration{5 * time.Minute},
		},
		HTTP:    HTTP{Listen: "0.0.0.0:8080"},
		Storage: Storage{Path: "/data/guard.db"},
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
	loc, err := time.LoadLocation(c.Policy.Timezone)
	if err != nil || loc == nil {
		return fmt.Errorf("policy.timezone is invalid: %s", c.Policy.Timezone)
	}
	if err := validateRule("policy.default", c.Policy.Default); err != nil {
		return err
	}
	for userID, override := range c.Policy.Overrides {
		if strings.TrimSpace(userID) == "" {
			return fmt.Errorf("policy override user ID cannot be empty")
		}
		if override.Exempt {
			continue
		}
		resolved := applyOverride(c.Policy.Default, override)
		if err := validateRule("policy.overrides."+userID, resolved); err != nil {
			return err
		}
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
