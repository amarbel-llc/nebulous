// Package capturer spawns the RFC 0001 capturer (chrest) via
// `chrest capture-batch` and parses its batch output. Structured
// to mirror internal/0/writer: subprocess, JSON in, one JSON
// object out, typed error kinds.
//
// Bin is overridable via ldflags (see flake.nix for the
// capturer.Bin=<path> injection), so Nix-built nebulous invokes
// the exact chrest pinned in the flake. Dev builds fall back to
// $PATH.
//
// See docs/plans/2026-04-19-orchestrator-design.md § capturer.
package capturer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
)

// Bin is the path to the chrest binary. Overridable at link time via:
//
//	-ldflags "-X github.com/friedenberg/nebulous/internal/alfa/capturer.Bin=<path>"
//
// Defaults to $PATH lookup.
var Bin = "chrest"

// Error is the typed failure surface. Kind is machine-readable and
// stable; Msg is human-readable; Stderr carries the child's stderr
// capture verbatim for diagnostics; Status is the subprocess exit
// code when applicable.
type Error struct {
	Kind   string
	Msg    string
	Stderr string
	Status int
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("capturer: %s: exit=%d: %s", e.Kind, e.Status, e.Msg)
	}
	return fmt.Sprintf("capturer: %s: %s", e.Kind, e.Msg)
}

// Run executes `chrest capture-batch`, pipes the marshaled BatchInput
// to its stdin, reads exactly one JSON object from its stdout.
//
// Returns:
//
//   - (BatchOutput{}, nil) only when the capturer exited 0, produced
//     exactly one JSON object, the object decoded, and its `schema`
//     matched the expected value.
//   - (BatchOutput{}, *Error) on any protocol violation. Kind values:
//     nonzero-exit, empty-stdout, bad-json, trailing-data, bad-shape.
//
// Per-capture errors inside a well-formed BatchOutput are NOT a Run
// error — they live in the returned BatchOutput.Captures[i].Error.
// Batch-level errors from the capturer are in BatchOutput.Errors.
func Run(ctx context.Context, in BatchInput) (BatchOutput, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return BatchOutput{}, fmt.Errorf("capturer: marshal input: %w", err)
	}

	cmd := exec.CommandContext(ctx, Bin, "capture-batch")
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return BatchOutput{}, &Error{
			Kind:   "nonzero-exit",
			Msg:    err.Error(),
			Stderr: stderr.String(),
			Status: exitStatus(err),
		}
	}

	trimmed := bytes.TrimSpace(stdout.Bytes())
	if len(trimmed) == 0 {
		return BatchOutput{}, &Error{
			Kind:   "empty-stdout",
			Msg:    "capturer exited 0 but produced no stdout",
			Stderr: stderr.String(),
		}
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var out BatchOutput
	if err := dec.Decode(&out); err != nil {
		return BatchOutput{}, &Error{
			Kind:   "bad-json",
			Msg:    err.Error(),
			Stderr: stderr.String(),
		}
	}
	if dec.More() {
		return BatchOutput{}, &Error{
			Kind:   "trailing-data",
			Msg:    "capturer emitted more than one JSON object on stdout",
			Stderr: stderr.String(),
		}
	}

	if out.Schema != Schema {
		return BatchOutput{}, &Error{
			Kind:   "bad-shape",
			Msg:    fmt.Sprintf("schema must be %q, got %q", Schema, out.Schema),
			Stderr: stderr.String(),
		}
	}

	return out, nil
}

func exitStatus(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 0
}
