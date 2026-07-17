package overlay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"
)

const PresentationSchemaV1 = "overlay.presentation/v1"

type Presentation struct {
	Schema       string         `json:"schema"`
	GameID       string         `json:"game_id"`
	UserID       string         `json:"user_id"`
	ObservedAt   time.Time      `json:"observed_at"`
	FreshUntil   time.Time      `json:"fresh_until"`
	SourceStatus string         `json:"source_status"`
	Identity     Identity       `json:"identity"`
	Map          *MapPosition   `json:"map,omitempty"`
	Fields       []DisplayField `json:"fields"`
}

type DisplayField struct {
	ID        string          `json:"id"`
	Label     string          `json:"label"`
	Kind      string          `json:"kind"`
	Available bool            `json:"available"`
	Value     json.RawMessage `json:"value,omitempty"`
	Tone      string          `json:"tone"`
	Progress  *float64        `json:"progress,omitempty"`
}

type displayCoordinates struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func StringDisplayField(id, label, kind, value, tone string, progress *float64) DisplayField {
	if kind != "text" && kind != "status" {
		panic("overlay: string display field requires text or status kind")
	}
	return availableDisplayField(id, label, kind, value, tone, progress)
}

func NumberDisplayField(id, label, kind string, value float64, tone string, progress *float64) DisplayField {
	if kind != "integer" && kind != "duration_ms" && kind != "latency_ms" {
		panic("overlay: number display field requires a numeric kind")
	}
	if !finite(value) || kind == "integer" && math.Trunc(value) != value {
		panic("overlay: number display field requires a valid numeric value")
	}
	return availableDisplayField(id, label, kind, value, tone, progress)
}

func TimestampDisplayField(id, label string, value time.Time, tone string) DisplayField {
	return availableDisplayField(id, label, "timestamp", value.Format(time.RFC3339Nano), tone, nil)
}

func CoordinatesDisplayField(id, label string, x, y float64, tone string) DisplayField {
	if !finite(x) || !finite(y) {
		panic("overlay: coordinates display field requires finite values")
	}
	return availableDisplayField(id, label, "coordinates", displayCoordinates{X: x, Y: y}, tone, nil)
}

func UnavailableDisplayField(id, label, kind, tone string) DisplayField {
	return DisplayField{ID: id, Label: label, Kind: kind, Tone: tone}
}

func availableDisplayField(id, label, kind string, value any, tone string, progress *float64) DisplayField {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("overlay: marshal display field value: %v", err))
	}
	return DisplayField{ID: id, Label: label, Kind: kind, Available: true, Value: encoded, Tone: tone, Progress: progress}
}

func ValidatePresentation(presentation Presentation) error {
	if presentation.Schema != PresentationSchemaV1 {
		return fmt.Errorf("presentation schema %q is unsupported", presentation.Schema)
	}
	seen := make(map[string]struct{}, len(presentation.Fields))
	for index, field := range presentation.Fields {
		if !safeDisplayFieldID(field.ID) {
			return fmt.Errorf("presentation field %d has unsafe ID %q", index, field.ID)
		}
		if _, exists := seen[field.ID]; exists {
			return fmt.Errorf("presentation field ID %q is duplicate", field.ID)
		}
		seen[field.ID] = struct{}{}
		if !supportedDisplayKind(field.Kind) {
			return fmt.Errorf("presentation field %q has unsupported kind %q", field.ID, field.Kind)
		}
		if !supportedDisplayTone(field.Tone) {
			return fmt.Errorf("presentation field %q has unsupported tone %q", field.ID, field.Tone)
		}
		if !field.Available {
			if len(field.Value) != 0 || field.Progress != nil {
				return fmt.Errorf("presentation field %q is unavailable but carries value or progress", field.ID)
			}
			continue
		}
		if len(field.Value) == 0 {
			return fmt.Errorf("presentation field %q is available but has no value", field.ID)
		}
		if field.Progress != nil && (!finite(*field.Progress) || *field.Progress < 0 || *field.Progress > 1) {
			return fmt.Errorf("presentation field %q has invalid progress", field.ID)
		}
		if err := validateDisplayValue(field.Kind, field.Value); err != nil {
			return fmt.Errorf("presentation field %q has invalid %s value: %w", field.ID, field.Kind, err)
		}
	}
	return nil
}

func safeDisplayFieldID(id string) bool {
	if len(id) == 0 || len(id) > 96 || !lowerAlphaNumeric(id[0]) {
		return false
	}
	for i := 1; i < len(id); i++ {
		if !lowerAlphaNumeric(id[i]) && id[i] != '.' && id[i] != '_' && id[i] != '-' {
			return false
		}
	}
	return true
}

func lowerAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func supportedDisplayKind(kind string) bool {
	switch kind {
	case "text", "integer", "duration_ms", "timestamp", "latency_ms", "coordinates", "status":
		return true
	default:
		return false
	}
}

func supportedDisplayTone(tone string) bool {
	switch tone {
	case "normal", "warning", "danger", "muted":
		return true
	default:
		return false
	}
}

func validateDisplayValue(kind string, raw json.RawMessage) error {
	switch kind {
	case "text", "status":
		var value any
		if err := decodeSingleJSON(raw, &value); err != nil {
			return err
		}
		if _, ok := value.(string); !ok {
			return errors.New("must be a string")
		}
		return nil
	case "integer", "duration_ms", "latency_ms":
		number, err := decodeJSONNumber(raw)
		if err != nil {
			return err
		}
		value, err := number.Float64()
		if err != nil || !finite(value) {
			return errors.New("must be a finite number")
		}
		if kind == "integer" && math.Trunc(value) != value {
			return errors.New("must be an integer")
		}
		return nil
	case "timestamp":
		var value string
		if err := decodeSingleJSON(raw, &value); err != nil {
			return err
		}
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			return err
		}
		return nil
	case "coordinates":
		var object map[string]json.RawMessage
		if err := decodeSingleJSON(raw, &object); err != nil {
			return err
		}
		if len(object) != 2 {
			return errors.New("must contain exactly x and y")
		}
		for _, key := range []string{"x", "y"} {
			coordinate, ok := object[key]
			if !ok {
				return fmt.Errorf("missing %s", key)
			}
			number, err := decodeJSONNumber(coordinate)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			value, err := number.Float64()
			if err != nil || !finite(value) {
				return fmt.Errorf("%s must be finite", key)
			}
		}
		return nil
	default:
		return errors.New("unsupported kind")
	}
}

func decodeJSONNumber(raw json.RawMessage) (json.Number, error) {
	var value any
	if err := decodeSingleJSON(raw, &value); err != nil {
		return "", err
	}
	number, ok := value.(json.Number)
	if !ok {
		return "", errors.New("must be a number")
	}
	if _, err := strconv.ParseFloat(number.String(), 64); err != nil {
		return "", errors.New("must be a number")
	}
	return number, nil
}

func decodeSingleJSON(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("must contain one JSON value")
	}
	return nil
}
