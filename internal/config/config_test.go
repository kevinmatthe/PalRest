package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDurationJSONMillisecondsRejectOverflow(t *testing.T) {
	maxMilliseconds := int64(math.MaxInt64) / int64(time.Millisecond)
	minMilliseconds := int64(math.MinInt64) / int64(time.Millisecond)
	tests := []struct {
		name    string
		value   int64
		want    time.Duration
		wantErr bool
	}{
		{"maximum representable quotient", maxMilliseconds, time.Duration(maxMilliseconds) * time.Millisecond, false},
		{"minimum representable quotient", minMilliseconds, time.Duration(minMilliseconds) * time.Millisecond, false},
		{"above maximum", maxMilliseconds + 1, 0, true},
		{"below minimum", minMilliseconds - 1, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var duration Duration
			err := duration.UnmarshalJSON([]byte(fmt.Sprintf("%d", tt.value)))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("value=%d duration=%s", tt.value, duration.Duration)
				}
				return
			}
			if err != nil || duration.Duration != tt.want {
				t.Fatalf("value=%d duration=%s want=%s err=%v", tt.value, duration.Duration, tt.want, err)
			}
		})
	}
}

func TestDayDurationGrammarAndBounds(t *testing.T) {
	tests := []struct {
		value   string
		want    time.Duration
		wantErr bool
	}{
		{"106751d", 106751 * 24 * time.Hour, false},
		{"106752d", 0, true},
		{"-1d", -24 * time.Hour, false},
		{"-106751d", -106751 * 24 * time.Hour, false},
		{"-106752d", 0, true},
		{"1.5d", 0, true},
		{"1e2d", 0, true},
		{"0x2d", 0, true},
		{"1d2h", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := parseDuration(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("duration=%s", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("duration=%s want=%s err=%v", got, tt.want, err)
			}
		})
	}
}

const validConfig = `
version: 1
server:
  base_url: http://palworld-server:8212/v1/api
  password_env: ADMIN_PASSWORD
  poll_interval: 30s
  request_timeout: 5s
  max_observation_gap: 75s
policy:
  timezone: Asia/Shanghai
  default:
    enabled: false
    strategy: fixed_window
    period: daily
    reset_at: "04:00"
    limit: 2h
    warning_before: [30m, 10m, 5m, 1m]
enforcement:
  kick_message: "Playtime limit reached. Try again after {{ .ResetAt }}."
  announce_message: "{{ .PlayerName }} has {{ .Remaining }} remaining."
  kick_retry_initial: 15s
  kick_retry_max: 5m
http:
  listen: 0.0.0.0:8080
storage:
  path: /data/guard.db
`

func env(name string) (string, bool) {
	if name == "ADMIN_PASSWORD" {
		return "secret", true
	}
	return "", false
}

func bootstrapConfig(policy string) string {
	return `version: 1
server:
  base_url: http://palworld-server:8212/v1/api
  password_env: ADMIN_PASSWORD
  poll_interval: 30s
  request_timeout: 5s
  max_observation_gap: 75s
` + policy + `
enforcement:
  kick_message: "reset {{ .ResetAt }}"
  announce_message: "{{ .PlayerName }}: {{ .Remaining }}"
  kick_retry_initial: 15s
  kick_retry_max: 5m
http:
  listen: 0.0.0.0:8080
storage:
  path: /data/guard.db
`
}

