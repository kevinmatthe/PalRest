package overlay

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestPresentationMarshalShapeAndConstructors(t *testing.T) {
	observedAt := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	progress := 0.5
	presentation := Presentation{
		Schema:       PresentationSchemaV1,
		GameID:       "palworld",
		UserID:       "steam_1",
		ObservedAt:   observedAt,
		FreshUntil:   observedAt.Add(15 * time.Second),
		SourceStatus: "online",
		Identity:     Identity{DisplayName: "Player"},
		Map:          &MapPosition{X: 1, Y: 2, Projection: "palworld_world_v1", TileSet: "palworld_default_v1", TileURL: "/map/tiles/{z}/{x}/{y}.png"},
		Fields: []DisplayField{
			StringDisplayField("identity.account", "Account", "text", "account_1", "normal", nil),
			NumberDisplayField("network.latency", "Latency", "latency_ms", 38.5, "normal", &progress),
			TimestampDisplayField("presence.last_online", "Last online", observedAt, "muted"),
			CoordinatesDisplayField("location.coordinates", "Coordinates", 1, 2, "normal"),
			UnavailableDisplayField("policy.remaining", "Remaining", "duration_ms", "muted"),
		},
	}
	if err := ValidatePresentation(presentation); err != nil {
		t.Fatalf("ValidatePresentation() error = %v", err)
	}

	data, err := json.Marshal(presentation)
	if err != nil {
		t.Fatalf("marshal presentation: %v", err)
	}
	root := decodeJSONObject(t, data)
	assertExactJSONKeys(t, root, "schema", "game_id", "user_id", "observed_at", "fresh_until", "source_status", "identity", "map", "fields")
	fields := decodeJSONRawArray(t, root["fields"])
	assertExactJSONKeys(t, decodeJSONObject(t, fields[0]), "id", "label", "kind", "available", "value", "tone")
	assertExactJSONKeys(t, decodeJSONObject(t, fields[1]), "id", "label", "kind", "available", "value", "tone", "progress")
	assertExactJSONKeys(t, decodeJSONObject(t, fields[4]), "id", "label", "kind", "available", "tone")
}

func TestDisplayFieldConstructorsRejectMismatchedKindsAndNonFiniteValues(t *testing.T) {
	tests := []struct {
		name  string
		build func()
	}{
		{name: "string with number kind", build: func() { StringDisplayField("field.one", "One", "integer", "1", "normal", nil) }},
		{name: "number with string kind", build: func() { NumberDisplayField("field.one", "One", "text", 1, "normal", nil) }},
		{name: "non-finite number", build: func() { NumberDisplayField("field.one", "One", "latency_ms", math.NaN(), "normal", nil) }},
		{name: "non-finite coordinate", build: func() { CoordinatesDisplayField("field.one", "One", math.Inf(1), 2, "normal") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("constructor did not reject invalid input")
				}
			}()
			tt.build()
		})
	}
}

