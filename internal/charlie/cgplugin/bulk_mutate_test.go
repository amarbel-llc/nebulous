package cgplugin

import (
	"context"
	"errors"
	"testing"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func TestBulkMutateNotInitialized(t *testing.T) {
	index = nil
	client = nil

	_, err := Plugin{}.BulkMutate(context.Background(), cg.BulkRequest{
		Atomicity: cg.BulkBestEffort,
		Ops:       []cg.BulkOp{{Kind: cg.BulkDelete, URI: mustURL(t, "newsblur://story/"+sampleHash)}},
	})
	if err == nil {
		t.Fatal("BulkMutate with no index/client: expected an error, got nil")
	}
}

func TestBulkMutateRejectsAtomic(t *testing.T) {
	_, fc := setupMutateTest(t)

	_, err := Plugin{}.BulkMutate(context.Background(), cg.BulkRequest{
		Atomicity: cg.BulkAtomic,
		Ops:       []cg.BulkOp{{Kind: cg.BulkDelete, URI: mustURL(t, "newsblur://story/"+sampleHash)}},
	})
	if !errors.Is(err, cg.ErrBulkAtomicUnsupported) {
		t.Errorf("err = %v, want ErrBulkAtomicUnsupported", err)
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none — atomic must be rejected before applying anything", fc.calls)
	}
}

// Explicit ops, best-effort, with a deliberately mixed outcome: one patch
// that lands, one delete (unstar) on a nonexistent story that fails, and
// one delete on the real story that lands — exercising all three
// AppliedNodes/Failed/PatchedNothing partitions is covered across this
// and the two tests below, mirroring how mutate_test.go splits each verb
// into its own test rather than one giant one.
func TestBulkMutateOpsPartitionsOutcomes(t *testing.T) {
	_, fc := setupMutateTest(t)

	storyURI := mustURL(t, "newsblur://story/"+sampleHash)
	missingURI := mustURL(t, "newsblur://story/999:missing")

	result, err := Plugin{}.BulkMutate(context.Background(), cg.BulkRequest{
		Atomicity: cg.BulkBestEffort,
		Ops: []cg.BulkOp{
			{Kind: cg.BulkPatch, URI: storyURI, Body: []byte(`{"user_tags":["a","b"]}`)},
			{Kind: cg.BulkDelete, URI: missingURI},
		},
	})
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}

	if len(result.AppliedNodes) != 1 || result.AppliedNodes[0].String() != storyURI.String() {
		t.Errorf("AppliedNodes = %v, want [%s]", result.AppliedNodes, storyURI)
	}
	if len(result.Failed) != 1 || result.Failed[0].URI.String() != missingURI.String() {
		t.Errorf("Failed = %v, want one failure for %s", result.Failed, missingURI)
	}
	if len(result.PatchedNothing) != 0 {
		t.Errorf("PatchedNothing = %v, want none", result.PatchedNothing)
	}
	if result.Atomic {
		t.Error("Atomic = true in a best-effort result, want false")
	}

	wantCalls := []string{"SetStoryUserTags:" + sampleHash + ":a,b"}
	if len(fc.calls) != len(wantCalls) || fc.calls[0] != wantCalls[0] {
		t.Errorf("calls = %v, want %v (the missing-story delete must never reach the client)", fc.calls, wantCalls)
	}
}

