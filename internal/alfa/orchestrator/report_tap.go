package orchestrator

import (
	"fmt"
	"io"

	tap "github.com/amarbel-llc/bob/packages/tap-dancer/go"
)

// writeTAPReport emits a TAP-14 stream describing the Report:
// one test point per Job (ok) and JobFailure (not ok with
// diagnostics), followed by BailOut when the circuit breaker
// tripped.
//
// Ordering: all Written entries emit first (in insertion order),
// then all Failed entries (in insertion order). The TAP numbering
// is the union sequence. This is not the interleaved execution
// order — the design doc doesn't pin inside-TAP ordering and this
// is the simplest deterministic choice.
func writeTAPReport(w io.Writer, r Report) error {
	tw := tap.NewWriter(w)
	tw.PlanAhead(len(r.Written) + len(r.Failed))

	for _, j := range r.Written {
		tw.Ok(fmt.Sprintf("%s %s", j.PolicyID, j.Subject))
	}
	for _, f := range r.Failed {
		tw.NotOk(fmt.Sprintf("%s %s", f.PolicyID, f.Subject), map[string]string{
			"kind":    f.Kind,
			"message": f.Message,
		})
	}
	if r.BailedOut {
		tw.BailOut("3 consecutive archive job failures")
	}
	return nil
}
