package archive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func truePtr() *bool { b := true; return &b }

// sampleRecord returns a fully-populated, valid Record suitable for
// round-trip and validation tests. One successful capture + one
// failed capture, representative of a real orchestrator output.
func sampleRecord() *Record {
	return &Record{
		Schema:     Schema,
		Subject:    "6327282:5d1cf5",
		URL:        "https://example.com/article",
		PolicyID:   "starred-default",
		CapturedAt: FormatTimestamp(time.Date(2026, 4, 19, 12, 0, 0, 412_000_000, time.UTC)),
		Captures: []Capture{
			{
				Name:     "pdf-clean",
				Spec:     &ArtifactRef{ID: "blake2b256-aaa", Size: 1287, MediaType: "application/vnd.web-capture-archive.spec+json"},
				Payload:  &ArtifactRef{ID: "blake2b256-bbb", Size: 842103, MediaType: "application/pdf", Normalized: truePtr()},
				Envelope: &ArtifactRef{ID: "blake2b256-ccc", Size: 892, MediaType: "application/vnd.web-capture-archive.envelope+json"},
			},
			{
				Name:  "text",
				Error: &Error{Kind: "fetch-failed", Message: "connection reset"},
			},
		},
		Errors: []Error{},
	}
}

func TestFormatTimestamp_millisecondPrecisionUTC(t *testing.T) {
	tm := time.Date(2026, 4, 19, 12, 0, 0, 412_000_000, time.UTC)
	got := FormatTimestamp(tm)
	want := "2026-04-19T12:00:00.412Z"
	if got != want {
		t.Errorf("FormatTimestamp: got %q, want %q", got, want)
	}

	// Nanosecond precision is truncated to ms per RFC 0001.
	tm2 := time.Date(2026, 4, 19, 12, 0, 0, 412_345_678, time.UTC)
	got2 := FormatTimestamp(tm2)
	if got2 != want {
		t.Errorf("FormatTimestamp truncation: got %q, want %q", got2, want)
	}

	// Non-UTC input gets converted.
	offset := time.FixedZone("+01:00", 3600)
	tm3 := time.Date(2026, 4, 19, 13, 0, 0, 412_000_000, offset)
	got3 := FormatTimestamp(tm3)
	if got3 != want {
		t.Errorf("FormatTimestamp tz conversion: got %q, want %q", got3, want)
	}
}

func TestParseTimestamp_roundtrip(t *testing.T) {
	s := "2026-04-19T12:00:00.412Z"
	tm, err := ParseTimestamp(s)
	if err != nil {
		t.Fatal(err)
	}
	if FormatTimestamp(tm) != s {
		t.Errorf("roundtrip: %q -> %v -> %q", s, tm, FormatTimestamp(tm))
	}
}

func TestValidate_accepts(t *testing.T) {
	if err := sampleRecord().Validate(); err != nil {
		t.Errorf("sample record should validate: %v", err)
	}
}

func TestValidate_rejectsMissingFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Record)
		wantSub string
	}{
		{"wrong-schema", func(r *Record) { r.Schema = "other/v1" }, "schema"},
		{"empty-subject", func(r *Record) { r.Subject = "" }, "subject"},
		{"empty-url", func(r *Record) { r.URL = "" }, "url"},
		{"empty-policy-id", func(r *Record) { r.PolicyID = "" }, "policy_id"},
		{"empty-captured-at", func(r *Record) { r.CapturedAt = "" }, "captured_at"},
		{"bad-captured-at", func(r *Record) { r.CapturedAt = "not-a-timestamp" }, "RFC 3339"},
		{"nil-errors", func(r *Record) { r.Errors = nil }, "errors must be at least"},
		{"nil-captures", func(r *Record) { r.Captures = nil }, "captures must be at least"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := sampleRecord()
			c.mutate(r)
			err := r.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantSub)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), c.wantSub)
			}
		})
	}
}

func TestValidate_rejectsDuplicateCaptureNames(t *testing.T) {
	r := sampleRecord()
	r.Captures = append(r.Captures, Capture{
		Name:    "pdf-clean",
		Spec:    &ArtifactRef{ID: "blake2b256-x", Size: 1, MediaType: "application/json"},
		Payload: &ArtifactRef{ID: "blake2b256-y", Size: 1, MediaType: "application/json"},
	})
	err := r.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Errorf("expected duplicated-name error, got %v", err)
	}
}

