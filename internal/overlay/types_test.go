package overlay

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSnapshotV1Fixture(t *testing.T) {
	data, err := os.ReadFile("../../testdata/overlay/palworld_snapshot_v1.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	if snapshot.Schema != "overlay.snapshot/v1" {
		t.Errorf("Schema = %q, want %q", snapshot.Schema, "overlay.snapshot/v1")
	}
	if snapshot.GameID != "palworld" {
		t.Errorf("GameID = %q, want %q", snapshot.GameID, "palworld")
	}
	if snapshot.UserID != "steam_76561198000000001" {
		t.Errorf("UserID = %q, want %q", snapshot.UserID, "steam_76561198000000001")
	}
	if len(snapshot.Timers) != 4 {
		t.Errorf("len(Timers) = %d, want 4", len(snapshot.Timers))
	}
	if snapshot.Map == nil {
		t.Error("Map is nil")
	}
	if snapshot.Latency == nil {
		t.Error("Latency is nil")
	}

	lowerFixture := strings.ToLower(string(data))
	for _, forbidden := range []string{"ip", "password", "authorization", "private_samples"} {
		if strings.Contains(lowerFixture, forbidden) {
			t.Errorf("fixture contains forbidden field or value %q", forbidden)
		}
	}
}
