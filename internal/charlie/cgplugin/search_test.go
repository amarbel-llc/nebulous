package cgplugin

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/nebulous/internal/bravo/tools"
	deweyerrors "code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

func wordsIndex() *fakeIndex {
	f := newFakeIndex()
	f.wordsComplete = true
	f.wordHits = map[string][]tools.StoryRef{
		"pelican": {
			{Hash: "1:aaa", Title: "A", FeedID: 1, Year: 2024, UserTags: []string{"birds"}, Tags: []string{"nature"}},
			{Hash: "2:bbb", Title: "B", FeedID: 2, Year: 2023, Read: true, UserTags: []string{"birds"}},
			{Hash: "3:ccc", Title: "C", FeedID: 1, Year: 2023},
		},
	}
	return f
}

func hashesFrom(nodes []cg.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.URI.String()
	}
	return out
}

func TestSearchWordsScopesAndFilters(t *testing.T) {
	index = wordsIndex()
	t.Cleanup(func() { index = nil })

	cases := []struct {
		name   string
		root   string
		filter cg.FacetFilter
		limit  int
		want   []string
		full   bool
	}{
		{
			"stories root", "newsblur://stories", nil, 0,
			[]string{"newsblur://story/1:aaa", "newsblur://story/2:bbb", "newsblur://story/3:ccc"},
			true,
		},
		{
			"feeds root searches every story", "newsblur://feeds", nil, 0,
			[]string{"newsblur://story/1:aaa", "newsblur://story/2:bbb", "newsblur://story/3:ccc"},
			true,
		},
		{
			"one feed", "newsblur://feed/1", nil, 0,
			[]string{"newsblur://story/1:aaa", "newsblur://story/3:ccc"},
			true,
		},
		{
			"user tag", "newsblur://tag/birds", nil, 0,
			[]string{"newsblur://story/1:aaa", "newsblur://story/2:bbb"},
			true,
		},
		{
			"story tag", "newsblur://tag/nature", nil, 0,
			[]string{"newsblur://story/1:aaa"},
			true,
		},
		{
			"tags root = any tagged story", "newsblur://tags", nil, 0,
			[]string{"newsblur://story/1:aaa", "newsblur://story/2:bbb"},
			true,
		},
		{
			"one story", "newsblur://story/2:bbb", nil, 0,
			[]string{"newsblur://story/2:bbb"},
			true,
		},
		{"a leaf has no story containers", "newsblur://story/2:bbb/content", nil, 0, []string{}, true},
		{
			"facet filter year", "newsblur://stories",
			cg.FacetFilter{{Dimension: facetYear, Value: "2023"}},
			0,
			[]string{"newsblur://story/2:bbb", "newsblur://story/3:ccc"},
			true,
		},
		{
			"facet filter read", "newsblur://stories",
			cg.FacetFilter{{Dimension: facetRead, Value: "unread"}, {Dimension: facetFeed, Value: "1"}},
			0,
			[]string{"newsblur://story/1:aaa", "newsblur://story/3:ccc"},
			true,
		},
		{
			"limit truncates and marks incomplete", "newsblur://stories", nil, 2,
			[]string{"newsblur://story/1:aaa", "newsblur://story/2:bbb"},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Plugin{}.Search(context.Background(), cg.SearchRequest{
				Root: mustURL(t, tc.root), Query: "pelican", Mode: cg.SearchWords,
				Filter: tc.filter, Limit: tc.limit,
			})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if got := hashesFrom(res.Nodes); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("nodes = %v, want %v", got, tc.want)
			}
			if res.Complete != tc.full {
				t.Errorf("Complete = %v, want %v", res.Complete, tc.full)
			}
			for _, n := range res.Nodes {
				if n.Type != typeStory {
					t.Errorf("node %s type = %q, want %q", n.URI, n.Type, typeStory)
				}
			}
		})
	}
}

func TestSearchWordsIncompleteIndexIsReported(t *testing.T) {
	f := wordsIndex()
	f.wordsComplete = false
	index = f
	t.Cleanup(func() { index = nil })

	res, err := Plugin{}.Search(context.Background(), cg.SearchRequest{
		Root: mustURL(t, "newsblur://stories"), Query: "pelican", Mode: cg.SearchWords,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Complete || len(res.Nodes) != 3 {
		t.Errorf("Complete = %v, nodes = %d; want incomplete with the 3 hits", res.Complete, len(res.Nodes))
	}
}

func TestSearchDeclinesOtherModes(t *testing.T) {
	index = wordsIndex()
	t.Cleanup(func() { index = nil })

	for _, mode := range []cg.SearchMode{cg.SearchSubstring, cg.SearchRegex} {
		_, err := Plugin{}.Search(context.Background(), cg.SearchRequest{
			Root: mustURL(t, "newsblur://stories"), Query: "pelican", Mode: mode,
		})
		if !errors.Is(err, cg.ErrSearchModeUnsupported) {
			t.Errorf("mode %q: err = %v, want ErrSearchModeUnsupported", mode, err)
		}
	}
	if q := index.(*fakeIndex).wordQueries; len(q) != 0 {
		t.Errorf("declined modes still hit the word index: %v", q)
	}
}

func TestSearchWordsErrors(t *testing.T) {
	f := wordsIndex()
	index = f
	t.Cleanup(func() { index = nil })
	req := cg.SearchRequest{Root: mustURL(t, "newsblur://stories"), Query: "pelican", Mode: cg.SearchWords}

	f.wordsErr = tools.ErrWordIndexUnavailable
	if _, err := (Plugin{}).Search(context.Background(), req); !errors.Is(err, cg.ErrSearchModeUnsupported) {
		t.Errorf("no index: err = %v, want ErrSearchModeUnsupported so cutting-garden reports the gap", err)
	}

	f.wordsErr = fmt.Errorf("%q is %w: details", "the", tools.ErrNotIndexedWord)
	err := func() error { _, e := Plugin{}.Search(context.Background(), req); return e }()
	if err == nil || errors.Is(err, cg.ErrSearchModeUnsupported) {
		t.Errorf("bad query: err = %v, want an error that fails the query loudly", err)
	}
	if !deweyerrors.Is400BadRequest(err) {
		t.Errorf("bad query: err = %v, want a bad request (-32602), not an internal error", err)
	}

	f.wordsErr = errors.New("disk exploded")
	if _, err := (Plugin{}).Search(context.Background(), req); err == nil || deweyerrors.Is400BadRequest(err) {
		t.Errorf("genuine failure: err = %v, want a non-bad-request error", err)
	}
	f.wordsErr = nil

	badFeed := cg.SearchRequest{Root: mustURL(t, "newsblur://feed/notanumber"), Query: "pelican", Mode: cg.SearchWords}
	if _, err := (Plugin{}).Search(context.Background(), badFeed); err == nil || !deweyerrors.Is400BadRequest(err) {
		t.Errorf("invalid feed id: err = %v, want a bad request", err)
	}

	index = nil
	if _, err := (Plugin{}).Search(context.Background(), req); err == nil {
		t.Error("uninitialized index accepted")
	}
}