func TestValidate_rejectsMutuallyExclusiveStates(t *testing.T) {
	r := sampleRecord()
	// Capture with both artifact refs AND an error.
	r.Captures[0].Error = &Error{Kind: "mixed", Message: "both present"}
	err := r.Validate()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually-exclusive error, got %v", err)
	}

	// Capture with neither.
	r2 := sampleRecord()
	r2.Captures[0] = Capture{Name: "empty"}
	err = r2.Validate()
	if err == nil || !strings.Contains(err.Error(), "neither") {
		t.Errorf("expected 'neither' error, got %v", err)
	}
}

func TestValidate_rejectsSuccessMissingSpecOrPayload(t *testing.T) {
	r := sampleRecord()
	r.Captures[0].Spec = nil
	err := r.Validate()
	if err == nil || !strings.Contains(err.Error(), "missing spec") {
		t.Errorf("expected missing-spec error, got %v", err)
	}

	r2 := sampleRecord()
	r2.Captures[0].Payload = nil
	err = r2.Validate()
	if err == nil || !strings.Contains(err.Error(), "missing payload") {
		t.Errorf("expected missing-payload error, got %v", err)
	}
}

func TestValidate_rejectsErrorMissingFields(t *testing.T) {
	r := sampleRecord()
	r.Captures[1].Error = &Error{Kind: "", Message: "msg"}
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Errorf("expected kind-required error, got %v", err)
	}
	r2 := sampleRecord()
	r2.Captures[1].Error = &Error{Kind: "fail", Message: ""}
	if err := r2.Validate(); err == nil || !strings.Contains(err.Error(), "message") {
		t.Errorf("expected message-required error, got %v", err)
	}
}

func TestWrite_happyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archives", "subject-1", "policy-1.json")

	if err := Write(path, sampleRecord()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Error("file should not be empty")
	}

	// Roundtrip via Read.
	r2, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if r2.Subject != "6327282:5d1cf5" {
		t.Errorf("subject: got %q", r2.Subject)
	}
	if len(r2.Captures) != 2 {
		t.Errorf("captures: got %d, want 2", len(r2.Captures))
	}
	if r2.Captures[0].Payload.Normalized == nil || *r2.Captures[0].Payload.Normalized != true {
		t.Errorf("normalized bool should survive roundtrip")
	}
}

func TestWrite_createsParentDirectories(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c", "d.json")
	if err := Write(deep, sampleRecord()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(deep); err != nil {
		t.Errorf("Write should have created parent dirs: %v", err)
	}
}

func TestWrite_atomicityNoTempLeakOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "only.json")
	if err := Write(path, sampleRecord()); err != nil {
		t.Fatal(err)
	}
	// Only the final file should remain; no .tmp siblings.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected 1 file after Write, got %d: %v", len(entries), names)
	}
}

func TestWrite_rejectsInvalidRecordWithoutTouchingFS(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")

	bad := sampleRecord()
	bad.Subject = ""

	err := Write(path, bad)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("file should not have been created on validation failure, stat err: %v", statErr)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("no tempfile should linger, found %d entries", len(entries))
	}
}

func TestWrite_overwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.json")

	r1 := sampleRecord()
	if err := Write(path, r1); err != nil {
		t.Fatal(err)
	}
	r2 := sampleRecord()
	r2.Subject = "different-subject"
	if err := Write(path, r2); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Subject != "different-subject" {
		t.Errorf("overwrite failed: got %q", reloaded.Subject)
	}
}

func TestWrite_outputIsValidRFC0001JSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.json")
	if err := Write(path, sampleRecord()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if raw["schema"] != Schema {
		t.Errorf("schema: got %v, want %q", raw["schema"], Schema)
	}
	// Per § Archive Record Format, errors MUST be at least [] not null.
	if raw["errors"] == nil {
		t.Error("errors should serialize as [] not null")
	}
}

func TestDefaultPath(t *testing.T) {
	got := DefaultPath("/var/data", "story-42", "starred-default")
	want := filepath.Join("/var/data", "archives", "story-42", "starred-default.json")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
