package cgplugin

import (
	"context"
	"fmt"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

var _ cg.BulkMutator = Plugin{}

// BulkMutate delegates to cutting-garden's shared best-effort dispatch
// helper (cg.BestEffortBulkMutate, cutting-garden#197) -- the same #182
// patchedNothing-vs-applied partitioning and sweep decline-refusal
// write-safety caldav and jira also use, so this plugin can't drift from
// that contract by carrying its own divergent copy (an earlier version
// of this file did exactly that: a hand-rolled port of caldav's original
// dispatch loop, before #197 extracted it). NewsBlur's REST API has no
// multi-object transaction primitive, so the helper's own
// atomic-rejects-with-ErrBulkAtomicUnsupported floor is exactly this
// plugin's posture too -- nothing more to add on top of the delegation.
// p satisfies both arguments the helper needs: NodeMutator (mutate.go)
// and EnrichedLister (traversal.go's ListEnriched, added specifically to
// make this delegation possible).
func (p Plugin) BulkMutate(
	ctx context.Context, req cg.BulkRequest,
) (cg.BulkResult, error) {
	if index == nil || client == nil {
		return cg.BulkResult{}, fmt.Errorf("newsblur plugin: not initialized")
	}
	return cg.BestEffortBulkMutate(ctx, p, p, req)
}
