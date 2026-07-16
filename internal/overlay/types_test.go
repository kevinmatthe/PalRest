package overlay

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"
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

	if snapshot.Schema != SchemaV1 {
		t.Errorf("Schema = %q, want %q", snapshot.Schema, SchemaV1)
	}
	if snapshot.GameID != "palworld" {
		t.Errorf("GameID = %q, want %q", snapshot.GameID, "palworld")
	}
	if snapshot.UserID != "steam_76561198000000001" {
		t.Errorf("UserID = %q, want %q", snapshot.UserID, "steam_76561198000000001")
	}
	if snapshot.ObservedAt != time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) {
		t.Errorf("ObservedAt = %s, want 2026-07-16T12:00:00Z", snapshot.ObservedAt)
	}
	if snapshot.FreshUntil != time.Date(2026, 7, 16, 12, 0, 15, 0, time.UTC) {
		t.Errorf("FreshUntil = %s, want 2026-07-16T12:00:15Z", snapshot.FreshUntil)
	}
	if snapshot.SourceStatus != "online" {
		t.Errorf("SourceStatus = %q, want %q", snapshot.SourceStatus, "online")
	}

	wantCapabilities := []string{"identity", "latency", "timers", "map"}
	if len(snapshot.Capabilities) != len(wantCapabilities) {
		t.Fatalf("Capabilities = %q, want %q", snapshot.Capabilities, wantCapabilities)
	}
	for i, want := range wantCapabilities {
		if snapshot.Capabilities[i] != want {
			t.Errorf("Capabilities[%d] = %q, want %q", i, snapshot.Capabilities[i], want)
		}
	}

	if snapshot.Identity.DisplayName != "Lamball Keeper" {
		t.Errorf("Identity.DisplayName = %q, want %q", snapshot.Identity.DisplayName, "Lamball Keeper")
	}
	if snapshot.Identity.AccountName != "" {
		t.Errorf("Identity.AccountName = %q, want empty", snapshot.Identity.AccountName)
	}
	if snapshot.Identity.Level == nil || *snapshot.Identity.Level != 42 {
		t.Errorf("Identity.Level = %v, want 42", snapshot.Identity.Level)
	}
	if snapshot.Latency == nil || snapshot.Latency.Milliseconds != 38.5 {
		t.Errorf("Latency = %#v, want 38.5 milliseconds", snapshot.Latency)
	}

	wantTimers := []struct {
		id       string
		label    string
		valueMS  int64
		semantic string
		tone     string
		progress float64
	}{
		{"today_observed", "Today observed", 7200000, "duration", "normal", 0.333333},
		{"week_observed", "Week observed", 21600000, "duration", "normal", 0.428571},
		{"policy_cycle_used", "Policy cycle used", 21600000, "duration", "warning", 0.75},
		{"policy_remaining", "Policy remaining", 7200000, "duration", "warning", 0.25},
	}
	if len(snapshot.Timers) != len(wantTimers) {
		t.Fatalf("len(Timers) = %d, want %d", len(snapshot.Timers), len(wantTimers))
	}
	for i, want := range wantTimers {
		got := snapshot.Timers[i]
		if got.ID != want.id || got.Label != want.label || got.ValueMS != want.valueMS || got.Semantic != want.semantic || got.Tone != want.tone {
			t.Errorf("Timers[%d] = %#v, want id=%q label=%q value_ms=%d semantic=%q tone=%q", i, got, want.id, want.label, want.valueMS, want.semantic, want.tone)
		}
		if got.Progress == nil || *got.Progress != want.progress {
			t.Errorf("Timers[%d].Progress = %v, want %v", i, got.Progress, want.progress)
		}
	}

	if snapshot.Map == nil {
		t.Fatal("Map is nil")
	}
	if snapshot.Map.X != 187.25 || snapshot.Map.Y != -64.5 {
		t.Errorf("Map coordinates = (%v, %v), want (187.25, -64.5)", snapshot.Map.X, snapshot.Map.Y)
	}
	if snapshot.Map.Projection != "palworld_world_v1" {
		t.Errorf("Map.Projection = %q, want %q", snapshot.Map.Projection, "palworld_world_v1")
	}
	if snapshot.Map.TileSet != "palworld_default_v1" {
		t.Errorf("Map.TileSet = %q, want %q", snapshot.Map.TileSet, "palworld_default_v1")
	}
	if snapshot.Map.TileURL != "/map/tiles/{z}/{x}/{y}.png" {
		t.Errorf("Map.TileURL = %q, want %q", snapshot.Map.TileURL, "/map/tiles/{z}/{x}/{y}.png")
	}

	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode fixture for sensitive-key inspection: %v", err)
	}
	assertNoSensitiveJSONKeys(t, raw, "fixture")
}

