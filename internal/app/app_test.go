package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
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
	if application.analytics == nil {
		t.Fatal("expected analytics service")
	}
	if application.CurrentConfig().Policy.Default.Enabled {
		t.Fatal("expected disabled policy")
	}
}

func TestNewWiresUnifiedPlayerAndServerObservations(t *testing.T) {
	requests := make(chan string, 20)
	palworld := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path
		switch r.URL.Path {
		case "/v1/api/players":
			_, _ = w.Write([]byte(`{"players":[{"name":"One","accountName":"one","playerId":"pal-1","userId":"u1","ip":"","ping":1,"location_x":10,"location_y":20,"level":3,"building_count":0}]}`))
		case "/v1/api/metrics":
			_, _ = w.Write([]byte(`{"serverfps":60,"currentplayernum":1,"serverframetime":1,"maxplayernum":32,"uptime":100,"basecampnum":1,"days":2}`))
		case "/v1/api/info":
			_, _ = w.Write([]byte(`{"version":"v1","servername":"server","description":"hello","worldguid":"world"}`))
		case "/v1/api/settings":
			_, _ = w.Write([]byte(`{"Difficulty":"Normal"}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(palworld.Close)
	t.Setenv("ADMIN_PASSWORD", "secret")
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "guard.db")
	configPath := filepath.Join(dir, "config.yaml")
	configData := strings.Replace(appConfig(palworld.URL+"/v1/api", dbPath, "127.0.0.1:0", false), "poll_interval: 20ms", "poll_interval: 1s", 1)
	configData = strings.Replace(configData, "max_observation_gap: 100ms", "max_observation_gap: 2s", 1)
	if err := os.WriteFile(configPath, []byte(configData), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(configPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		application.poller.Run(ctx)
		close(done)
	}()

	seen := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for !(seen["/v1/api/players"] && seen["/v1/api/metrics"] && seen["/v1/api/info"] && seen["/v1/api/settings"]) {
		select {
		case path := <-requests:
			seen[path] = true
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("timed out waiting for wired observation requests: %v", seen)
		}
	}
	for {
		_, _, metricErr := application.repo.LatestServerMetrics(t.Context())
		_, infoErr := application.repo.LatestServerDocument(t.Context(), "info")
		_, settingsErr := application.repo.LatestServerDocument(t.Context(), "settings")
		if metricErr == nil && infoErr == nil && settingsErr == nil {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("timed out waiting for durable server observations: metric=%v info=%v settings=%v", metricErr, infoErr, settingsErr)
		}
	}
	cancel()
	<-done

	timeline, err := application.repo.ReadSensitivePlayerTimeline(t.Context(), "test", "u1", time.Now().Add(-time.Minute), time.Now().Add(time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline.Events) != 1 || timeline.Events[0].EventType != "player_joined" || len(timeline.Trajectories) != 1 || len(timeline.PrivateSamples) != 0 {
		t.Fatalf("events=%+v samples=%+v", timeline.Events, timeline.Trajectories)
	}
	if err := application.repo.RecordServerMetrics(t.Context(), time.Unix(1, 0), domain.ServerMetrics{ServerFrameTime: 1}); err == nil {
		t.Fatal("expected old metric to be rejected after wired sampler persisted a sample")
	}
	latestInfo, err := application.repo.LatestServerDocument(t.Context(), "info")
	if err != nil {
		t.Fatal(err)
	}
	if string(latestInfo.Canonical) != `{"version":"v1","servername":"server","description":"hello","worldguid":"world"}` {
		t.Fatalf("canonical info=%s", latestInfo.Canonical)
	}
}

func TestNewUsesObservationSamplingConfiguration(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	palworld := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.URL.Path]++
		call := counts[r.URL.Path]
		mu.Unlock()
		switch r.URL.Path {
		case "/v1/api/players":
			_, _ = fmt.Fprintf(w, `{"players":[{"name":"One","accountName":"one","playerId":"pal-1","userId":"u1","ip":"127.0.0.1","ping":1,"location_x":%d,"location_y":20,"level":3,"building_count":0}]}`, call)
		case "/v1/api/metrics":
			_, _ = w.Write([]byte(`{"serverfps":60,"currentplayernum":1,"serverframetime":1,"maxplayernum":32,"uptime":100,"basecampnum":1,"days":2}`))
		case "/v1/api/info":
			_, _ = w.Write([]byte(`{"version":"v1","servername":"server","description":"hello","worldguid":"world"}`))
		case "/v1/api/settings":
			_, _ = w.Write([]byte(`{"Difficulty":"Normal"}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(palworld.Close)
	t.Setenv("ADMIN_PASSWORD", "secret")
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "guard.db")
	configPath := filepath.Join(dir, "config.yaml")
	configData := appConfig(palworld.URL+"/v1/api", dbPath, "127.0.0.1:0", false) + `
observation:
  server_document_interval: 1h
  trajectory_min_distance: 0
  trajectory_max_interval: 1h
  raw_retention: 1d
`
	if err := os.WriteFile(configPath, []byte(configData), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(configPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		application.poller.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		metricsCalls := counts["/v1/api/metrics"]
		mu.Unlock()
		if metricsCalls >= 3 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("timed out waiting for samples: %+v", counts)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	infoCalls, settingsCalls := counts["/v1/api/info"], counts["/v1/api/settings"]
	mu.Unlock()
	if infoCalls != 1 || settingsCalls != 1 {
		t.Fatalf("metadata cadence ignored: info=%d settings=%d", infoCalls, settingsCalls)
	}
	timeline, err := application.repo.ReadSensitivePlayerTimeline(t.Context(), "test", "u1", time.Now().Add(-time.Minute), time.Now().Add(time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline.Trajectories) < 3 {
		t.Fatalf("trajectory distance ignored: samples=%d", len(timeline.Trajectories))
	}
}

func TestNewRestoresDurableServerBaselinesBeforePolling(t *testing.T) {
	palworld := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/api/players" {
			_, _ = w.Write([]byte(`{"players":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer palworld.Close()
	t.Setenv("ADMIN_PASSWORD", "secret")
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "guard.db")
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(appConfig(palworld.URL+"/v1/api", dbPath, "127.0.0.1:0", false)), 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	invalidCanonical := []byte(`{"version":5}`)
	invalidDigest := sha256.Sum256(invalidCanonical)
	invalidTypedInfo := store.ServerDocumentObservation{
		Kind: "info", At: time.Now(), Canonical: invalidCanonical, Hash: hex.EncodeToString(invalidDigest[:]),
	}
	if _, err := repo.RecordServerDocumentObservation(t.Context(), invalidTypedInfo); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	application, err := New(configPath)
	if err == nil {
		_ = application.Close()
		t.Fatal("expected startup restore to reject invalid durable typed info")
	}
	if !strings.Contains(err.Error(), "restore observed server info") {
		t.Fatalf("error=%v", err)
	}
}

func TestNewRestoresOpenAnalyticsSessions(t *testing.T) {
	application, path := newTestApp(t)
	at := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	if err := application.repo.RecordAnalyticsObservation(t.Context(), store.AnalyticsObservation{
		At: at, LocalDate: "2026-07-11", Players: []domain.Player{{UserID: "u1", Name: "One"}}, JoinedUserIDs: []string{"u1"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	ids, restoredAt := restarted.analytics.Current()
	if len(ids) != 1 || ids[0] != "u1" || !restoredAt.Equal(at) {
		t.Fatalf("restored ids=%v at=%v", ids, restoredAt)
	}
}

func TestReloadIgnoresYAMLPolicyAfterDatabaseInitialization(t *testing.T) {
	application, path := newTestApp(t)
	before := application.policies.Resolve("player")
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
	if after := application.policies.Resolve("player"); after.Revision != before.Revision {
		t.Fatalf("YAML reload replaced database policy: before=%+v after=%+v", before, after)
	}
	invalid := strings.Replace(enabled, "timezone: Asia/Shanghai", "timezone: Invalid/Zone", 1)
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := application.reload(); err != nil {
		t.Fatalf("ignored YAML policy blocked reload: %v", err)
	}
	if after := application.policies.Resolve("player"); after.Revision != before.Revision {
		t.Fatalf("invalid YAML policy replaced database policy: before=%+v after=%+v", before, after)
	}
	if application.poller.Status().ConfigReloadErr != "" {
		t.Fatalf("ignored YAML policy reported reload error: %s", application.poller.Status().ConfigReloadErr)
	}
}

func TestNewIgnoresInvalidYAMLPolicyWhenDatabaseHasPolicy(t *testing.T) {
	application, path := newTestApp(t)
	stored := application.policies.Policy()
	stored.Default.Limit.Duration = 3 * time.Hour
	if err := application.policies.SetPolicy(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	before := application.policies.Resolve("player")
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	invalid := strings.Replace(string(data), "timezone: Asia/Shanghai", "timezone: Invalid/Zone", 1)
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(path)
	if err != nil {
		t.Fatalf("existing database policy should make YAML policy irrelevant: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if after := restarted.policies.Resolve("player"); after.Revision != before.Revision {
		t.Fatalf("restart replaced database policy: before=%+v after=%+v", before, after)
	}
	if got := restarted.CurrentConfig().Policy.Default.Limit.Duration; got != 3*time.Hour {
		t.Fatalf("current config policy=%v, want stored 3h", got)
	}
}

func TestNewIgnoresInvalidYAMLPolicyWhenDatabaseAlreadyHasPolicy(t *testing.T) {
	application, path := newTestApp(t)
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	invalid := strings.Replace(string(data), "timezone: Asia/Shanghai", "timezone: Invalid/Zone", 1)
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(path)
	if err != nil {
		t.Fatalf("stored database policy should make YAML policy irrelevant: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if got := restarted.policies.Resolve("player").Timezone; got != "Asia/Shanghai" {
		t.Fatalf("timezone=%q", got)
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

func TestReloadRejectsObservationSettingsWithoutChangingPolicy(t *testing.T) {
	application, path := newTestApp(t)
	before := application.policies.Resolve("player")
	beforeObservation := application.CurrentConfig().Observation
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := string(data) + "\nobservation:\n  trajectory_min_distance: 25\n"
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	err = application.reload()
	if err == nil || !strings.Contains(err.Error(), "observation") || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("error=%v", err)
	}
	if after := application.policies.Resolve("player"); after.Revision != before.Revision {
		t.Fatalf("observation reload changed stored policy: before=%+v after=%+v", before, after)
	}
	if after := application.CurrentConfig().Observation; !reflect.DeepEqual(after, beforeObservation) {
		t.Fatalf("rejected reload changed observation config: before=%+v after=%+v", beforeObservation, after)
	}
	if reloadErr := application.poller.Status().ConfigReloadErr; reloadErr == "" {
		t.Fatal("rejected observation reload did not update poll status")
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
		select {
		case <-application.pollerDone:
		default:
			t.Fatal("Run returned before poller stopped")
		}
		select {
		case <-application.watcherDone:
		default:
			t.Fatal("Run returned before config watcher stopped")
		}
	case <-time.After(time.Second):
		t.Fatal("application did not stop")
	}
}
