package capturer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeStub materializes a bash script in a fresh temp dir and
// returns its path. Callers then point Bin at that path for the test.
func writeStub(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "chrest-stub.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

// Minimal conformant stub: consumes stdin, emits a canned
// BatchOutput with one successful capture.
const goodStub = `#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
cat <<'JSON'
{
  "schema": "web-capture-archive/v1",
  "capturer": {"name": "chrest-stub", "version": "0.0.1"},
  "errors": [],
  "captures": [
    {
      "name": "text",
      "spec": {"id": "blake2b256-s", "size": 10, "media_type": "application/vnd.web-capture-archive.spec+json"},
      "payload": {"id": "blake2b256-p", "size": 20, "media_type": "text/plain; charset=utf-8"}
    }
  ]
}
JSON
`

func basicInput() BatchInput {
	return BatchInput{
		Schema:   Schema,
		Writer:   WriterCmd{Cmd: []string{"/bin/true"}},
		URL:      "https://example.com/",
		Captures: []CaptureRequest{{Name: "text", Format: "text"}},
	}
}

func TestRun_happyPath(t *testing.T) {
	Bin = writeStub(t, goodStub)
	t.Cleanup(func() { Bin = "chrest" })

	out, err := Run(context.Background(), basicInput())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Capturer.Name != "chrest-stub" {
		t.Errorf("capturer.name: got %q", out.Capturer.Name)
	}
	if len(out.Captures) != 1 {
		t.Errorf("captures length: got %d", len(out.Captures))
	}
	if out.Captures[0].Payload == nil {
		t.Errorf("payload should be present")
	}
}

const failingStub = `#!/usr/bin/env bash
echo "something broke" >&2
exit 7
`

func TestRun_nonzeroExit(t *testing.T) {
	Bin = writeStub(t, failingStub)
	t.Cleanup(func() { Bin = "chrest" })

	_, err := Run(context.Background(), basicInput())
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if cerr.Kind != "nonzero-exit" || cerr.Status != 7 {
		t.Errorf("got %+v", cerr)
	}
	if !strings.Contains(cerr.Stderr, "something broke") {
		t.Errorf("stderr should be captured, got %q", cerr.Stderr)
	}
}

const emptyStub = `#!/usr/bin/env bash
cat >/dev/null
exit 0
`

func TestRun_emptyStdout(t *testing.T) {
	Bin = writeStub(t, emptyStub)
	t.Cleanup(func() { Bin = "chrest" })

	_, err := Run(context.Background(), basicInput())
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if cerr.Kind != "empty-stdout" {
		t.Errorf("got kind %q", cerr.Kind)
	}
}

const malformedStub = `#!/usr/bin/env bash
cat >/dev/null
printf 'not a json object\n'
`

func TestRun_badJSON(t *testing.T) {
	Bin = writeStub(t, malformedStub)
	t.Cleanup(func() { Bin = "chrest" })

	_, err := Run(context.Background(), basicInput())
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if cerr.Kind != "bad-json" {
		t.Errorf("got kind %q", cerr.Kind)
	}
}

const trailingDataStub = `#!/usr/bin/env bash
cat >/dev/null
cat <<'JSON'
{"schema": "web-capture-archive/v1", "capturer": {"name":"x","version":"0"}, "errors": [], "captures": []}
{"schema": "web-capture-archive/v1", "capturer": {"name":"x","version":"0"}, "errors": [], "captures": []}
JSON
`

func TestRun_trailingData(t *testing.T) {
	Bin = writeStub(t, trailingDataStub)
	t.Cleanup(func() { Bin = "chrest" })

	_, err := Run(context.Background(), basicInput())
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if cerr.Kind != "trailing-data" {
		t.Errorf("got kind %q", cerr.Kind)
	}
}

const badSchemaStub = `#!/usr/bin/env bash
cat >/dev/null
cat <<'JSON'
{"schema": "other/v1", "capturer": {"name":"x","version":"0"}, "errors": [], "captures": []}
JSON
`

func TestRun_badShape(t *testing.T) {
	Bin = writeStub(t, badSchemaStub)
	t.Cleanup(func() { Bin = "chrest" })

	_, err := Run(context.Background(), basicInput())
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if cerr.Kind != "bad-shape" {
		t.Errorf("got kind %q", cerr.Kind)
	}
	if !strings.Contains(cerr.Msg, "schema") {
		t.Errorf("msg should mention schema, got %q", cerr.Msg)
	}
}

func TestRun_spawnFailed(t *testing.T) {
	Bin = "/definitely/not/a/real/path-" + t.Name()
	t.Cleanup(func() { Bin = "chrest" })

	_, err := Run(context.Background(), basicInput())
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	// nonzero-exit from exec.Run on a missing binary is acceptable;
	// the important thing is we get a *capturer.Error with Kind set.
	if cerr.Kind == "" {
		t.Errorf("kind should be set, got %+v", cerr)
	}
}
