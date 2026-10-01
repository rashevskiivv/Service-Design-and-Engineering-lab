package oai

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

// MaxDepth is the maximum JSON nesting depth accepted in any request body
// (security §5.3).
const MaxDepth = 32

// DepthOK reports whether no object or array in b nests deeper than limit. It
// is a cheap pre-scan that runs before encoding/json and does not validate
// syntax.
func DepthOK(b []byte, limit int) bool {
	depth, inStr, esc := 0, false, false
	for _, c := range b {
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			depth++
			if depth > limit {
				return false
			}
		case '}', ']':
			depth--
		}
	}
	return true
}

// fields is a JSON object decoded with exact-case keys. encoding/json matches
// struct fields case-insensitively; a map does not, so "MAX_TOKENS" can never
// alias "max_tokens".
type fields map[string]json.RawMessage

// decodeObject decodes raw as a JSON object. It fails for anything else,
// including null.
func decodeObject(raw json.RawMessage) (fields, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	var f fields
	if json.Unmarshal(raw, &f) != nil {
		return nil, false
	}
	return f, true
}

// get returns the value of key; a JSON null counts as absent.
func (f fields) get(key string) (json.RawMessage, bool) {
	v, ok := f[key]
	if !ok || isNull(v) {
		return nil, false
	}
	return v, true
}

func isNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

func decodeString(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	return s, json.Unmarshal(raw, &s) == nil
}

func decodeBool(raw json.RawMessage) (bool, bool) {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

func decodeArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var a []json.RawMessage
	return a, json.Unmarshal(raw, &a) == nil
}

func decodeNumber(raw json.RawMessage) (float64, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) {
		return 0, false
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// ParseInt decodes a JSON number that has an integral value. Integral values
// written with a fraction or exponent ("1.0", "1e9") are accepted; values
// outside int64 saturate. Non-integral numbers and non-numbers fail.
func ParseInt(raw json.RawMessage) (int64, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) || !json.Valid(raw) {
		return 0, false
	}
	s := string(raw)
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	switch {
	case f >= math.MaxInt64:
		return math.MaxInt64, true
	case f <= math.MinInt64:
		return math.MinInt64, true
	case f != math.Trunc(f):
		return 0, false
	}
	return int64(f), true
}
