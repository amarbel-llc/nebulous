package capture

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseReceiptFromStdoutFindsReceiptPhase(t *testing.T) {
	stdout := []byte(
		`{"type":"test","ok":true,"description":"setup","diagnostic":{}}` + "\n" +
			`{"type":"test","ok":true,"description":"receipt store=nebulous","diagnostic":{"store":"nebulous","receipt_id":"blake2b256-abc","count":1}}` + "\n" +
			`{"type":"summary","passed":2,"failed":0}` + "\n",
	)

	receiptID, hasFailures, ok, err := parseReceiptFromStdout(stdout)
	if err != nil {
		t.Fatalf("parseReceiptFromStdout: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if hasFailures {
		t.Error("hasFailures = true, want false")
	}
	if receiptID != "blake2b256-abc" {
		t.Errorf("receiptID = %q, want %q", receiptID, "blake2b256-abc")
	}
}

func TestParseReceiptFromStdoutPicksLastReceiptPhase(t *testing.T) {
	// A single capture invocation only ever emits one receipt phase per
	// store, but the parser should reflect the last one seen rather than
	// the first, matching NDJSON's append-only stream semantics.
	stdout := []byte(
		`{"type":"test","ok":true,"description":"receipt store=nebulous","diagnostic":{"receipt_id":"blake2b256-first"}}` + "\n" +
			`{"type":"test","ok":true,"description":"receipt store=nebulous","diagnostic":{"receipt_id":"blake2b256-second"}}` + "\n",
	)

	receiptID, _, ok, err := parseReceiptFromStdout(stdout)
	if err != nil {
		t.Fatalf("parseReceiptFromStdout: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if receiptID != "blake2b256-second" {
		t.Errorf("receiptID = %q, want the last %q", receiptID, "blake2b256-second")
	}
}

func TestParseReceiptFromStdoutDetectsFailuresPhase(t *testing.T) {
	stdout := []byte(
		`{"type":"test","ok":true,"description":"receipt store=nebulous","diagnostic":{"receipt_id":"blake2b256-abc"}}` + "\n" +
			`{"type":"test","ok":true,"description":"failures store=nebulous","diagnostic":{"id":"blake2b256-fail","count":1}}` + "\n",
	)

	receiptID, hasFailures, ok, err := parseReceiptFromStdout(stdout)
	if err != nil {
		t.Fatalf("parseReceiptFromStdout: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if !hasFailures {
		t.Error("hasFailures = false, want true")
	}
	if receiptID != "blake2b256-abc" {
		t.Errorf("receiptID = %q, want %q", receiptID, "blake2b256-abc")
	}
}

// A "receipt store=..." phase with ok=false (never emitted by
// cutting-garden's reportReceipt today, but a receipt phase's own OK
// field is meaningful in the tap-ndjson schema) must not be accepted
// as a clean receipt.
func TestParseReceiptFromStdoutRejectsNotOKReceiptPhase(t *testing.T) {
	stdout := []byte(
		`{"type":"test","ok":false,"description":"receipt store=nebulous","diagnostic":{"receipt_id":"blake2b256-abc"}}` + "\n",
	)

	_, hasFailures, ok, err := parseReceiptFromStdout(stdout)
	if err != nil {
		t.Fatalf("parseReceiptFromStdout: %v", err)
	}
	if ok {
		t.Error("ok = true for a not-OK receipt phase, want false")
	}
	if hasFailures {
		t.Error("hasFailures = true, want false (no failures phase present)")
	}
}

func TestParseReceiptFromStdoutNoReceiptPhaseIsOkFalse(t *testing.T) {
	stdout := []byte(
		`{"type":"test","ok":true,"description":"setup","diagnostic":{}}` + "\n" +
			`{"type":"summary","passed":1,"failed":0}` + "\n",
	)

	_, _, ok, err := parseReceiptFromStdout(stdout)
	if err != nil {
		t.Fatalf("parseReceiptFromStdout: %v", err)
	}
	if ok {
		t.Error("ok = true with no receipt phase present, want false")
	}
}

func TestParseReceiptFromStdoutIgnoresMalformedLines(t *testing.T) {
	stdout := []byte(
		"not json at all\n" +
			`{"type":"test","ok":true,"description":"receipt store=nebulous","diagnostic":{"receipt_id":"blake2b256-abc"}}` + "\n",
	)

	receiptID, _, ok, err := parseReceiptFromStdout(stdout)
	if err != nil {
		t.Fatalf("parseReceiptFromStdout: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if receiptID != "blake2b256-abc" {
		t.Errorf("receiptID = %q, want %q", receiptID, "blake2b256-abc")
	}
}

func TestParseReceiptFromStdoutEmptyIsOkFalse(t *testing.T) {
	_, _, ok, err := parseReceiptFromStdout(nil)
	if err != nil {
		t.Fatalf("parseReceiptFromStdout: %v", err)
	}
	if ok {
		t.Error("ok = true for empty stdout, want false")
	}
}

// A line past the scanner's buffer cap is a real parse failure, not a
// silent "no receipt phase" miss — err must be non-nil so the caller
// can tell the two apart.
func TestParseReceiptFromStdoutOversizedLineIsError(t *testing.T) {
	huge := `{"type":"test","ok":true,"description":"receipt store=nebulous","diagnostic":{"receipt_id":"` +
		strings.Repeat("x", 2*1024*1024) + `"}}`

	_, _, _, err := parseReceiptFromStdout([]byte(huge))
	if err == nil {
		t.Fatal("err = nil for an oversized line, want a scanner error")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("token too long")) {
		t.Errorf("err = %v, want a bufio.Scanner 'token too long' error", err)
	}
}
