package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func appConfig(baseURL, dbPath, listen string, enabled bool) string {
	return fmt.Sprintf(`
version: 1
server:
  base_url: %s
  password_env: ADMIN_PASSWORD
  poll_interval: 20ms
  request_timeout: 100ms
  max_observation_gap: 100ms
policy:
  timezone: Asia/Shanghai
  default:
    enabled: %t
    period: daily
    reset_at: "04:00"
    limit: 2h
    warning_before: [30m, 5m]
  overrides: {}
enforcement:
  kick_message: "reset {{ .ResetAt }}"
  announce_message: "{{ .PlayerName }}: {{ .Remaining }}"
  kick_retry_initial: 15s
  kick_retry_max: 5m
http:
  listen: %s
storage:
  path: %s
`, baseURL, enabled, listen, dbPath)
}

func newTestApp(t *testing.T) (*App, string) {
	t.Helper()
	palworld := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/api/players" {
			_, _ = w.Write([]byte(`{"players":[]}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(palworld.Close)
	t.Setenv("ADMIN_PASSWORD", "secret")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := appConfig(palworld.URL+"/v1/api", filepath.Join(dir, "guard.db"), "127.0.0.1:0", false)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	return application, path
}

func TestNewLoadsDisabledConfiguration(t *testing.T) {
	application, _ := newTestApp(t)
	if application.CurrentConfig().Policy.Default.Enabled {
		t.Fatal("expected disabled policy")
	}
}

func TestReloadAppliesValidPolicyAndRetainsOldOnInvalid(t *testing.T) {
	application, path := newTestApp(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	enabled := strings.Replace(string(data), "enabled: false", "enabled: true", 1)
	if err := os.WriteFile(path, []byte(enabled), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := application.reload(); err != nil {
		t.Fatal(err)
	}
	if !application.CurrentConfig().Policy.Default.Enabled {
		t.Fatal("valid reload was not applied")
	}
	invalid := strings.Replace(enabled, "timezone: Asia/Shanghai", "timezone: Invalid/Zone", 1)
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := application.reload(); err == nil {
		t.Fatal("expected invalid reload error")
	}
	if !application.CurrentConfig().Policy.Default.Enabled {
		t.Fatal("invalid reload replaced active config")
	}
	if application.poller.Status().ConfigReloadErr == "" {
		t.Fatal("reload error not exposed")
	}
}

func TestReloadRejectsStartupOnlySettings(t *testing.T) {
	application, path := newTestApp(t)
	data, _ := os.ReadFile(path)
	changed := strings.Replace(string(data), "127.0.0.1:0", "127.0.0.1:9999", 1)
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := application.reload(); err == nil || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("error=%v", err)
	}
}

func TestRunStopsOnCancellation(t *testing.T) {
	application, _ := newTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("application did not stop")
	}
}
