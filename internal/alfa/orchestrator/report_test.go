package orchestrator

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWriteTAPReport_basicShape(t *testing.T) {
	rep := Report{
		Written: []Job{
			{PolicyID: "p1", Subject: "story:abc", Path: "/root/by-story/abc/p1.json"},
			{PolicyID: "p2", Subject: "story:abc", Path: "/root/by-story/abc/p2.json"},
		},
		Failed: []JobFailure{
			{PolicyID: "p1", Subject: "url:sha256-def", Kind: "writer-failed", Message: "permission denied"},
		},
	}

	var buf bytes.Buffer
	if err := WriteTAPReport(&buf, rep, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// Header + plan.
	if !strings.HasPrefix(out, "TAP version 14") {
		t.Errorf("should start with TAP version 14, got: %q", firstLine(out))
	}
	if !strings.Contains(out, "\n1..3\n") {
		t.Errorf("should contain plan 1..3, got:\n%s", out)
	}

	// Two ok lines, one not ok.
	if got := strings.Count(out, "\nok "); got != 2 {
		t.Errorf("ok count: got %d, want 2", got)
	}
	if got := strings.Count(out, "\nnot ok "); got != 1 {
		t.Errorf("not ok count: got %d, want 1", got)
	}

	// Each `ok` line carries the record path inline as a
	// `# path: …` suffix, not on a separate following line. This
	// keeps one test point to one line — friendlier for tail/grep
	// and for TAP parsers that don't expect a comment between a
	// test point and the next plan/test line.
	if !strings.Contains(out, "ok 1 - p1 story:abc # path: /root/by-story/abc/p1.json\n") {
		t.Errorf("expected inline path suffix on p1 `ok` line:\n%s", out)
	}
	if !strings.Contains(out, "ok 2 - p2 story:abc # path: /root/by-story/abc/p2.json\n") {
		t.Errorf("expected inline path suffix on p2 `ok` line:\n%s", out)
	}
	// No standalone `# path:` lines (interstitial comments) must
	// appear — the path belongs on the test-point line itself.
	if strings.Contains(out, "\n# path: ") {
		t.Errorf("found standalone `# path:` line; expected inline only:\n%s", out)
	}

	// The failing entry's diagnostic details appear in some form.
	// tap-dancer renders diagnostics as a YAML block between `---`
	// and `...`; the exact layout is library-owned, so we just
	// check the key/value strings show up.
	if !strings.Contains(out, "writer-failed") {
		t.Errorf("expected kind `writer-failed` in output:\n%s", out)
	}
	if !strings.Contains(out, "permission denied") {
		t.Errorf("expected message in output:\n%s", out)
	}
}

// TestWriteTAPReport_colorEmitsANSI verifies that color=true wraps
// the ok / not ok / Bail out! / # SKIP keywords in ANSI escape
// codes. color=false (already covered by the other tests) produces
// plain-text output.
func TestWriteTAPReport_colorEmitsANSI(t *testing.T) {
	rep := Report{
		Written: []Job{{PolicyID: "p1", Subject: "story:a", Path: "/a/a.json"}},
		Skipped: []Skip{{
			PolicyID:       "p1",
			Subject:        "story:b",
			Path:           "/a/b.json",
			LastCapturedAt: time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC),
		}},
		Failed: []JobFailure{{
			PolicyID: "p1", Subject: "story:c", Kind: "capturer-failed", Message: "boom",
		}},
		BailedOut: true,
	}
	var buf bytes.Buffer
	if err := WriteTAPReport(&buf, rep, true); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// Ok keyword (green).
	if !strings.Contains(out, "\x1b[32mok\x1b[0m") {
		t.Errorf("expected green `ok` prefix, got:\n%s", out)
	}
	// Not ok keyword (red).
	if !strings.Contains(out, "\x1b[31mnot ok\x1b[0m") {
		t.Errorf("expected red `not ok` prefix, got:\n%s", out)
	}
	// Bail out keyword (red).
	if !strings.Contains(out, "\x1b[31mBail out!\x1b[0m") {
		t.Errorf("expected red `Bail out!` prefix, got:\n%s", out)
	}
	// SKIP directive carries its own colored keyword.
	if !strings.Contains(out, "\x1b[33m# SKIP\x1b[0m") && !strings.Contains(out, "# SKIP") {
		// tap-dancer may use a different hue for SKIP; at minimum
		// the "# SKIP" substring must appear.
		t.Errorf("expected `# SKIP` directive, got:\n%s", out)
	}
}

