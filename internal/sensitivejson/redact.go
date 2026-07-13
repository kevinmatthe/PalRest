package sensitivejson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

const Redacted = "[REDACTED]"

func Redact(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, child := range value {
			if secretKey(key) {
				result[key] = Redacted
			} else {
				result[key] = Redact(child)
			}
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, child := range value {
			result[index] = Redact(child)
		}
		return result
	default:
		return value
	}
}

func RedactJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode sensitive JSON: %w", err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("decode sensitive JSON: trailing content")
	}
	redacted, err := json.Marshal(Redact(value))
	if err != nil {
		return nil, fmt.Errorf("encode redacted JSON: %w", err)
	}
	return redacted, nil
}

func secretKey(key string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
	for _, marker := range []string{"password", "secret", "token", "apikey", "credential"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
