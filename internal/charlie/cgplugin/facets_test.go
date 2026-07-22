package cgplugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/nebulous/internal/bravo/tools"
)

func TestFacetVersionStoriesTokensOnManifestMtime(t *testing.T) {
	fi := newFakeIndex()
	fi.manifestPath = filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(fi.manifestPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	index = fi
	t.Cleanup(func() { index = nil })

	ctx := context.Background()
	tok1, ok, err := Plugin{}.FacetVersion(ctx, mustURL(t, "newsblur://stories"))
	if err != nil || !ok {
		t.Fatalf("FacetVersion: ok=%v err=%v", ok, err)
	}

	tok1Again, ok, err := Plugin{}.FacetVersion(ctx, mustURL(t, "newsblur://stories"))
	if err != nil || !ok {
		t.Fatalf("FacetVersion repeat: ok=%v err=%v", ok, err)
	}
	if tok1 != tok1Again {
		t.Errorf("token unstable across an unchanged manifest: %q vs %q", tok1, tok1Again)
	}

	// Advance the manifest's mtime (Chtimes rather than a rewrite, so the
	// content stays trivial) and confirm the token moves.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(fi.manifestPath, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	tok2, ok, err := Plugin{}.FacetVersion(ctx, mustURL(t, "newsblur://stories"))
	if err != nil || !ok {
		t.Fatalf("FacetVersion after mtime bump: ok=%v err=%v", ok, err)
	}
	if tok2 == tok1 {
		t.Errorf("token did not move with the manifest mtime: %q", tok2)
	}
}

func TestFacetVersionStoriesNoManifestYet(t *testing.T) {
	fi := newFakeIndex()
	fi.manifestPath = filepath.Join(t.TempDir(), "manifest.json") // never written
	index = fi
	t.Cleanup(func() { index = nil })

	_, ok, err := Plugin{}.FacetVersion(context.Background(), mustURL(t, "newsblur://stories"))
	if err != nil {
		t.Fatalf("FacetVersion without a manifest: %v", err)
	}
	if ok {
		t.Error("ok = true against a nonexistent manifest, want false")
	}
}

func TestFacetVersionFeedTokensOnNT(t *testing.T) {
	fi := newFakeIndex()
	fi.feeds = []tools.FeedRef{{ID: "123", Title: "Example Feed", NT: 3}}
	index = fi
	t.Cleanup(func() { index = nil })

	tok, ok, err := Plugin{}.FacetVersion(context.Background(), mustURL(t, "newsblur://feed/123"))
	if err != nil || !ok {
		t.Fatalf("FacetVersion(feed): ok=%v err=%v", ok, err)
	}
	if tok != "3" {
		t.Errorf("token = %q, want \"3\" (the feed's NT)", tok)
	}

	fi.feeds[0].NT = 4
	tok2, ok, err := Plugin{}.FacetVersion(context.Background(), mustURL(t, "newsblur://feed/123"))
	if err != nil || !ok {
		t.Fatalf("FacetVersion(feed) after NT bump: ok=%v err=%v", ok, err)
	}
	if tok2 == tok {
		t.Errorf("token did not move with NT: %q", tok2)
	}
}

func TestFacetVersionUnknownFeedIsOkFalse(t *testing.T) {
	fi := newFakeIndex()
	index = fi
	t.Cleanup(func() { index = nil })

	_, ok, err := Plugin{}.FacetVersion(context.Background(), mustURL(t, "newsblur://feed/999"))
	if err != nil {
		t.Fatalf("FacetVersion(unknown feed): %v", err)
	}
	if ok {
		t.Error("ok = true for an unknown feed id, want false")
	}
}

// TestAgeBandDeclaration pins the RFC 0012 §11.3 obligations on the
// volatile dimension: nonzero RevalidateAfter, a closed domain covering
// every bucket, and declaration Orders consistent with the bucketing
// map (the literal exists for stable Values ordering; the map is the
// single source at lift time).
func TestAgeBandDeclaration(t *testing.T) {
	var dim *cg.FacetDimension
	for _, ntf := range (Plugin{}).DescribeFacets() {
		if ntf.Tag != typeStory {
			continue
		}
		for i := range ntf.Dimensions {
			if ntf.Dimensions[i].Key == facetAgeBand {
				dim = &ntf.Dimensions[i]
			}
		}
	}
	if dim == nil {
		t.Fatal("age_band not declared for the story type")
	}
	if dim.Kind != cg.FacetNumericBucket {
		t.Errorf("age_band kind = %q, want numeric-bucket", dim.Kind)
	}
	if dim.RevalidateAfter != ageBandRevalidateAfter {
		t.Errorf("RevalidateAfter = %v, want %v", dim.RevalidateAfter, ageBandRevalidateAfter)
	}
	if len(dim.Values) != len(ageBandOrder) {
		t.Fatalf("closed domain has %d values, want %d", len(dim.Values), len(ageBandOrder))
	}
	for _, v := range dim.Values {
		if want, ok := ageBandOrder[v.Key]; !ok || v.Order != want {
			t.Errorf("declared %q order %d inconsistent with ageBandOrder (%d, declared=%t)",
				v.Key, v.Order, want, ok)
		}
	}
}

// TestAgeBandOf pins the quantized bucketing: evaluation is against the
// host-local day start, and the domain totally partitions time relative
// to it (today / this-week / this-month / older).
func TestAgeBandOf(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.Local)

	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Date(2026, 7, 18, 8, 0, 0, 0, time.Local), ageBandToday},
		{time.Date(2026, 7, 18, 23, 0, 0, 0, time.Local), ageBandToday},
		{time.Date(2026, 7, 19, 0, 0, 0, 0, time.Local), ageBandToday}, // future: clamps to today
		{time.Date(2026, 7, 17, 9, 0, 0, 0, time.Local), ageBandThisWeek},
		{time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local), ageBandThisWeek}, // today-6: the week's edge
		{time.Date(2026, 7, 11, 0, 0, 0, 0, time.Local), ageBandThisMonth},
		{time.Date(2026, 6, 18, 0, 0, 0, 0, time.Local), ageBandThisMonth}, // today-30
		{time.Date(2026, 6, 17, 0, 0, 0, 0, time.Local), ageBandOlder},     // today-31
		{time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local), ageBandOlder},
		{time.Time{}, ""},
	}
	for _, c := range cases {
		key, order := ageBandOf(c.in, now)
		if key != c.want {
			t.Errorf("ageBandOf(%v) = %q, want %q", c.in, key, c.want)
			continue
		}
		if key != "" && order != ageBandOrder[key] {
			t.Errorf("ageBandOf(%v) order = %d, want %d", c.in, order, ageBandOrder[key])
		}
	}
}

