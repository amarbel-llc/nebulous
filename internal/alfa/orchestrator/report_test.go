package orchestrator

import (
	"bytes"
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
