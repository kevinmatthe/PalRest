package observation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
)

func canonicalDocument(value any) ([]byte, string, error) {
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, "", fmt.Errorf("encode canonical document: %w", err)
	}
	return canonical, documentHash(canonical), nil
}

func canonicalSettings(values map[string]any) ([]byte, string, error) {
	normalized, err := canonicalJSONValue(values)
	if err != nil {
		return nil, "", err
	}
	object, ok := normalized.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("settings must be a top-level object")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, "", fmt.Errorf("encode canonical settings: %w", err)
	}
	return canonical, documentHash(canonical), nil
}

func canonicalJSONValue(value any) (any, error) {
	switch value := value.(type) {
	case nil, bool, string:
		return value, nil
	case json.Number:
		number, err := value.Float64()
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fmt.Errorf("settings contain an invalid number")
		}
		return number, nil
	case float32:
		return canonicalFloat(float64(value))
	case float64:
		return canonicalFloat(value)
	case int:
		return float64(value), nil
	case int8:
		return float64(value), nil
	case int16:
		return float64(value), nil
	case int32:
		return float64(value), nil
	case int64:
		return float64(value), nil
	case uint:
		return float64(value), nil
	case uint8:
		return float64(value), nil
	case uint16:
		return float64(value), nil
	case uint32:
		return float64(value), nil
	case uint64:
		return float64(value), nil
	case []any:
		result := make([]any, len(value))
		for index, child := range value {
			normalized, err := canonicalJSONValue(child)
			if err != nil {
				return nil, err
			}
			result[index] = normalized
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, child := range value {
			normalized, err := canonicalJSONValue(child)
			if err != nil {
				return nil, err
			}
			result[key] = normalized
		}
		return result, nil
	default:
		return nil, fmt.Errorf("settings contain unsupported value %T", value)
	}
}

func canonicalFloat(value float64) (float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("settings contain a non-finite number")
	}
	return value, nil
}

func documentHash(canonical []byte) string {
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:])
}