// TestFacetCounts_AgeBandVolatile drives the volatile dimension end to
// end: stories bucket against the injected today, and the §11.3
// emission rule holds — every bucket key is present (informative
// zeros) because the summarized set contains stories, even though no
// story occupies "older".
func TestFacetCounts_AgeBandVolatile(t *testing.T) {
	prev := ageBandNow
	ageBandNow = func() time.Time {
		return time.Date(2026, 7, 18, 9, 30, 0, 0, time.Local)
	}
	t.Cleanup(func() { ageBandNow = prev })

	fi := newFakeIndex()
	fi.stories = []tools.StoryRef{
		{Hash: "1", FeedID: 1, Year: 2026, Date: time.Date(2026, 7, 18, 6, 0, 0, 0, time.Local)},
		{Hash: "2", FeedID: 1, Year: 2026, Date: time.Date(2026, 7, 15, 0, 0, 0, 0, time.Local)},
		{Hash: "3", FeedID: 1, Year: 2026, Date: time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)},
	}
	index = fi
	t.Cleanup(func() { index = nil })

	result, ok, err := Plugin{}.FacetCounts(context.Background(), mustURL(t, "newsblur://stories"), nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}

	assertFacetCount(t, result.Summary, facetAgeBand, ageBandToday, 1)
	assertFacetCount(t, result.Summary, facetAgeBand, ageBandThisWeek, 1)
	assertFacetCount(t, result.Summary, facetAgeBand, ageBandThisMonth, 1)
	// Informative zero: present, empty — the volatile expiry trigger.
	assertFacetCount(t, result.Summary, facetAgeBand, ageBandOlder, 0)
}

