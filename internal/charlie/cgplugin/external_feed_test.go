package cgplugin

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"code.linenisgreat.com/nebulous/internal/bravo/tools"
)

// withExternalStory gives the fake index one saved-from-a-URL story: its feed
// id is 0, a feed NewsBlur never lists among the subscriptions.
func withExternalStory(fi *fakeIndex) {
	story := tools.StoryRef{Hash: "0:45788b", Title: "Saved page", FeedID: 0}
	fi.stories = append(fi.stories, story)
	fi.feedStories[0] = []tools.StoryRef{story}
}

func feedNodeNames(t *testing.T) map[string]string {
	t.Helper()
	nodes, err := Plugin{}.ListRoots(context.Background(), mustURL(t, "newsblur://feeds"))
	if err != nil {
		t.Fatalf("ListRoots(feeds): %v", err)
	}
	names := map[string]string{}
	for _, n := range nodes {
		names[n.URI.String()] = n.Name
	}
	return names
}

func TestFeedsRootListsExternalStoriesAsFeedZero(t *testing.T) {
	fi := newFakeIndex()
	withExternalStory(fi)
	index = fi
	t.Cleanup(func() { index = nil })

	got := feedNodeNames(t)
	if got["newsblur://feed/0"] != externalFeedTitle {
		t.Errorf("feeds root = %v, want newsblur://feed/0 named %q", got, externalFeedTitle)
	}
	if got["newsblur://feed/123"] != "Example Feed" {
		t.Errorf("feeds root = %v, lost the real subscription", got)
	}
}

func TestFeedsRootOmitsFeedZeroWithoutExternalStories(t *testing.T) {
	index = newFakeIndex()
	t.Cleanup(func() { index = nil })

	if _, ok := feedNodeNames(t)["newsblur://feed/0"]; ok {
		t.Error("feed/0 listed although no story has feed id 0")
	}
}

func TestFeedZeroIsListedByEnrichedListingAndCountedInFacets(t *testing.T) {
	fi := newFakeIndex()
	withExternalStory(fi)
	index = fi
	t.Cleanup(func() { index = nil })
	ctx := context.Background()

	enriched, ok, err := Plugin{}.ListEnriched(ctx, mustURL(t, "newsblur://feeds"), nil)
	if err != nil || !ok {
		t.Fatalf("ListEnriched: ok=%v err=%v", ok, err)
	}
	if len(enriched) != 2 {
		t.Errorf("ListEnriched(feeds) = %d nodes, want 2 (the subscription and feed 0)", len(enriched))
	}

	// FacetCounts must agree with the listing (RFC 0012): two feeds. The
	// subscription in the fake carries no Active flag; feed 0 is active.
	result, ok, err := Plugin{}.FacetCounts(ctx, mustURL(t, "newsblur://feeds"), nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	assertFacetCount(t, result.Summary, facetActive, "active", 1)
	assertFacetCount(t, result.Summary, facetActive, "inactive", 1)
}

func TestResolveFacetLabelsNamesFeedZero(t *testing.T) {
	fi := newFakeIndex()
	withExternalStory(fi)
	index = fi
	t.Cleanup(func() { index = nil })

	labels, err := Plugin{}.ResolveFacetLabels(context.Background(), facetFeed, []string{"0", "123"})
	if err != nil {
		t.Fatalf("ResolveFacetLabels: %v", err)
	}
	if got := labels["0"]; got != externalFeedTitle {
		t.Errorf("labels[0] = %q, want %q", got, externalFeedTitle)
	}
	if got := labels["123"]; got != "Example Feed" {
		t.Errorf("labels[123] = %q, want the real feed's title", got)
	}
}

func TestFacetVersionOfFeedZeroTokensOnTheManifest(t *testing.T) {
	fi := newFakeIndex()
	withExternalStory(fi)
	fi.manifestPath = filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(fi.manifestPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	index = fi
	t.Cleanup(func() { index = nil })

	tok, ok, err := Plugin{}.FacetVersion(context.Background(), mustURL(t, "newsblur://feed/0"))
	if err != nil || !ok || tok == "" {
		t.Errorf("FacetVersion(feed/0) = %q, ok=%v, err=%v; want a manifest token", tok, ok, err)
	}
}

// feed 0 is a display grouping, not a subscription: listing it must never
// make it a valid target for the feed writes (rename, move, unsubscribe).
func TestFeedZeroCannotBeMutatedAsAFeed(t *testing.T) {
	fi, fc := setupMutateTest(t)
	withExternalStory(fi)

	if err := (Plugin{}).DeleteNode(context.Background(), mustURL(t, "newsblur://feed/0")); err == nil {
		t.Error("DeleteNode(feed/0): expected an error, not an unsubscribe")
	}
	if _, err := (Plugin{}).PatchNode(context.Background(), mustURL(t, "newsblur://feed/0"), bytes.NewReader([]byte(`{"title":"x"}`))); err == nil {
		t.Error("PatchNode(feed/0): expected an error, not a rename")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
	}
}