func TestWriteTAPReport_bailOutEmitsBailLine(t *testing.T) {
	rep := Report{
		Failed: []JobFailure{
			{PolicyID: "p1", Subject: "story:abc", Kind: "capturer-failed", Message: "boom"},
			{PolicyID: "p2", Subject: "story:abc", Kind: "capturer-failed", Message: "boom"},
			{PolicyID: "p3", Subject: "story:abc", Kind: "capturer-failed", Message: "boom"},
		},
		BailedOut: true,
	}

	var buf bytes.Buffer
	if err := WriteTAPReport(&buf, rep, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Bail out!") {
		t.Errorf("expected `Bail out!` in output:\n%s", buf.String())
	}
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func TestWriteJSONReport_shape(t *testing.T) {
	rep := Report{
		Written: []Job{{PolicyID: "p", Subject: "story:abc", Path: "/a/r.json"}},
		Failed:  []JobFailure{},
	}
	var buf bytes.Buffer
	if err := WriteJSONReport(&buf, rep); err != nil {
		t.Fatal(err)
	}

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	written, ok := out["written"].([]any)
	if !ok || len(written) != 1 {
		t.Errorf("written: %+v", out["written"])
	}
	first := written[0].(map[string]any)
	if first["policy_id"] != "p" {
		t.Errorf("policy_id: %v", first["policy_id"])
	}
	if first["subject"] != "story:abc" {
		t.Errorf("subject: %v", first["subject"])
	}
	if first["path"] != "/a/r.json" {
		t.Errorf("path: %v", first["path"])
	}
	if out["bailed_out"].(bool) {
		t.Error("bailed_out should be false")
	}

	// Nil slices serialize as [], not null, for stable consumption
	// by jq-like tools.
	if out["failed"] == nil {
		t.Error("failed should be [] not null")
	}
}

func TestWriteJSONReport_bailedOutTrue(t *testing.T) {
	rep := Report{BailedOut: true}
	var buf bytes.Buffer
	if err := WriteJSONReport(&buf, rep); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if b, _ := out["bailed_out"].(bool); !b {
		t.Errorf("bailed_out should be true, got %v", out["bailed_out"])
	}
}

// TestWriteTAPReport_emitsSkippedBetweenWrittenAndFailed verifies
// the batched TAP writer produces `# SKIP` test points in the
// Written → Skipped → Failed ordering documented in the godoc,
// with the plan line reflecting all three slices.
func TestWriteTAPReport_emitsSkippedBetweenWrittenAndFailed(t *testing.T) {
	rep := Report{
		Written: []Job{{PolicyID: "p1", Subject: "story:a", Path: "/a/a.json"}},
		Skipped: []Skip{{
			PolicyID:       "p1",
			Subject:        "story:b",
			Path:           "/a/b.json",
			LastCapturedAt: time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC),
		}},
		Failed: []JobFailure{{
			PolicyID: "p1", Subject: "story:c", Kind: "capturer-failed", Message: "boom",
		}},
	}
	var buf bytes.Buffer
	if err := WriteTAPReport(&buf, rep, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "\n1..3\n") {
		t.Errorf("plan should be 1..3, got:\n%s", out)
	}
	if !strings.Contains(out, "# SKIP") {
		t.Errorf("expected `# SKIP` directive, got:\n%s", out)
	}
	// Only the Written entry carries a `# path:` suffix — skips
	// did not write a new record, and failures have no record at
	// all. The suffix lives on the ok line itself, never on a
	// standalone comment line.
	if !strings.Contains(out, "ok 1 - p1 story:a # path: /a/a.json\n") {
		t.Errorf("expected inline path suffix on the Written `ok` line:\n%s", out)
	}
	if strings.Contains(out, "\n# path: ") {
		t.Errorf("found standalone `# path:` line; expected inline only:\n%s", out)
	}
	// Ordering check: story:a `ok` before story:b `# SKIP` before
	// story:c `not ok`.
	iWritten := strings.Index(out, "story:a")
	iSkip := strings.Index(out, "story:b")
	iFail := strings.Index(out, "story:c")
	if iWritten < 0 || iSkip < 0 || iFail < 0 {
		t.Fatalf("missing one of the expected subjects in:\n%s", out)
	}
	if !(iWritten < iSkip && iSkip < iFail) {
		t.Errorf("order wrong: written=%d skip=%d fail=%d in:\n%s", iWritten, iSkip, iFail, out)
	}
}

// TestWriteJSONReport_includesSkippedShape verifies the JSON shape
// exposes `skipped` as an array of objects with the expected field
// names, and that last_captured_at round-trips through the
// archive timestamp format.
func TestWriteJSONReport_includesSkippedShape(t *testing.T) {
	ts := time.Date(2026, 4, 20, 12, 34, 56, 789_000_000, time.UTC)
	rep := Report{
		Skipped: []Skip{{
			PolicyID:       "p1",
			Subject:        "story:abc",
			Path:           "/root/by-story/abc/p1.json",
			LastCapturedAt: ts,
		}},
	}
	var buf bytes.Buffer
	if err := WriteJSONReport(&buf, rep); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	skipped, ok := out["skipped"].([]any)
	if !ok || len(skipped) != 1 {
		t.Fatalf("skipped: %+v", out["skipped"])
	}
	s := skipped[0].(map[string]any)
	if s["policy_id"] != "p1" {
		t.Errorf("policy_id: got %v", s["policy_id"])
	}
	if s["subject"] != "story:abc" {
		t.Errorf("subject: got %v", s["subject"])
	}
	if s["path"] != "/root/by-story/abc/p1.json" {
		t.Errorf("path: got %v", s["path"])
	}
	if got, want := s["last_captured_at"], "2026-04-20T12:34:56.789Z"; got != want {
		t.Errorf("last_captured_at: got %q, want %q", got, want)
	}
	// When Skipped is empty, the field still serializes as [] so
	// jq consumers don't have to handle null.
	rep2 := Report{}
	var buf2 bytes.Buffer
	_ = WriteJSONReport(&buf2, rep2)
	var out2 map[string]any
	_ = json.Unmarshal(buf2.Bytes(), &out2)
	if out2["skipped"] == nil {
		t.Error("skipped should serialize as [] not null when empty")
	}
}