// TestFacetCounts_NoStoriesNoAgeBand pins the other half of the
// emission rule: a story-free subtree omits the volatile dimension
// entirely, so its memoized summary stays purely token-gated (the
// §11.3 cost containment).
func TestFacetCounts_NoStoriesNoAgeBand(t *testing.T) {
	fi := newFakeIndex()
	fi.stories = nil
	index = fi
	t.Cleanup(func() { index = nil })

	result, ok, err := Plugin{}.FacetCounts(context.Background(), mustURL(t, "newsblur://stories"), nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	if _, present := result.Summary[facetAgeBand]; present {
		t.Errorf("story-free summary carries age_band: %+v", result.Summary[facetAgeBand])
	}
}

// TestFacetCounts_ByContainerAttributesStoriesToFeeds pins RFC 0012 §13's
// adoption on the stories/ root: a multi-feed summary reports which feeds
// the matches came from, sorted by descending count (ties broken
// ascending URI, SortAndLimitContainerBreakdown's own rule).
func TestFacetCounts_ByContainerAttributesStoriesToFeeds(t *testing.T) {
	fi := newFakeIndex()
	fi.stories = []tools.StoryRef{
		{Hash: "1", FeedID: 1, Year: 2026},
		{Hash: "2", FeedID: 1, Year: 2026},
		{Hash: "3", FeedID: 2, Year: 2026},
	}
	index = fi
	t.Cleanup(func() { index = nil })

	result, ok, err := Plugin{}.FacetCounts(context.Background(), mustURL(t, "newsblur://stories"), nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}

	if len(result.ByContainer) != 2 {
		t.Fatalf("ByContainer = %+v, want 2 entries", result.ByContainer)
	}
	if got, want := result.ByContainer[0], (cg.FacetContainerBreakdown{URI: "newsblur://feed/1", Count: 2}); got != want {
		t.Errorf("ByContainer[0] = %+v, want %+v (highest count first)", got, want)
	}
	if got, want := result.ByContainer[1], (cg.FacetContainerBreakdown{URI: "newsblur://feed/2", Count: 1}); got != want {
		t.Errorf("ByContainer[1] = %+v, want %+v", got, want)
	}
	if result.ByContainerTruncated {
		t.Error("ByContainerTruncated = true for only 2 containers, want false")
	}
}

// TestFacetCounts_ByContainerRespectsFilter pins the RFC's attribution-scope
// rule: ByContainer counts only the (possibly filter-narrowed) matches
// Summary itself aggregates, not every story regardless of filter.
func TestFacetCounts_ByContainerRespectsFilter(t *testing.T) {
	fi := newFakeIndex()
	fi.stories = []tools.StoryRef{
		{Hash: "1", FeedID: 1, Year: 2026, Read: true},
		{Hash: "2", FeedID: 2, Year: 2026, Read: false},
	}
	index = fi
	t.Cleanup(func() { index = nil })

	filter := cg.FacetFilter{{Dimension: facetRead, Value: "unread"}}
	result, ok, err := Plugin{}.FacetCounts(context.Background(), mustURL(t, "newsblur://stories"), filter)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}

	if len(result.ByContainer) != 1 || result.ByContainer[0].URI != "newsblur://feed/2" {
		t.Errorf("ByContainer = %+v, want exactly [feed/2] (the read story's feed/1 must not appear)", result.ByContainer)
	}
}

