package observation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
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
		return canonicalNumber(value.String())
	case float32:
		return canonicalFloat(float64(value), 32)
	case float64:
		return canonicalFloat(value, 64)
	case int:
		return canonicalNumber(strconv.FormatInt(int64(value), 10))
	case int8:
		return canonicalNumber(strconv.FormatInt(int64(value), 10))
	case int16:
		return canonicalNumber(strconv.FormatInt(int64(value), 10))
	case int32:
		return canonicalNumber(strconv.FormatInt(int64(value), 10))
	case int64:
		return canonicalNumber(strconv.FormatInt(value, 10))
	case uint:
		return canonicalNumber(strconv.FormatUint(uint64(value), 10))
	case uint8:
		return canonicalNumber(strconv.FormatUint(uint64(value), 10))
	case uint16:
		return canonicalNumber(strconv.FormatUint(uint64(value), 10))
	case uint32:
		return canonicalNumber(strconv.FormatUint(uint64(value), 10))
	case uint64:
		return canonicalNumber(strconv.FormatUint(value, 10))
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

func canonicalFloat(value float64, bitSize int) (json.Number, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("settings contain a non-finite number")
	}
	return canonicalNumber(strconv.FormatFloat(value, 'g', -1, bitSize))
}

func canonicalNumber(value string) (json.Number, error) {
	rational, ok := new(big.Rat).SetString(value)
	if !ok {
		return "", fmt.Errorf("settings contain an invalid number")
	}
	if rational.Sign() == 0 {
		return json.Number("0"), nil
	}
	if rational.IsInt() {
		return json.Number(rational.Num().String()), nil
	}

	denominator := new(big.Int).Set(rational.Denom())
	twos := factorCount(denominator, 2)
	fives := factorCount(denominator, 5)
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return "", fmt.Errorf("settings number has no finite decimal representation")
	}
	scale := twos
	if fives > scale {
		scale = fives
	}
	scaled := new(big.Int).Set(rational.Num())
	if scale > twos {
		scaled.Mul(scaled, new(big.Int).Exp(big.NewInt(2), big.NewInt(int64(scale-twos)), nil))
	}
	if scale > fives {
		scaled.Mul(scaled, new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(scale-fives)), nil))
	}
	negative := scaled.Sign() < 0
	digits := new(big.Int).Abs(scaled).String()
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	integer := digits[:len(digits)-scale]
	fraction := strings.TrimRight(digits[len(digits)-scale:], "0")
	result := integer
	if fraction != "" {
		result += "." + fraction
	}
	if negative {
		result = "-" + result
	}
	return json.Number(result), nil
}

func factorCount(value *big.Int, factor int64) int {
	count := 0
	divisor := big.NewInt(factor)
	for {
		quotient, remainder := new(big.Int), new(big.Int)
		quotient.QuoRem(value, divisor, remainder)
		if remainder.Sign() != 0 {
			return count
		}
		value.Set(quotient)
		count++
	}
}

func documentHash(canonical []byte) string {
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:])
}
