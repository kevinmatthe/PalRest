package saveworker

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeWorker(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "worker")
	if runtime.GOOS == "windows" {
		path += ".bat"
	}
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunnerExtractsSnapshot(t *testing.T) {
	worker := writeWorker(t, `#!/bin/sh
cat <<'JSON'
{"schema":"palrest.save_snapshot.v1","parser":{"name":"test","version":1},"source":{"level_sav":"/save/Level.sav","fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","level_sav_size":1,"level_sav_mtime":"2026-07-13T08:00:00Z","captured_at":"2026-07-13T08:00:01Z","player_file_count":0},"players":[],"guilds":[]}
JSON
`)
	runner, err := New(worker, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runner.Extract(t.Context(), "/save/Level.sav")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Schema != "palrest.save_snapshot.v1" || snapshot.Source.Fingerprint != strings.Repeat("a", 64) {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestRunnerReportsWorkerFailureAndRejectsUnknownFields(t *testing.T) {
	failing := writeWorker(t, "#!/bin/sh\necho secret stderr >&2\nexit 3\n")
	runner, err := New(failing, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Extract(t.Context(), "/save/Level.sav"); err == nil || !strings.Contains(err.Error(), "secret stderr") {
		t.Fatalf("err=%v", err)
	}

	unknown := writeWorker(t, `#!/bin/sh
echo '{"schema":"palrest.save_snapshot.v1","extra":true}'
`)
	runner, err = New(unknown, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Extract(t.Context(), "/save/Level.sav"); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("err=%v", err)
	}
}
