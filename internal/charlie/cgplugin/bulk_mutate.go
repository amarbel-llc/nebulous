package cgplugin

import (
	"bytes"
	"context"
	"fmt"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

var _ cg.BulkMutator = Plugin{}

// BulkMutate is newsblur's best-effort BulkMutator (RFC 0017,
// cutting-garden#191). NewsBlur's REST API has no multi-object
// transaction primitive -- every mutation here is already its own HTTP
// call -- so this plugin advertises bulk-mutate only (never bulk-atomic)
// and REJECTS an atomic request with cg.ErrBulkAtomicUnsupported rather
// than silently downgrading to best-effort. Best-effort applies each op
// via the SAME per-verb NodeMutator methods (mutate.go) a single
// create_node/put_node/patch_node/delete_node call would use -- so the
// per-node write contract (strict create, unsupported put, #182 patch
// reporting, delete) is identical to a single-node call, and mutateMu is
// already serialized per call the same way N sequential single-node
// calls would be, needing no extra locking here.
func (Plugin) BulkMutate(
	ctx context.Context, req cg.BulkRequest,
) (cg.BulkResult, error) {
	if index == nil || client == nil {
		return cg.BulkResult{}, fmt.Errorf("newsblur plugin: not initialized")
	}
	if req.Atomicity == cg.BulkAtomic {
		return cg.BulkResult{}, cg.ErrBulkAtomicUnsupported
	}

	if req.Sweep != nil {
		return bulkSweep(ctx, req.Sweep)
	}
	return bulkOps(ctx, req.Ops)
}

func bulkOps(
	ctx context.Context, ops []cg.BulkOp,
) (cg.BulkResult, error) {
	var result cg.BulkResult
	for _, op := range ops {
		applyBulkOp(ctx, op, &result)
	}
	return result, nil
}

// bulkSweep resolves sweep.Root's children via the plugin's own
// ListRoots (traversal.go) -- already Facet-enriched for every
// story/feed node, the same data storyFacetCounts/feedFacetCounts
// (facets.go) already fold over -- and keeps the ones matching
// sweep.Filter, exactly the filter.Matches(node.Facets) pattern those
// functions already use. Unlike caldav's ListEnriched-based sweep, there
// is no "wrong scope" decline case to handle: nebulous's ListRoots never
// flattens a container-of-containers the way caldav's calendar-home
// does, so a root with no facet-bearing children (e.g. a folder/{path},
// which traversal.go documents as having no read surface at all) simply
// yields zero matches -- an empty sweep, not an error.
func bulkSweep(
	ctx context.Context, sweep *cg.BulkSweep,
) (cg.BulkResult, error) {
	nodes, err := Plugin{}.ListRoots(ctx, sweep.Root)
	if err != nil {
		return cg.BulkResult{}, err
	}

	var result cg.BulkResult
	for _, node := range nodes {
		if !sweep.Filter.Matches(node.Facets) {
			continue
		}
		op := sweep.Op
		op.URI = node.URI
		applyBulkOp(ctx, op, &result)
	}
	return result, nil
}

// applyBulkOp applies one op via the matching NodeMutator verb and
// records the outcome: AppliedNodes on success, PatchedNothing when a
// patch was accepted but landed no recognized field (#182), Failed
// (never a returned error, in best-effort mode) otherwise -- identical
// bookkeeping to caldav's own applyBulkOp.
func applyBulkOp(ctx context.Context, op cg.BulkOp, result *cg.BulkResult) {
	var (
		patchedNothing bool
		err            error
	)

	switch op.Kind {
	case cg.BulkCreate:
		err = Plugin{}.CreateNode(ctx, op.URI, bytes.NewReader(op.Body), op.Type)
	case cg.BulkPut:
		err = Plugin{}.PutNode(ctx, op.URI, bytes.NewReader(op.Body))
	case cg.BulkPatch:
		var applied []string
		applied, err = Plugin{}.PatchNode(ctx, op.URI, bytes.NewReader(op.Body))
		patchedNothing = err == nil && len(applied) == 0
	case cg.BulkDelete:
		err = Plugin{}.DeleteNode(ctx, op.URI)
	default:
		err = fmt.Errorf("newsblur plugin: unknown bulk op kind %q", op.Kind)
	}

	switch {
	case err != nil:
		result.Failed = append(result.Failed, cg.BulkFailure{
			URI: op.URI, Err: err.Error(),
		})
	case patchedNothing:
		result.PatchedNothing = append(result.PatchedNothing, op.URI)
	default:
		result.AppliedNodes = append(result.AppliedNodes, op.URI)
	}
}