func TestParseUsesCodePolicyDefaultsWhenPolicyIsOmitted(t *testing.T) {
	cfg, err := Parse([]byte(bootstrapConfig("")), env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Policy.Timezone != "Asia/Shanghai" || cfg.Policy.Default.Strategy != "fixed_window" || cfg.Policy.Default.Limit.Duration != 2*time.Hour {
		t.Fatalf("policy=%+v", cfg.Policy)
	}
	if len(cfg.Policy.Default.WarningBefore) != 4 || cfg.Policy.Default.Enabled {
		t.Fatalf("default=%+v", cfg.Policy.Default)
	}
}

func TestParseOverlaysPartialYAMLPolicyOnCodeDefaults(t *testing.T) {
	cfg, err := Parse([]byte(bootstrapConfig("policy:\n  default:\n    enabled: true\n    limit: 90m\n")), env)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Policy.Default.Enabled || cfg.Policy.Default.Limit.Duration != 90*time.Minute || cfg.Policy.Default.ResetAt != "04:00" {
		t.Fatalf("default=%+v", cfg.Policy.Default)
	}
}

func TestParseRejectsYAMLPolicyOverrides(t *testing.T) {
	_, err := Parse([]byte(bootstrapConfig("policy:\n  overrides:\n    player: { exempt: true }\n")), env)
	if err == nil || !strings.Contains(err.Error(), "overrides") {
		t.Fatalf("err=%v", err)
	}
}

func TestParseRejectsUnknownBootstrapRuleField(t *testing.T) {
	_, err := Parse([]byte(bootstrapConfig("policy:\n  default:\n    limti: 90m\n")), env)
	if err == nil || !strings.Contains(err.Error(), "limti") {
		t.Fatalf("err=%v", err)
	}
}

func TestParseValidConfig(t *testing.T) {
	cfg, err := Parse([]byte(validConfig), env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != 1 || cfg.Policy.Default.Enabled {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.Server.PollInterval.Duration != 30*time.Second {
		t.Fatalf("poll interval=%v", cfg.Server.PollInterval.Duration)
	}
	if cfg.Password() != "secret" {
		t.Fatal("password was not resolved")
	}
}

func TestParseUsesObservationDefaultsWhenOmitted(t *testing.T) {
	cfg, err := Parse([]byte(validConfig), env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Observation.ServerDocumentInterval.Duration != 5*time.Minute ||
		cfg.Observation.TrajectoryMinDistance != 100 ||
		cfg.Observation.TrajectoryPingChangeThreshold != 10 ||
		cfg.Observation.TrajectoryMaxInterval.Duration != 5*time.Minute ||
		cfg.Observation.RawRetention.Duration != 90*24*time.Hour {
		t.Fatalf("observation defaults=%+v", cfg.Observation)
	}
}

func TestParseAcceptsExplicitObservationSettings(t *testing.T) {
	input := validConfig + `observation:
  server_document_interval: 7m
  trajectory_min_distance: 42.5
  trajectory_ping_change_threshold: 12.5
  trajectory_max_interval: 3m
  raw_retention: 30d
`
	cfg, err := Parse([]byte(input), env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Observation.ServerDocumentInterval.Duration != 7*time.Minute ||
		cfg.Observation.TrajectoryMinDistance != 42.5 ||
		cfg.Observation.TrajectoryPingChangeThreshold != 12.5 ||
		cfg.Observation.TrajectoryMaxInterval.Duration != 3*time.Minute ||
		cfg.Observation.RawRetention.Duration != 30*24*time.Hour {
		t.Fatalf("observation=%+v", cfg.Observation)
	}
}

func TestParseRejectsInvalidObservationSettings(t *testing.T) {
	tests := map[string]string{
		"zero server document interval": "server_document_interval: 0s",
		"zero trajectory max interval":  "trajectory_max_interval: 0s",
		"zero raw retention":            "raw_retention: 0s",
		"negative day raw retention":    "raw_retention: -1d",
		"negative trajectory distance":  "trajectory_min_distance: -1",
		"nan trajectory distance":       "trajectory_min_distance: .nan",
		"infinite trajectory distance":  "trajectory_min_distance: .inf",
		"zero ping threshold":           "trajectory_ping_change_threshold: 0",
		"negative ping threshold":       "trajectory_ping_change_threshold: -1",
		"nan ping threshold":            "trajectory_ping_change_threshold: .nan",
		"infinite ping threshold":       "trajectory_ping_change_threshold: .inf",
	}
	for name, setting := range tests {
		t.Run(name, func(t *testing.T) {
			input := validConfig + "observation:\n  " + setting + "\n"
			if _, err := Parse([]byte(input), env); err == nil || !strings.Contains(err.Error(), "observation.") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestParseRejectsUnknownObservationSetting(t *testing.T) {
	input := validConfig + "observation:\n  trajectory_min_distnace: 100\n"
	_, err := Parse([]byte(input), env)
	if err == nil || !strings.Contains(err.Error(), "trajectory_min_distnace") {
		t.Fatalf("error=%v", err)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	_, err := Parse([]byte(validConfig+"unknown: true\n"), env)
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsInvalidValues(t *testing.T) {
	tests := map[string]string{
		"timezone": strings.Replace(validConfig, "Asia/Shanghai", "Mars/Olympus", 1),
		"reset":    strings.Replace(validConfig, `"04:00"`, `"25:00"`, 1),
		"password": strings.Replace(validConfig, "ADMIN_PASSWORD", "MISSING_PASSWORD", 1),
		"warning":  strings.Replace(validConfig, "[30m, 10m, 5m, 1m]", "[1m, 5m]", 1),
		"version":  strings.Replace(validConfig, "version: 1", "version: 2", 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(input), env); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestParseRequiresWeekdayForWeeklyRule(t *testing.T) {
	input := strings.Replace(validConfig, "period: daily", "period: weekly", 1)
	if _, err := Parse([]byte(input), env); err == nil || !strings.Contains(err.Error(), "reset_weekday") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExampleConfigIsValidAndDisabled(t *testing.T) {
	path := filepath.Join("..", "..", "config.example.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Policy.Default.Enabled {
		t.Fatal("sample policy must default to disabled")
	}
	if cfg.Observation.ServerDocumentInterval.Duration != 5*time.Minute || cfg.Observation.TrajectoryMinDistance != 100 ||
		cfg.Observation.TrajectoryPingChangeThreshold != 10 || cfg.Observation.TrajectoryMaxInterval.Duration != 5*time.Minute || cfg.Observation.RawRetention.Duration != 90*24*time.Hour {
		t.Fatalf("sample observation=%+v", cfg.Observation)
	}
}
