// Package writer is a client for the writer side of the RFC 0001 Web
// Capture Archive Protocol.
//
// A writer is a CLI program that accepts bytes on stdin and emits one
// NDJSON object on stdout containing a content-addressed identifier
// and the byte count. This package spawns such a writer, streams the
// payload into its stdin without buffering, parses the stdout result,
// and surfaces a typed error for any protocol violation.
//
// The concrete writer used by nebulous is madder; however, this
// package is deliberately writer-agnostic. Any binary conforming to
// § Writer Protocol of the RFC is usable.
//
// See docs/rfcs/0001-web-capture-archive-protocol.md.
package writer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Result is a successful writer invocation's output. It corresponds to
// the writer protocol's mandatory JSON shape:
//
//	{"id": "<markl-id>", "size": <n>, ...}
//
// Additional fields emitted by the writer are accessible through the
// Extra map so callers that need e.g. madder's `source` or `store`
// fields can read them without the package knowing those names.
type Result struct {
	ID    string
	Size  int64
	Extra map[string]any
}

// Error represents a writer-side failure: non-zero exit, malformed
// output, or empty output. The stderr capture is included verbatim
// for diagnostics.
type Error struct {
	Kind   string
	Msg    string
	Stderr string
	Status int
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("writer: %s: exit=%d: %s", e.Kind, e.Status, e.Msg)
	}
	return fmt.Sprintf("writer: %s: %s", e.Kind, e.Msg)
}

// Write spawns argv as a writer subprocess, streams src into its
// stdin, and parses one NDJSON result object from its stdout.
//
// argv[0] is the writer binary; argv[1:] are its arguments. argv
// must have at least one element.
//
// The caller retains ownership of src. Write does not close src.
//
// Write does not buffer src in memory — bytes flow through
// io.Copy directly into the writer's stdin, so multi-megabyte
// payloads are supported without local memory pressure. The result
// is typically small enough to buffer in full.
//
// Context cancellation kills the subprocess via SIGKILL (os/exec's
// default), so callers should use a context with a reasonable
// timeout for non-trusted inputs.
func Write(ctx context.Context, argv []string, src io.Reader) (Result, error) {
	if len(argv) == 0 {
		return Result{}, &Error{Kind: "bad-argv", Msg: "empty argv"}
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Result{}, fmt.Errorf("writer: stdin pipe: %w", err)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return Result{}, &Error{Kind: "spawn-failed", Msg: err.Error()}
	}

	// Copy src → writer stdin in the foreground. The spec requires
	// the writer to begin reading before stdin is closed, so interleaved
	// streaming is safe. Close stdin on EOF to signal end-of-stream.
	copyErr := streamAndClose(stdin, src)

	// Always wait — even if copy failed, we need to reap the subprocess.
	waitErr := cmd.Wait()

	if copyErr != nil && !errors.Is(copyErr, io.ErrClosedPipe) {
		return Result{}, &Error{
			Kind:   "stdin-copy-failed",
			Msg:    copyErr.Error(),
			Stderr: stderr.String(),
			Status: exitStatus(waitErr),
		}
	}

	if waitErr != nil {
		return Result{}, &Error{
			Kind:   "nonzero-exit",
			Msg:    waitErr.Error(),
			Stderr: stderr.String(),
			Status: exitStatus(waitErr),
		}
	}

	return parseResult(stdout.Bytes(), stderr.String())
}

// streamAndClose copies src into w and closes w. Closing on EOF is
// how the writer protocol signals end-of-stream.
func streamAndClose(w io.WriteCloser, src io.Reader) error {
	_, copyErr := io.Copy(w, src)
	closeErr := w.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// parseResult validates that stdout contains exactly one JSON object
// with id + size, per the writer protocol. Trailing whitespace
// (including a terminating newline) is tolerated; additional JSON
// objects or non-whitespace trailing bytes are a protocol violation.
func parseResult(stdoutBytes []byte, stderrText string) (Result, error) {
	trimmed := bytes.TrimSpace(stdoutBytes)
	if len(trimmed) == 0 {
		return Result{}, &Error{
			Kind:   "empty-stdout",
			Msg:    "writer exited 0 but produced no stdout",
			Stderr: stderrText,
		}
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()

	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return Result{}, &Error{
			Kind:   "bad-json",
			Msg:    err.Error(),
			Stderr: stderrText,
		}
	}

	// The writer protocol allows whitespace after the object (per
	// NDJSON framing) but no further tokens.
	if dec.More() {
		return Result{}, &Error{
			Kind:   "trailing-data",
			Msg:    "writer emitted more than one JSON object on stdout",
			Stderr: stderrText,
		}
	}

	res, err := extractResult(raw)
	if err != nil {
		return Result{}, &Error{
			Kind:   "bad-shape",
			Msg:    err.Error(),
			Stderr: stderrText,
		}
	}
	return res, nil
}

func extractResult(raw map[string]any) (Result, error) {
	idAny, ok := raw["id"]
	if !ok {
		return Result{}, errors.New("missing required field `id`")
	}
	idStr, ok := idAny.(string)
	if !ok || idStr == "" {
		return Result{}, errors.New("field `id` must be a non-empty string")
	}
	if !strings.ContainsRune(idStr, '-') {
		return Result{}, fmt.Errorf("field `id` is not a markl-id: %q", idStr)
	}

	sizeAny, ok := raw["size"]
	if !ok {
		return Result{}, errors.New("missing required field `size`")
	}
	sizeNum, ok := sizeAny.(json.Number)
	if !ok {
		return Result{}, fmt.Errorf("field `size` must be a number, got %T", sizeAny)
	}
	size, err := sizeNum.Int64()
	if err != nil {
		return Result{}, fmt.Errorf("field `size` must be an integer: %w", err)
	}
	if size < 0 {
		return Result{}, fmt.Errorf("field `size` must be >= 0, got %d", size)
	}

	extra := make(map[string]any, len(raw))
	for k, v := range raw {
		if k == "id" || k == "size" {
			continue
		}
		extra[k] = v
	}

	return Result{ID: idStr, Size: size, Extra: extra}, nil
}

func exitStatus(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 0
}
