package cgplugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/nebulous/internal/bravo/tools"
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
			{Kind: cg.BulkPatch, URI: storyURI, Body: []byte(`{"user_tags":["a","b"],"force":true}`)},
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

// seedStories adds stories to the fake index (feed 123) so a sweep has
// several matches.
func seedStories(fi *fakeIndex, hashes ...string) {
	for _, h := range hashes {
		fi.stories = append(fi.stories, tools.StoryRef{Hash: h, Title: h})
		fi.storyMetadata[h] = storyMetadataEntry{
			view: tools.StoryMetadataView{Hash: h, Title: h},
			raw:  []byte(`{"story_hash":"` + h + `","story_title":"` + h + `"}`),
		}
	}
}

// mutate_nodes (cutting-garden#315) sends one batch whose items fail
// independently: a changeset with a failing item in the MIDDLE must still
// apply the ones after it, and report the failure per item, not as a
// returned error.
func TestBulkMutateChangesetOneFailureDoesNotStopTheRest(t *testing.T) {
	fi, fc := setupMutateTest(t)
	seedStories(fi, "123:b", "123:c")
	fc.failFor = map[string]error{"MarkStoriesRead:123:b": errors.New("upstream said no")}

	uri := func(h string) *url.URL { return mustURL(t, "newsblur://story/"+h) }
	result, err := Plugin{}.BulkMutate(context.Background(), cg.BulkRequest{
		Atomicity: cg.BulkBestEffort,
		Ops: []cg.BulkOp{
			{Kind: cg.BulkPatch, URI: uri("123:abc"), Body: []byte(`{"read":true}`)},
			{Kind: cg.BulkPatch, URI: uri("123:b"), Body: []byte(`{"read":true}`)},
			{Kind: cg.BulkPatch, URI: uri("123:c"), Body: []byte(`{"read":true}`)},
		},
	})
	if err != nil {
		t.Fatalf("BulkMutate returned an error for a per-item failure: %v", err)
	}
	assertURIs(t, "AppliedNodes", result.AppliedNodes, "newsblur://story/123:abc", "newsblur://story/123:c")
	if len(result.Failed) != 1 || result.Failed[0].URI.String() != "newsblur://story/123:b" ||
		!strings.Contains(result.Failed[0].Err, "upstream said no") {
		t.Errorf("Failed = %+v, want one failure for story 123:b carrying the upstream error", result.Failed)
	}
}

// Same contract for a sweep: the failing match is reported per item and
// the matches after it still apply.
func TestBulkMutateSweepOneFailureDoesNotStopTheRest(t *testing.T) {
	fi, fc := setupMutateTest(t)
	seedStories(fi, "123:b", "123:c")
	fc.failFor = map[string]error{"MarkStoriesRead:123:b": errors.New("upstream said no")}

	result, err := Plugin{}.BulkMutate(context.Background(), cg.BulkRequest{
		Atomicity: cg.BulkBestEffort,
		Sweep: &cg.BulkSweep{
			Root:   mustURL(t, "newsblur://stories"),
			Filter: cg.FacetFilter{{Dimension: facetRead, Value: "unread"}},
			Op:     cg.BulkOp{Kind: cg.BulkPatch, Body: []byte(`{"read":true}`)},
		},
	})
	if err != nil {
		t.Fatalf("BulkMutate returned an error for a per-item failure: %v", err)
	}
	if len(result.AppliedNodes) != 2 {
		t.Errorf("AppliedNodes = %v, want the 2 stories other than 123:b", result.AppliedNodes)
	}
	if len(result.Failed) != 1 || result.Failed[0].URI.String() != "newsblur://story/123:b" {
		t.Errorf("Failed = %+v, want exactly story 123:b", result.Failed)
	}
	if len(fc.calls) != 3 {
		t.Errorf("calls = %v, want one MarkStoriesRead per match (3)", fc.calls)
	}
}

// A caller mistake inside a bulk item (unknown tag-edit shape here: a
// user_tags replace without force) is that item's failure, with the
// bad-request text preserved, not a whole-call error.
func TestBulkMutateCallerMistakeIsPerItem(t *testing.T) {
	fi, fc := setupMutateTest(t)
	seedStories(fi, "123:b")

	result, err := Plugin{}.BulkMutate(context.Background(), cg.BulkRequest{
		Atomicity: cg.BulkBestEffort,
		Ops: []cg.BulkOp{
			{Kind: cg.BulkPatch, URI: mustURL(t, "newsblur://story/123:abc"), Body: []byte(`{"user_tags":["x"]}`)},
			{Kind: cg.BulkPatch, URI: mustURL(t, "newsblur://story/123:b"), Body: []byte(`{"read":true}`)},
		},
	})
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}
	if len(result.Failed) != 1 || result.Failed[0].URI.String() != "newsblur://story/123:abc" {
		t.Errorf("Failed = %+v, want the un-forced user_tags replace", result.Failed)
	}
	assertURIs(t, "AppliedNodes", result.AppliedNodes, "newsblur://story/123:b")
	if len(fc.calls) != 1 || fc.calls[0] != "MarkStoriesRead:123:b" {
		t.Errorf("calls = %v, want only the valid item to reach the client", fc.calls)
	}
}

