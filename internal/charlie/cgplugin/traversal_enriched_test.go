package cgplugin

import (
	"context"
	"testing"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/nebulous/internal/bravo/tools"
)

func TestListEnrichedNotInitialized(t *testing.T) {
	index = nil
	_, ok, err := Plugin{}.ListEnriched(context.Background(), mustURL(t, "newsblur://stories"), nil)
	if err == nil || ok {
		t.Fatalf("ListEnriched with no index: ok=%v err=%v, want an error", ok, err)
	}
}

// ListEnriched's scope mirrors FacetCounts exactly: stories/feed/{id}/
// tag/{tag} are enrichable, with the SAME Facets ListRoots already
// populates, filtered.
func TestListEnrichedFiltersStories(t *testing.T) {
	fi := newFakeIndex()
	fi.stories = []tools.StoryRef{
		{Hash: "1", FeedID: 1, Year: 2026, Read: true},
		{Hash: "2", FeedID: 1, Year: 2026, Read: false},
	}
	index = fi
	t.Cleanup(func() { index = nil })

	filter := cg.FacetFilter{{Dimension: facetRead, Value: "unread"}}
	nodes, ok, err := Plugin{}.ListEnriched(context.Background(), mustURL(t, "newsblur://stories"), filter)
	if err != nil || !ok {
		t.Fatalf("ListEnriched: ok=%v err=%v", ok, err)
	}
	if len(nodes) != 1 || nodes[0].URI.String() != "newsblur://story/2" {
		t.Errorf("nodes = %v, want exactly [story/2] (the unread one)", nodes)
	}
}

func TestListEnrichedFeeds(t *testing.T) {
	fi := newFakeIndex()
	fi.feeds = []tools.FeedRef{
		{ID: "1", Title: "Active Feed", Active: true},
		{ID: "2", Title: "Inactive Feed", Active: false},
	}
	index = fi
	t.Cleanup(func() { index = nil })

	filter := cg.FacetFilter{{Dimension: facetActive, Value: "active"}}
	nodes, ok, err := Plugin{}.ListEnriched(context.Background(), mustURL(t, "newsblur://feeds"), filter)
	if err != nil || !ok {
		t.Fatalf("ListEnriched: ok=%v err=%v", ok, err)
	}
	if len(nodes) != 1 || nodes[0].URI.String() != "newsblur://feed/1" {
		t.Errorf("nodes = %v, want exactly [feed/1]", nodes)
	}
}

// A story's own leaf listing is not a facet-bearing container -- decline
// (ok=false, no error) rather than returning its (facet-less) leaves as
// if they were filterable matches. This is the case
// BulkBestEffortSweep's write-safety refusal depends on.
func TestListEnrichedDeclinesStoryLeaves(t *testing.T) {
	setupMutateTest(t)

	_, ok, err := Plugin{}.ListEnriched(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), nil)
	if err != nil {
		t.Fatalf("ListEnriched: unexpected error %v", err)
	}
	if ok {
		t.Error("ListEnriched on story/{hash}: ok = true, want false (decline)")
	}
}

// "tags" (the tag-LISTING container) declines too, matching
// DescribeFacets: tag nodes carry no facets of their own to filter by --
// distinct from "tag/{tag}", which enriches the tag's member stories.
func TestListEnrichedDeclinesTagsRoot(t *testing.T) {
	fi := newFakeIndex()
	index = fi
	t.Cleanup(func() { index = nil })

	_, ok, err := Plugin{}.ListEnriched(context.Background(), mustURL(t, "newsblur://tags"), nil)
	if err != nil {
		t.Fatalf("ListEnriched: unexpected error %v", err)
	}
	if ok {
		t.Error("ListEnriched on tags: ok = true, want false (decline)")
	}
}
