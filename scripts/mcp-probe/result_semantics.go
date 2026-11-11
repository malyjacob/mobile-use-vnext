package main

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"
	"math"
	"math/big"
	"strconv"
)

// This file holds the probe's own JSON semantics: an exact decoder that keeps
// number literals instead of rounding them through float64, and a structural
// comparison that ignores object member order while preserving array order and
// numeric value. The probe never compares source fingerprints with the bytes
// an SDK produced from a re-serialized tools/list response; it compares meaning
// and only then relies on the source fingerprint.

var (
	errJSONInvalid     = errors.New("json invalid")
	errJSONTrailing    = errors.New("json trailing value")
	errJSONDuplicate   = errors.New("json duplicate member")
	errJSONScalarShape = errors.New("json scalar shape")
)

// decodeExactJSON decodes exactly one JSON value. Object member order is not
// significant; array order and the number literal are preserved as
// encoding/json.Number.
func decodeExactJSON(data []byte) (any, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	value, err := readExactJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, errJSONTrailing
	}
	return value, nil
}

// readExactJSONValue reads one token tree. Duplicate object names are rejected
// so a readable projection can never be resolved by last-member-wins.
func readExactJSONValue(decoder *jsontext.Decoder) (any, error) {
	switch decoder.PeekKind() {
	case '{':
		if _, err := decoder.ReadToken(); err != nil {
			return nil, err
		}
		object := make(map[string]any)
		for decoder.PeekKind() != '}' {
			name, err := decoder.ReadToken()
			if err != nil {
				return nil, err
			}
			if name.Kind() != '"' {
				return nil, errJSONInvalid
			}
			key := name.String()
			if _, duplicate := object[key]; duplicate {
				return nil, errJSONDuplicate
			}
			value, err := readExactJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if _, err := decoder.ReadToken(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		if _, err := decoder.ReadToken(); err != nil {
			return nil, err
		}
		array := make([]any, 0)
		for decoder.PeekKind() != ']' {
			value, err := readExactJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if _, err := decoder.ReadToken(); err != nil {
			return nil, err
		}
		return array, nil
	case '"':
		token, err := decoder.ReadToken()
		if err != nil {
			return nil, err
		}
		return token.String(), nil
	case 't', 'f':
		token, err := decoder.ReadToken()
		if err != nil {
			return nil, err
		}
		if token.Kind() != 't' && token.Kind() != 'f' {
			return nil, errJSONScalarShape
		}
		return token.Bool(), nil
	case 'n':
		if _, err := decoder.ReadToken(); err != nil {
			return nil, err
		}
		return nil, nil
	default:
		token, err := decoder.ReadToken()
		if err != nil {
			return nil, err
		}
		if token.Kind() != '0' {
			return nil, errJSONScalarShape
		}
		return json.Number(token.String()), nil
	}
}

// semanticEqual reports whether two decoded JSON values mean the same thing.
// Object member order is ignored, array order is significant, and numbers are
// compared by exact rational value rather than float64 representation.
func semanticEqual(left, right any) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	switch leftValue := left.(type) {
	case map[string]any:
		rightValue, ok := right.(map[string]any)
		if !ok || len(leftValue) != len(rightValue) {
			return false
		}
		for key, value := range leftValue {
			other, exists := rightValue[key]
			if !exists || !semanticEqual(value, other) {
				return false
			}
		}
		return true
	case []any:
		rightValue, ok := right.([]any)
		if !ok || len(leftValue) != len(rightValue) {
			return false
		}
		for index := range leftValue {
			if !semanticEqual(leftValue[index], rightValue[index]) {
				return false
			}
		}
		return true
	case string:
		rightValue, ok := right.(string)
		return ok && leftValue == rightValue
	case bool:
		rightValue, ok := right.(bool)
		return ok && leftValue == rightValue
	default:
		leftNumber, leftOK := numberRat(left)
		rightNumber, rightOK := numberRat(right)
		return leftOK && rightOK && leftNumber.Cmp(rightNumber) == 0
	}
}

// numberRat converts every number representation the probe can meet into an
// exact rational. float64 input is accepted only as the exact value it already
// represents; it cannot recover precision lost before it reached the probe.
func numberRat(value any) (*big.Rat, bool) {
	switch number := value.(type) {
	case json.Number:
		rational, ok := new(big.Rat).SetString(string(number))
		return rational, ok
	case int:
		return new(big.Rat).SetInt64(int64(number)), true
	case int64:
		return new(big.Rat).SetInt64(number), true
	case uint64:
		return new(big.Rat).SetUint64(number), true
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, false
		}
		rational, ok := new(big.Rat).SetString(strconv.FormatFloat(number, 'g', -1, 64))
		return rational, ok
	default:
		return nil, false
	}
}

// jsonStringField is a small helper for exact-decoded objects.
func jsonStringField(object map[string]any, name string) (string, bool) {
	value, found := object[name]
	if !found {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

// exactObject decodes data and requires an object at the top level.
func exactObject(data []byte) (map[string]any, error) {
	value, err := decodeExactJSON(data)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errJSONInvalid
	}
	return object, nil
}
