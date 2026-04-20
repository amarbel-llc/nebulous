package orchestrator

import (
	"context"
	"io"
	"time"

	"github.com/friedenberg/nebulous/internal/0/archive"
	"github.com/friedenberg/nebulous/internal/alfa/capturer"
	"github.com/friedenberg/nebulous/internal/alfa/policy"
)

// Deps is the orchestrator's dependency injection seam. The CLI
// layer constructs a Deps (real newsblur resolver, real chrest
// subprocess, real madder history store) and hands it to Run.
// Tests substitute stubs.
//
// All fields are required — Run does not supply defaults. A
// zero-valued field will cause a nil-deref panic on first use,
// which is the intended "invariant violation" semantics per the
// design.
type Deps struct {
	LoadPolicies func(path string) ([]policy.Policy, error)
	ResolveStory func(id string) (policy.Story, error)
	RunCapturer  func(context.Context, capturer.BatchInput) (capturer.BatchOutput, error)
	WriteArchive func(context.Context, string, *archive.Record, archive.Writer) error
	TimeNow      func() time.Time

	// HistoryStore satisfies archive.Writer — receives prior-record
	// bytes on overwrites and returns a markl ID for the Previous
	// field. The real production wiring uses internal/0/madder.Store.
	HistoryStore archive.Writer

	// WriterCmd is the argv passed into BatchInput.Writer when the
	// orchestrator builds a capturer.BatchInput. Production: the
	// flake-pinned madder binary. Tests: something harmless.
	WriterCmd []string
}

// nopHistory is an archive.Writer that discards prior-record bytes
// and returns a fixed markl-like id. Used in tests that don't care
// about history semantics.
type nopHistory struct{}

func (nopHistory) Write(ctx context.Context, src io.Reader) (string, error) {
	_, _ = io.Copy(io.Discard, src)
	return "blake2b256-nop-history", nil
}
