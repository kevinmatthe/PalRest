package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
    period: daily
    reset_at: "04:00"
    limit: 2h
    warning_before: [30m, 10m, 5m, 1m]
  overrides:
    steam_1:
      limit: 4h
    steam_2:
      exempt: true
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
}