// TestFacetCounts_FeedHasNoByContainer pins the single-container case
// (RFC 0012 §13): feed/{id} summarizes ITS OWN stories, so there is
// nothing further to attribute across -- ByContainer stays nil, matching
// caldav's own single-calendar case.
func TestFacetCounts_FeedHasNoByContainer(t *testing.T) {
	fi := newFakeIndex()
	fi.feeds = []tools.FeedRef{{ID: "1", Title: "Feed One"}}
	fi.feedStories = map[int][]tools.StoryRef{
		1: {{Hash: "1", FeedID: 1, Year: 2026}},
	}
	index = fi
	t.Cleanup(func() { index = nil })

	result, ok, err := Plugin{}.FacetCounts(context.Background(), mustURL(t, "newsblur://feed/1"), nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	if result.ByContainer != nil {
		t.Errorf("ByContainer = %+v, want nil (a single feed has nothing to attribute across)", result.ByContainer)
	}
}

// TestFacetCounts_TagHasNoByContainer pins that tag/{tag} does NOT
// populate ByContainer, unlike stories/: index.StoriesByTag narrows by a
// union match across user_tags and story_tags (two separate facet
// dimensions), which no single re-issued FacetFilter (AND-only) can
// reproduce against feed/{id} -- RFC 0012 §13's "every entry MUST be a
// working descend target" rule isn't satisfiable here, so this omits
// rather than approximates.
func TestFacetCounts_TagHasNoByContainer(t *testing.T) {
	fi := newFakeIndex()
	fi.tagStories = map[string][]tools.StoryRef{
		"golang": {
			{Hash: "1", FeedID: 1, Year: 2026, Tags: []string{"golang"}},
			{Hash: "2", FeedID: 2, Year: 2026, Tags: []string{"golang"}},
		},
	}
	index = fi
	t.Cleanup(func() { index = nil })

	result, ok, err := Plugin{}.FacetCounts(context.Background(), mustURL(t, "newsblur://tag/golang"), nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	if result.ByContainer != nil {
		t.Errorf("ByContainer = %+v, want nil (tag/{tag}'s union narrowing isn't filter-reproducible)", result.ByContainer)
	}
}

// TestFacetCounts_FeedsRootHasNoByContainer pins that feedFacetCounts was
// deliberately left untouched by the ByContainer adoption: there is no
// addressable, list_nodes-reachable container to break feeds down BY yet
// (folder nodes aren't listed by ListRoots -- see README's own caveat).
func TestFacetCounts_FeedsRootHasNoByContainer(t *testing.T) {
	fi := newFakeIndex()
	fi.feeds = []tools.FeedRef{{ID: "1", Title: "Feed One"}, {ID: "2", Title: "Feed Two"}}
	index = fi
	t.Cleanup(func() { index = nil })

	result, ok, err := Plugin{}.FacetCounts(context.Background(), mustURL(t, "newsblur://feeds"), nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	if result.ByContainer != nil {
		t.Errorf("ByContainer = %+v, want nil (no addressable container to break feeds down by)", result.ByContainer)
	}
}

// TestResolveFacetLabels_LiveFeedTakesPrecedence pins the common case:
// a currently-subscribed feed resolves from the live snapshot.
func TestResolveFacetLabels_LiveFeedTakesPrecedence(t *testing.T) {
	fi := newFakeIndex()
	fi.feeds = []tools.FeedRef{{ID: "1", Title: "Live Title"}}
	fi.seenFeedTitles = map[string]string{"1": "Stale Title"}
	index = fi
	t.Cleanup(func() { index = nil })

	labels, err := Plugin{}.ResolveFacetLabels(context.Background(), facetFeed, []string{"1"})
	if err != nil {
		t.Fatalf("ResolveFacetLabels: %v", err)
	}
	if got := labels["1"]; got != "Live Title" {
		t.Errorf("labels[1] = %q, want the live snapshot's title %q", got, "Live Title")
	}
}

// TestResolveFacetLabels_FallsBackToSeenFeedTitle pins nebulous#49: a
// feed id absent from the live subscription list (unsubscribed since)
// still resolves via SeenFeedTitle's accumulating registry.
func TestResolveFacetLabels_FallsBackToSeenFeedTitle(t *testing.T) {
	fi := newFakeIndex()
	fi.feeds = nil // no longer subscribed to anything
	fi.seenFeedTitles = map[string]string{"42": "Retired Feed"}
	index = fi
	t.Cleanup(func() { index = nil })

	labels, err := Plugin{}.ResolveFacetLabels(context.Background(), facetFeed, []string{"42", "999"})
	if err != nil {
		t.Fatalf("ResolveFacetLabels: %v", err)
	}
	if got := labels["42"]; got != "Retired Feed" {
		t.Errorf("labels[42] = %q, want the seen-titles fallback %q", got, "Retired Feed")
	}
	if _, present := labels["999"]; present {
		t.Errorf("labels[999] present, want absent (no live or seen title for it)")
	}
}

func assertFacetCount(
	t *testing.T,
	summary cg.FacetSummary,
	dimension, key string,
	want int64,
) {
	t.Helper()
	hist, ok := summary[dimension]
	if !ok {
		t.Errorf("dimension %q absent from summary", dimension)
		return
	}
	if got := hist[key]; got != want {
		t.Errorf("summary[%s][%s] = %d, want %d", dimension, key, got, want)
	}
}
