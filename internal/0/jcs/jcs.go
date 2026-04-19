// Package jcs canonicalizes JSON values per RFC 8785 (JSON
// Canonicalization Scheme).
//
// Scope: the integer-only JSON subset used by the Web Capture Archive
// Protocol (RFC 0001 in docs/rfcs/). Supports strings, booleans, nulls,
// arrays, objects, and integers (int, int64, json.Number parseable as
// int64). Floating-point numbers are rejected because the ES6
// ToString serialization required by RFC 8785 §3.2.2.3 is not needed
// for any schema this consumer canonicalizes.
//
// Implementation notes:
//
//   - Object keys are sorted by UTF-16 code unit sequence per §3.2.3.
//     For ASCII-only keys this coincides with lexicographic byte order.
//   - Strings escape only the characters RFC 8785 §3.2.2.2 requires
//     (`"`, `\`, and the C0 control block). All other characters are
//     passed through as their UTF-8 bytes. This is stricter than Go's
//     encoding/json default, which escapes `<`, `>`, `&`, and line
//     separators.
//   - Output contains no insignificant whitespace.
//
// The canonicalizer's contract is: two semantically-equal inputs
// produce byte-identical outputs.
package jcs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf16"
)

// Canonicalize returns the RFC 8785 canonical form of v.
//
// Accepted input types (recursively):
//
//   - nil (emits `null`)
//   - bool
//   - string
//   - int, int64, json.Number (must parse as int64)
//   - []any
//   - map[string]any
//
// Any other type causes an error. In particular, float32/float64 and
// json.Number values that do not parse as int64 are rejected.
func Canonicalize(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := encode(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CanonicalizeJSON parses raw JSON bytes (using json.Number for numeric
// values) and returns the canonicalized form.
func CanonicalizeJSON(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("jcs: decode: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("jcs: decode: trailing data after value")
	}
	return Canonicalize(v)
}

func encode(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		encodeString(buf, x)
	case int:
		buf.WriteString(strconv.FormatInt(int64(x), 10))
	case int64:
		buf.WriteString(strconv.FormatInt(x, 10))
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			return fmt.Errorf("jcs: non-integer number %q unsupported", x.String())
		}
		buf.WriteString(strconv.FormatInt(n, 10))
	case []any:
		return encodeArray(buf, x)
	case map[string]any:
		return encodeObject(buf, x)
	default:
		return fmt.Errorf("jcs: unsupported type %T", v)
	}
	return nil
}

func encodeArray(buf *bytes.Buffer, arr []any) error {
	buf.WriteByte('[')
	for i, item := range arr {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := encode(buf, item); err != nil {
			return err
		}
	}
	buf.WriteByte(']')
	return nil
}

func encodeObject(buf *bytes.Buffer, obj map[string]any) error {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sortKeysUTF16(keys)

	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		encodeString(buf, k)
		buf.WriteByte(':')
		if err := encode(buf, obj[k]); err != nil {
			return err
		}
	}
	buf.WriteByte('}')
	return nil
}

// sortKeysUTF16 sorts keys by UTF-16 code unit sequence (RFC 8785 §3.2.3).
func sortKeysUTF16(keys []string) {
	sort.Slice(keys, func(i, j int) bool {
		ai, bi := utf16.Encode([]rune(keys[i])), utf16.Encode([]rune(keys[j]))
		n := len(ai)
		if len(bi) < n {
			n = len(bi)
		}
		for k := 0; k < n; k++ {
			if ai[k] != bi[k] {
				return ai[k] < bi[k]
			}
		}
		return len(ai) < len(bi)
	})
}

// encodeString writes s as a JCS-compliant JSON string literal.
func encodeString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				// Remaining C0 controls. RFC 8785 §3.2.2.2 requires
				// lowercase hex in \uXXXX.
				fmt.Fprintf(buf, `\u%04x`, r)
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}