func TestSnapshotV1MarshalShape(t *testing.T) {
	level := 42
	progress := 0.5
	snapshot := Snapshot{
		Schema:       SchemaV1,
		GameID:       "palworld",
		UserID:       "steam_user",
		ObservedAt:   time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		FreshUntil:   time.Date(2026, 7, 16, 12, 0, 15, 0, time.UTC),
		SourceStatus: "online",
		Capabilities: []string{"identity", "latency", "timers", "map"},
		Identity: Identity{
			DisplayName: "Player",
			AccountName: "account",
			Level:       &level,
		},
		Latency: &Latency{Milliseconds: 12.5},
		Timers: []Timer{{
			ID:       "today_observed",
			Label:    "Today observed",
			ValueMS:  1000,
			Semantic: "duration",
			Tone:     "normal",
			Progress: &progress,
		}},
		Map: &MapPosition{
			X:          1,
			Y:          2,
			Projection: "palworld_world_v1",
			TileSet:    "palworld_default_v1",
			TileURL:    "/map/tiles/{z}/{x}/{y}.png",
		},
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	root := decodeJSONObject(t, data)
	assertExactJSONKeys(t, root,
		"schema", "game_id", "user_id", "observed_at", "fresh_until", "source_status",
		"capabilities", "identity", "latency", "timers", "map",
	)
	assertExactJSONKeys(t, decodeJSONObject(t, root["identity"]), "display_name", "account_name", "level")
	assertExactJSONKeys(t, decodeJSONObject(t, root["latency"]), "milliseconds")
	assertExactJSONKeys(t, decodeJSONArrayObject(t, root["timers"], 0), "id", "label", "value_ms", "semantic", "tone", "progress")
	assertExactJSONKeys(t, decodeJSONObject(t, root["map"]), "x", "y", "projection", "tile_set", "tile_url")
}

func TestSnapshotV1CollectionOmission(t *testing.T) {
	tests := []struct {
		name         string
		capabilities []string
		timers       []Timer
		wantCapsJSON string
		wantTimers   bool
	}{
		{name: "nil collections", wantCapsJSON: "null"},
		{name: "empty collections", capabilities: []string{}, timers: []Timer{}, wantCapsJSON: "[]"},
		{name: "populated timers", capabilities: []string{}, timers: []Timer{{ID: "today_observed"}}, wantCapsJSON: "[]", wantTimers: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(Snapshot{Capabilities: tt.capabilities, Timers: tt.timers})
			if err != nil {
				t.Fatalf("marshal snapshot: %v", err)
			}
			fields := decodeJSONObject(t, data)
			gotCapabilities, ok := fields["capabilities"]
			if !ok {
				t.Fatal("capabilities must always be present")
			}
			if string(gotCapabilities) != tt.wantCapsJSON {
				t.Errorf("capabilities JSON = %s, want %s", gotCapabilities, tt.wantCapsJSON)
			}
			_, gotTimers := fields["timers"]
			if gotTimers != tt.wantTimers {
				t.Errorf("timers present = %v, want %v", gotTimers, tt.wantTimers)
			}
		})
	}
}

func decodeJSONObject(t *testing.T, data []byte) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("decode JSON object: %v", err)
	}
	return object
}

func decodeJSONArrayObject(t *testing.T, data []byte, index int) map[string]json.RawMessage {
	t.Helper()
	var array []json.RawMessage
	if err := json.Unmarshal(data, &array); err != nil {
		t.Fatalf("decode JSON array: %v", err)
	}
	if index >= len(array) {
		t.Fatalf("JSON array index %d out of range", index)
	}
	return decodeJSONObject(t, array[index])
}

func assertExactJSONKeys(t *testing.T, object map[string]json.RawMessage, want ...string) {
	t.Helper()
	if len(object) != len(want) {
		t.Errorf("JSON keys count = %d, want %d; object = %s", len(object), len(want), mustMarshalJSON(t, object))
	}
	for _, key := range want {
		if _, ok := object[key]; !ok {
			t.Errorf("JSON object missing key %q", key)
		}
	}
}

func assertNoSensitiveJSONKeys(t *testing.T, value any, path string) {
	t.Helper()
	forbidden := map[string]bool{
		"ip":              true,
		"password":        true,
		"authorization":   true,
		"private_samples": true,
		"location_x":      true,
		"location_y":      true,
		"account_name":    true,
		"player_id":       true,
	}

	var walk func(any, string)
	walk = func(current any, currentPath string) {
		switch current := current.(type) {
		case map[string]any:
			for key, child := range current {
				if forbidden[key] {
					t.Errorf("sensitive JSON key %q found at %s", key, currentPath)
				}
				walk(child, currentPath+"."+key)
			}
		case []any:
			for i, child := range current {
				walk(child, currentPath+"["+strconv.Itoa(i)+"]")
			}
		}
	}
	walk(value, path)
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal diagnostic JSON: %v", err)
	}
	return data
}