// A temp-id create (subscribe: NewsBlur assigns the feed id) goes through
// ContainerCreator.CreateChild, is reported in Created, and a later op can
// address it by Ref. A failing subscribe is that item's failure and the
// dependent op fails without reaching the client.
func TestBulkMutateTempIDSubscribeAndRef(t *testing.T) {
	fi := newFakeIndex()
	fc := &subscribeFakeClient{
		subscribeResp: json.RawMessage(`{"code":1,"message":"","feed":{"id":42}}`),
	}
	fi.feedMetadata["42"] = feedMetadataEntry{
		view: tools.FeedMetadataView{ID: "42", Title: "New Feed"},
		raw:  json.RawMessage(`{"id":42,"feed_title":"New Feed"}`),
	}
	index, client = fi, fc
	t.Cleanup(func() { index, client = nil, nil })

	feeds := mustURL(t, "newsblur://feeds")
	result, err := Plugin{}.BulkMutate(context.Background(), cg.BulkRequest{
		Atomicity: cg.BulkBestEffort,
		Ops: []cg.BulkOp{
			{Kind: cg.BulkCreate, TempID: "f", In: feeds, Type: typeFeed, Body: []byte(`{"url":"https://example.com/feed"}`)},
			{Kind: cg.BulkPatch, Ref: "f", Body: []byte(`{"title":"Renamed"}`)},
		},
	})
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}
	if len(result.Created) != 1 || result.Created[0].TempID != "f" || result.Created[0].URI.String() != "newsblur://feed/42" {
		t.Errorf("Created = %+v, want f -> newsblur://feed/42", result.Created)
	}
	if len(result.Failed) != 0 {
		t.Errorf("Failed = %+v, want none", result.Failed)
	}

	fc.calls = nil
	fc.subscribeResp = json.RawMessage(`{"code":-1,"message":"not a feed"}`)
	result, err = Plugin{}.BulkMutate(context.Background(), cg.BulkRequest{
		Atomicity: cg.BulkBestEffort,
		Ops: []cg.BulkOp{
			{Kind: cg.BulkCreate, TempID: "f", In: feeds, Type: typeFeed, Body: []byte(`{"url":"https://example.com/nope"}`)},
			{Kind: cg.BulkPatch, Ref: "f", Body: []byte(`{"title":"Renamed"}`)},
		},
	})
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}
	if len(result.Failed) != 2 || len(result.Created) != 0 || len(result.AppliedNodes) != 0 {
		t.Errorf("result = %+v, want both items failed (subscribe, then its dependent)", result)
	}
	if len(fc.calls) != 1 {
		t.Errorf("calls = %v, want only the failed Subscribe", fc.calls)
	}
}

func assertURIs(t *testing.T, name string, got []*url.URL, want ...string) {
	t.Helper()
	var gs []string
	for _, u := range got {
		gs = append(gs, u.String())
	}
	if strings.Join(gs, " ") != strings.Join(want, " ") {
		t.Errorf("%s = %v, want %v", name, gs, want)
	}
}

// A sweep whose Root isn't a facet-bearing container (ListEnriched
// declines) REFUSES with an error rather than silently sweeping zero
// nodes -- cutting-garden#197's shared write-safety contract: a mutation
// must never treat "I don't serve this" the same as "nothing matched."
func TestBulkMutateSweepDeclinedRootRefuses(t *testing.T) {
	_, fc := setupMutateTest(t)

	_, err := Plugin{}.BulkMutate(context.Background(), cg.BulkRequest{
		Atomicity: cg.BulkBestEffort,
		Sweep: &cg.BulkSweep{
			Root: mustURL(t, "newsblur://story/"+sampleHash),
			Op:   cg.BulkOp{Kind: cg.BulkPatch, Body: []byte(`{"read":true}`)},
		},
	})
	if err == nil {
		t.Fatal("BulkMutate sweeping a declined root: expected an error, got nil")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
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