func TestValidatePresentationRejectsInvalidFields(t *testing.T) {
	valid := StringDisplayField("identity.account", "Account", "text", "account", "normal", nil)
	progress := 0.5
	tests := []struct {
		name   string
		fields []DisplayField
		want   string
	}{
		{name: "duplicate ID", fields: []DisplayField{valid, valid}, want: "duplicate"},
		{name: "unsafe ID", fields: []DisplayField{{ID: "identity/IP", Label: "IP", Kind: "text", Available: true, Value: json.RawMessage(`"secret"`), Tone: "normal"}}, want: "unsafe"},
		{name: "unsupported kind", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "html", Available: true, Value: json.RawMessage(`"x"`), Tone: "normal"}}, want: "kind"},
		{name: "unsupported tone", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "text", Available: true, Value: json.RawMessage(`"x"`), Tone: "critical"}}, want: "tone"},
		{name: "available missing value", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "text", Available: true, Tone: "normal"}}, want: "value"},
		{name: "unavailable carries value", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "text", Value: json.RawMessage(`"x"`), Tone: "muted"}}, want: "unavailable"},
		{name: "unavailable carries progress", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "duration_ms", Tone: "muted", Progress: &progress}}, want: "unavailable"},
		{name: "progress below zero", fields: []DisplayField{withProgress(valid, -0.01)}, want: "progress"},
		{name: "progress above one", fields: []DisplayField{withProgress(valid, 1.01)}, want: "progress"},
		{name: "progress NaN", fields: []DisplayField{withProgress(valid, math.NaN())}, want: "progress"},
		{name: "text wrong JSON type", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "text", Available: true, Value: json.RawMessage(`1`), Tone: "normal"}}, want: "text"},
		{name: "status malformed JSON", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "status", Available: true, Value: json.RawMessage(`"online`), Tone: "normal"}}, want: "status"},
		{name: "integer fractional", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "integer", Available: true, Value: json.RawMessage(`1.5`), Tone: "normal"}}, want: "integer"},
		{name: "integer precision-hidden fraction", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "integer", Available: true, Value: json.RawMessage(`9007199254740992.1`), Tone: "normal"}}, want: "integer"},
		{name: "duration string", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "duration_ms", Available: true, Value: json.RawMessage(`"1000"`), Tone: "normal"}}, want: "duration_ms"},
		{name: "latency object", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "latency_ms", Available: true, Value: json.RawMessage(`{}`), Tone: "normal"}}, want: "latency_ms"},
		{name: "timestamp invalid", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "timestamp", Available: true, Value: json.RawMessage(`"yesterday"`), Tone: "normal"}}, want: "timestamp"},
		{name: "coordinates missing y", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "coordinates", Available: true, Value: json.RawMessage(`{"x":1}`), Tone: "normal"}}, want: "coordinates"},
		{name: "coordinates extra data", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "coordinates", Available: true, Value: json.RawMessage(`{"x":1,"y":2,"ip":"secret"}`), Tone: "normal"}}, want: "coordinates"},
		{name: "coordinates duplicate x", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "coordinates", Available: true, Value: json.RawMessage(`{"x":"hidden","x":1,"y":2}`), Tone: "normal"}}, want: "coordinates"},
		{name: "coordinates duplicate y", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "coordinates", Available: true, Value: json.RawMessage(`{"x":1,"y":0,"y":2}`), Tone: "normal"}}, want: "coordinates"},
		{name: "coordinates trailing JSON", fields: []DisplayField{{ID: "field.one", Label: "One", Kind: "coordinates", Available: true, Value: json.RawMessage(`{"x":1,"y":2} true`), Tone: "normal"}}, want: "coordinates"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePresentation(Presentation{Schema: PresentationSchemaV1, Fields: tt.fields})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("ValidatePresentation() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestValidatePresentationAcceptsExactIntegerAndCoordinateBoundaries(t *testing.T) {
	tests := []DisplayField{
		{ID: "integer.large", Label: "Large", Kind: "integer", Available: true, Value: json.RawMessage(`9007199254740992`), Tone: "normal"},
		{ID: "integer.exponent", Label: "Exponent", Kind: "integer", Available: true, Value: json.RawMessage(`1e3`), Tone: "normal"},
		{ID: "coordinates.reordered", Label: "Coordinates", Kind: "coordinates", Available: true, Value: json.RawMessage(`{"y":-2.5e2,"x":1.25}`), Tone: "normal"},
	}
	if err := ValidatePresentation(Presentation{Schema: PresentationSchemaV1, Fields: tests}); err != nil {
		t.Fatalf("ValidatePresentation() error = %v", err)
	}
}

func withProgress(field DisplayField, progress float64) DisplayField {
	field.Progress = &progress
	return field
}

func decodeJSONRawArray(t *testing.T, data []byte) []json.RawMessage {
	t.Helper()
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		t.Fatalf("decode JSON array: %v", err)
	}
	return values
}
