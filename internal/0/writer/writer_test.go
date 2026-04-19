package writer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubWriter writes a shell-script stub into a fresh temp dir that
// emits a RFC 0001 Writer Protocol output. The returned argv is
// [bash scriptPath extraArgs...].
func stubWriter(t *testing.T, body string) []string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "stub.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return []string{"bash", path}
}

// ndjsonStub is the minimal conformant writer: counts stdin bytes,
// emits `{"id":"blake2b256-stub-<size>","size":<size>}`.
const ndjsonStub = `#!/usr/bin/env bash
set -euo pipefail
size=$(wc -c)
printf '{"id":"blake2b256-stub-%s","size":%s}\n' "$size" "$size"
`

func TestWrite_happyPath(t *testing.T) {
	argv := stubWriter(t, ndjsonStub)
	payload := []byte("hello world")

	res, err := Write(context.Background(), argv, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.Size != int64(len(payload)) {
		t.Errorf("size: got %d, want %d", res.Size, len(payload))
	}
	if !strings.HasPrefix(res.ID, "blake2b256-stub-") {
		t.Errorf("id prefix: got %q", res.ID)
	}
	if !strings.HasSuffix(res.ID, "-11") {
		t.Errorf("id suffix (size-encoded): got %q", res.ID)
	}
	if len(res.Extra) != 0 {
		t.Errorf("expected no extra fields, got %v", res.Extra)
	}
}

func TestWrite_streamingNotBufferedInMemory(t *testing.T) {
	// The writer reads lazily via `wc -c`. We hand it an io.Reader
	// that would be expensive to slurp, to exercise streaming.
	argv := stubWriter(t, ndjsonStub)

	// 64 KiB × 100 = 6.4 MiB — still small for CI but enough to
	// verify the copy path actually flows.
	chunk := bytes.Repeat([]byte("a"), 64*1024)
	src := io.MultiReader(
		bytes.NewReader(chunk), bytes.NewReader(chunk), bytes.NewReader(chunk),
		bytes.NewReader(chunk), bytes.NewReader(chunk),
	)

	res, err := Write(context.Background(), argv, src)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := int64(5 * len(chunk))
	if res.Size != want {
		t.Errorf("size: got %d, want %d", res.Size, want)
	}
}

func TestWrite_extraFieldsPreserved(t *testing.T) {
	const stub = `#!/usr/bin/env bash
set -euo pipefail
size=$(wc -c)
printf '{"id":"blake2b256-stub-%s","size":%s,"source":"-","store":"ephemeral"}\n' "$size" "$size"
`
	argv := stubWriter(t, stub)

	res, err := Write(context.Background(), argv, strings.NewReader("x"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := res.Extra["source"]; got != "-" {
		t.Errorf("Extra[source]: got %v, want %q", got, "-")
	}
	if got := res.Extra["store"]; got != "ephemeral" {
		t.Errorf("Extra[store]: got %v, want %q", got, "ephemeral")
	}
}

func TestWrite_nonzeroExitSurfacesAsError(t *testing.T) {
	const stub = `#!/usr/bin/env bash
echo "simulated failure" >&2
exit 42
`
	argv := stubWriter(t, stub)
	_, err := Write(context.Background(), argv, strings.NewReader(""))

	var werr *Error
	if !errors.As(err, &werr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if werr.Kind != "nonzero-exit" {
		t.Errorf("kind: got %q, want nonzero-exit", werr.Kind)
	}
	if werr.Status != 42 {
		t.Errorf("status: got %d, want 42", werr.Status)
	}
	if !strings.Contains(werr.Stderr, "simulated failure") {
		t.Errorf("stderr should be captured, got %q", werr.Stderr)
	}
}

func TestWrite_emptyStdoutIsError(t *testing.T) {
	const stub = `#!/usr/bin/env bash
exit 0
`
	argv := stubWriter(t, stub)
	_, err := Write(context.Background(), argv, strings.NewReader(""))

	var werr *Error
	if !errors.As(err, &werr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if werr.Kind != "empty-stdout" {
		t.Errorf("kind: got %q, want empty-stdout", werr.Kind)
	}
}

func TestWrite_malformedJSONIsError(t *testing.T) {
	const stub = `#!/usr/bin/env bash
printf 'not a json object\n'
`
	argv := stubWriter(t, stub)
	_, err := Write(context.Background(), argv, strings.NewReader(""))

	var werr *Error
	if !errors.As(err, &werr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if werr.Kind != "bad-json" {
		t.Errorf("kind: got %q, want bad-json", werr.Kind)
	}
}

func TestWrite_multipleObjectsIsError(t *testing.T) {
	const stub = `#!/usr/bin/env bash
printf '{"id":"blake2b256-stub-0","size":0}\n{"id":"blake2b256-stub-0","size":0}\n'
`
	argv := stubWriter(t, stub)
	_, err := Write(context.Background(), argv, strings.NewReader(""))

	var werr *Error
	if !errors.As(err, &werr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if werr.Kind != "trailing-data" {
		t.Errorf("kind: got %q, want trailing-data", werr.Kind)
	}
}

func TestWrite_missingIDIsError(t *testing.T) {
	const stub = `#!/usr/bin/env bash
printf '{"size":0}\n'
`
	argv := stubWriter(t, stub)
	_, err := Write(context.Background(), argv, strings.NewReader(""))

	var werr *Error
	if !errors.As(err, &werr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if werr.Kind != "bad-shape" {
		t.Errorf("kind: got %q, want bad-shape", werr.Kind)
	}
	if !strings.Contains(werr.Msg, "id") {
		t.Errorf("msg should mention id: %q", werr.Msg)
	}
}

func TestWrite_missingSizeIsError(t *testing.T) {
	const stub = `#!/usr/bin/env bash
printf '{"id":"blake2b256-stub-0"}\n'
`
	argv := stubWriter(t, stub)
	_, err := Write(context.Background(), argv, strings.NewReader(""))

	var werr *Error
	if !errors.As(err, &werr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if werr.Kind != "bad-shape" {
		t.Errorf("kind: got %q, want bad-shape", werr.Kind)
	}
	if !strings.Contains(werr.Msg, "size") {
		t.Errorf("msg should mention size: %q", werr.Msg)
	}
}

func TestWrite_negativeSizeIsError(t *testing.T) {
	const stub = `#!/usr/bin/env bash
printf '{"id":"blake2b256-stub-0","size":-1}\n'
`
	argv := stubWriter(t, stub)
	_, err := Write(context.Background(), argv, strings.NewReader(""))

	var werr *Error
	if !errors.As(err, &werr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if werr.Kind != "bad-shape" {
		t.Errorf("kind: got %q, want bad-shape", werr.Kind)
	}
}

func TestWrite_idWithoutMarklShapeIsError(t *testing.T) {
	const stub = `#!/usr/bin/env bash
printf '{"id":"deadbeef","size":0}\n'
`
	argv := stubWriter(t, stub)
	_, err := Write(context.Background(), argv, strings.NewReader(""))

	var werr *Error
	if !errors.As(err, &werr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if werr.Kind != "bad-shape" {
		t.Errorf("kind: got %q, want bad-shape", werr.Kind)
	}
	if !strings.Contains(werr.Msg, "markl-id") {
		t.Errorf("msg should mention markl-id: %q", werr.Msg)
	}
}

func TestWrite_emptyArgvIsError(t *testing.T) {
	_, err := Write(context.Background(), nil, strings.NewReader(""))
	var werr *Error
	if !errors.As(err, &werr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if werr.Kind != "bad-argv" {
		t.Errorf("kind: got %q, want bad-argv", werr.Kind)
	}
}

func TestWrite_spawnFailedIsError(t *testing.T) {
	_, err := Write(context.Background(), []string{"/definitely/not/a/real/path-" + t.Name()}, strings.NewReader(""))
	var werr *Error
	if !errors.As(err, &werr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if werr.Kind != "spawn-failed" {
		t.Errorf("kind: got %q, want spawn-failed", werr.Kind)
	}
}

// Sanity: the Result round-trips through json-marshalable Extras.
func TestWrite_extrasAreJSONMarshalable(t *testing.T) {
	const stub = `#!/usr/bin/env bash
printf '{"id":"blake2b256-stub-0","size":0,"source":"-","extra_num":42}\n'
`
	argv := stubWriter(t, stub)

	res, err := Write(context.Background(), argv, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(res.Extra)
	if err != nil {
		t.Fatalf("marshal extras: %v", err)
	}
	if !bytes.Contains(b, []byte(`"source":"-"`)) {
		t.Errorf("marshaled extras missing `source`: %s", b)
	}
}
