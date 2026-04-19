package jcs

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCanonicalize_basicValues(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"null", nil, `null`},
		{"true", true, `true`},
		{"false", false, `false`},
		{"empty-string", "", `""`},
		{"ascii-string", "hello", `"hello"`},
		{"int-zero", 0, `0`},
		{"int-neg", -42, `-42`},
		{"int64", int64(1 << 40), `1099511627776`},
		{"empty-array", []any{}, `[]`},
		{"empty-object", map[string]any{}, `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Canonicalize(c.in)
			if err != nil {
				t.Fatalf("Canonicalize(%v): %v", c.in, err)
			}
			if string(got) != c.want {
				t.Errorf("Canonicalize(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// RFC 8785 §3.2.3 example: keys must be sorted by UTF-16 code units.
// The canonical form is stable regardless of input insertion order.
func TestCanonicalize_objectKeySortingAgainstInputOrder(t *testing.T) {
	want := `{"a":1,"b":2,"c":3}`

	orders := []map[string]any{
		{"a": 1, "b": 2, "c": 3},
		{"c": 3, "b": 2, "a": 1},
		{"b": 2, "a": 1, "c": 3},
	}
	for i, in := range orders {
		got, err := Canonicalize(in)
		if err != nil {
			t.Fatalf("order %d: %v", i, err)
		}
		if string(got) != want {
			t.Errorf("order %d: got %q, want %q", i, got, want)
		}
	}
}

// Nested objects and arrays round-trip the same canonical form
// regardless of how the input was constructed.
func TestCanonicalize_nestedInvariance(t *testing.T) {
	raw1 := []byte(`{"outer":{"b":2,"a":1},"list":[3,1,2]}`)
	raw2 := []byte(`{"list":[3,1,2],"outer":{"a":1,"b":2}}`)

	out1, err := CanonicalizeJSON(raw1)
	if err != nil {
		t.Fatalf("raw1: %v", err)
	}
	out2, err := CanonicalizeJSON(raw2)
	if err != nil {
		t.Fatalf("raw2: %v", err)
	}
	if string(out1) != string(out2) {
		t.Errorf("canonical forms disagree:\n  raw1 → %s\n  raw2 → %s", out1, out2)
	}

	// Arrays preserve order (unlike objects).
	want := `{"list":[3,1,2],"outer":{"a":1,"b":2}}`
	if string(out1) != want {
		t.Errorf("got %s, want %s", out1, want)
	}
}

// String escape rules per RFC 8785 §3.2.2.2: only `"`, `\`, and the
// named control escapes (`\b`, `\f`, `\n`, `\r`, `\t`) are shortened;
// remaining C0 controls become `\uXXXX` in lowercase hex; all other
// characters pass through as UTF-8 bytes.
func TestCanonicalize_stringEscaping(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"quote", `he said "hi"`, `"he said \"hi\""`},
		{"backslash", `a\b`, `"a\\b"`},
		{"newline", "line1\nline2", `"line1\nline2"`},
		{"tab", "a\tb", `"a\tb"`},
		{"bell-is-hex", "\x07", `"\u0007"`},
		{"unicode-passes-through", "café", `"café"`},
		{"angle-brackets-unescaped", "<html>", `"<html>"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Canonicalize(c.in)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if string(got) != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestCanonicalize_rejectsFloats(t *testing.T) {
	if _, err := Canonicalize(1.5); err == nil {
		t.Errorf("expected error for float64, got nil")
	}
	if _, err := Canonicalize(float32(1.5)); err == nil {
		t.Errorf("expected error for float32, got nil")
	}
}

func TestCanonicalizeJSON_rejectsFloatNumbers(t *testing.T) {
	_, err := CanonicalizeJSON([]byte(`{"x": 1.5}`))
	if err == nil {
		t.Errorf("expected error on non-integer number, got nil")
	}
	if !strings.Contains(err.Error(), "non-integer") {
		t.Errorf("error message should mention non-integer, got: %v", err)
	}
}

func TestCanonicalizeJSON_jsonNumberIntegerPath(t *testing.T) {
	got, err := CanonicalizeJSON([]byte(`  { "x": 42 }  `))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"x":42}` {
		t.Errorf("got %q, want %q", got, `{"x":42}`)
	}
}

// Sanity check that a spec-shaped document (matching RFC 0001's
// capture spec artifact) canonicalizes to a deterministic byte string.
func TestCanonicalize_specShapedDocument(t *testing.T) {
	raw := []byte(`{
  "schema": "web-capture-archive.spec/v1",
  "capture": {
    "format": "text",
    "options": {},
    "isolation": "fresh",
    "split": false
  },
  "browser": {
    "name": "firefox",
    "version": "149.0.2",
    "extensions": []
  },
  "host": {
    "os": "linux",
    "kernel": "6.17.0-20-generic"
  },
  "capturer": {
    "name": "chrest",
    "version": "0.0.1"
  }
}`)

	got, err := CanonicalizeJSON(raw)
	if err != nil {
		t.Fatalf("%v", err)
	}

	// Verify top-level keys appear in UTF-16 code-unit order:
	// browser, capture, capturer, host, schema.
	want := `{"browser":{"extensions":[],"name":"firefox","version":"149.0.2"},"capture":{"format":"text","isolation":"fresh","options":{},"split":false},"capturer":{"name":"chrest","version":"0.0.1"},"host":{"kernel":"6.17.0-20-generic","os":"linux"},"schema":"web-capture-archive.spec/v1"}`

	if string(got) != want {
		t.Errorf("spec canonicalization mismatch:\n  got:  %s\n  want: %s", got, want)
	}

	// Double-check the output round-trips back to the same struct.
	var reparsed map[string]any
	dec := json.NewDecoder(strings.NewReader(string(got)))
	dec.UseNumber()
	if err := dec.Decode(&reparsed); err != nil {
		t.Fatalf("canonical bytes should round-trip: %v", err)
	}
}