// A patch whose body recognizes no field is accepted but applies
// nothing (#182) — that belongs in PatchedNothing, distinct from both
// AppliedNodes and Failed, mirroring PatchNode's own applied==[]string{}
// case (mutate_test.go's TestPatchNodeStoryNoRecognizedFields-equivalent).
func TestBulkMutateOpsPatchedNothing(t *testing.T) {
	_, fc := setupMutateTest(t)

	storyURI := mustURL(t, "newsblur://story/"+sampleHash)
	result, err := Plugin{}.BulkMutate(context.Background(), cg.BulkRequest{
		Atomicity: cg.BulkBestEffort,
		Ops:       []cg.BulkOp{{Kind: cg.BulkPatch, URI: storyURI, Body: []byte(`{}`)}},
	})
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}
	if len(result.PatchedNothing) != 1 || result.PatchedNothing[0].String() != storyURI.String() {
		t.Errorf("PatchedNothing = %v, want [%s]", result.PatchedNothing, storyURI)
	}
	if len(result.AppliedNodes) != 0 || len(result.Failed) != 0 {
		t.Errorf("AppliedNodes = %v, Failed = %v, want both empty", result.AppliedNodes, result.Failed)
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none — an empty patch body never reaches the client", fc.calls)
	}
}

// Bulk PutNode dispatch is wired but always fails, same as a single
// put_node call (TestPutNodeIsNotSupported) — confirms the bulk path
// doesn't silently swallow that unsupported-verb error into a success.
func TestBulkMutateOpsPutIsNotSupported(t *testing.T) {
	setupMutateTest(t)

	storyURI := mustURL(t, "newsblur://story/"+sampleHash)
	result, err := Plugin{}.BulkMutate(context.Background(), cg.BulkRequest{
		Atomicity: cg.BulkBestEffort,
		Ops:       []cg.BulkOp{{Kind: cg.BulkPut, URI: storyURI}},
	})
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}
	if len(result.Failed) != 1 || result.Failed[0].URI.String() != storyURI.String() {
		t.Errorf("Failed = %v, want one failure for %s", result.Failed, storyURI)
	}
}

// Sweep resolves matches via ListRoots + Filter.Matches(node.Facets) —
// the same fold storyFacetCounts (facets.go) already does — rather than
// a separate EnrichedLister capability. The seeded sample story is
// unread by construction (tools.StoryRef zero value), so a read=unread
// filter over stories/ must find it.
func TestBulkMutateSweepMatchesAndApplies(t *testing.T) {
	_, fc := setupMutateTest(t)

	storyURI := mustURL(t, "newsblur://story/"+sampleHash)
	result, err := Plugin{}.BulkMutate(context.Background(), cg.BulkRequest{
		Atomicity: cg.BulkBestEffort,
		Sweep: &cg.BulkSweep{
			Root:   mustURL(t, "newsblur://stories"),
			Filter: cg.FacetFilter{{Dimension: facetRead, Value: "unread"}},
			Op:     cg.BulkOp{Kind: cg.BulkPatch, Body: []byte(`{"read":true}`)},
		},
	})
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}
	if len(result.AppliedNodes) != 1 || result.AppliedNodes[0].String() != storyURI.String() {
		t.Errorf("AppliedNodes = %v, want [%s]", result.AppliedNodes, storyURI)
	}
	if len(fc.calls) != 1 || fc.calls[0] != "MarkStoriesRead:"+sampleHash {
		t.Errorf("calls = %v, want [MarkStoriesRead:%s]", fc.calls, sampleHash)
	}
}

// A filter matching nothing yields an empty, error-free result — not a
// "root not found" or "nothing to sweep" error. No node's per-verb
// mutation ever fires.
func TestBulkMutateSweepNoMatches(t *testing.T) {
	_, fc := setupMutateTest(t)

	result, err := Plugin{}.BulkMutate(context.Background(), cg.BulkRequest{
		Atomicity: cg.BulkBestEffort,
		Sweep: &cg.BulkSweep{
			Root:   mustURL(t, "newsblur://stories"),
			Filter: cg.FacetFilter{{Dimension: facetRead, Value: "read"}},
			Op:     cg.BulkOp{Kind: cg.BulkPatch, Body: []byte(`{"read":true}`)},
		},
	})
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}
	if len(result.AppliedNodes) != 0 || len(result.Failed) != 0 || len(result.PatchedNothing) != 0 {
		t.Errorf("result = %+v, want an empty result", result)
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
	}
}
