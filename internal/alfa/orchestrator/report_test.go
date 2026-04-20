package orchestrator

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWriteTAPReport_basicShape(t *testing.T) {
	rep := Report{
		Written: []Job{
			{PolicyID: "p1", Subject: "story:abc"},
			{PolicyID: "p2", Subject: "story:abc"},
		},
		Failed: []JobFailure{
			{PolicyID: "p1", Subject: "url:sha256-def", Kind: "writer-failed", Message: "permission denied"},
		},
	}

	var buf bytes.Buffer
	if err := writeTAPReport(&buf, rep); err != nil {
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
	if err := writeTAPReport(&buf, rep); err != nil {
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
	if err := writeJSONReport(&buf, rep); err != nil {
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
	if err := writeJSONReport(&buf, rep); err != nil {
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

func TestEmitReport_dispatchesByTTY(t *testing.T) {
	rep := Report{Written: []Job{{PolicyID: "p", Subject: "story:abc"}}}

	var tapOut, jsonOut bytes.Buffer
	if err := EmitReport(&tapOut, rep, true); err != nil {
		t.Fatal(err)
	}
	if err := EmitReport(&jsonOut, rep, false); err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(tapOut.String(), "TAP version 14") {
		t.Errorf("tty mode should emit TAP header, got: %q", firstLine(tapOut.String()))
	}
	if !strings.HasPrefix(strings.TrimSpace(jsonOut.String()), "{") {
		t.Errorf("non-tty mode should emit JSON object, got: %q", firstLine(jsonOut.String()))
	}
}
