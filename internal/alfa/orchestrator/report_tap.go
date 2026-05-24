package orchestrator

import (
	"fmt"
	"io"

	tap "github.com/amarbel-llc/tap/go/pkgs/writer"
)

// WriteTAPReport emits a TAP-14 stream describing an already-complete
// Report: one test point per Job (ok), JobFailure (not ok with
// diagnostics), and Skip (ok with # SKIP directive), followed by
// BailOut when the circuit breaker tripped. This is the batched
// counterpart to Args.StreamTAP, which emits the same shape
// incrementally while the run is in progress.
//
// color enables ANSI-colored keywords (`ok`, `not ok`, `# SKIP`,
// `Bail out!`). Pass true only when w is known to be a TTY; false
// otherwise.
//
// Ordering: Written first, then Skipped, then Failed, each in
// insertion order. The TAP numbering is the union sequence. This
// is not the interleaved execution order — the design doc doesn't
// pin inside-TAP ordering and this is the simplest deterministic
// choice.
func WriteTAPReport(w io.Writer, r Report, color bool) error {
	tw := tap.NewColorWriter(w, color)
	tw.PlanAhead(len(r.Written) + len(r.Skipped) + len(r.Failed))

	for _, j := range r.Written {
		tw.Ok(okDescription(j))
	}
	for _, s := range r.Skipped {
		tw.Skip(fmt.Sprintf("%s %s", s.PolicyID, s.Subject), skipReason(s))
	}
	for _, f := range r.Failed {
		tw.NotOk(fmt.Sprintf("%s %s", f.PolicyID, f.Subject), map[string]string{
			"kind":    f.Kind,
			"message": f.Message,
		})
	}
	if r.BailedOut {
		tw.BailOut(bailOutReason)
	}
	return nil
}
