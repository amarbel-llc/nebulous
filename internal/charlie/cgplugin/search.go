package cgplugin

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/nebulous/internal/bravo/tools"
)

// Search serves cutting-garden's RFC 0016 search for newsblur://, in WORDS
// mode only (trellis's `_words` field). Substring and regex are declined
// (cg.ErrSearchModeUnsupported): `_body*=` is exact-case substring over raw
// leaf bytes, which nebulous's word index cannot answer honestly, so a
// `_body` query keeps walking.
//
// What a words search means here (the tokenizer is nebulous's own, shared
// with the story_query tool's word search):
//
//   - Text covered: each story's title, its summary content, and its cached
//     original article text, with HTML tags stripped. Only these entities
//     are decoded first: &amp; &lt; &gt; &quot; &apos; &#39; &nbsp; and
//     numeric ones (&#233;, &#xe9;); any other named entity (&eacute;) is
//     left as text and so splits or garbles the word around it. One index
//     entry per story: a match does not say which of those texts the word
//     appeared in.
//   - Match: the query must be a single word. It is lowercased and has its
//     accents removed (NFD, combining marks dropped), then looked up as a
//     whole word; "Café", "cafe" and "CAFÉ" are the same word. Hyphens
//     inside a word are kept ("machine-learning" is a word, and also
//     indexes "machine" and "learning").
//   - Not indexed, therefore not matchable: words shorter than 3 characters,
//     purely numeric words, and these stop words: the and for with from that
//     this are was but not you all can had has have its our will www com org
//     net http https. A query that is not exactly one indexed word (a phrase,
//     a stop word, "go") is a bad-request error naming why, never a silent
//     empty result.
//   - Nodes returned: story containers (newsblur-story-v1) only, never their
//     content/original leaves. Scope is the request Root: feeds and stories
//     search every story, feed/{id} that feed's stories, tag/{tag} the
//     stories carrying that user or feed tag, story/{hash} that one story;
//     any other Root (a leaf, a folder, a feed's metadata) contains no story
//     containers, so the answer is an empty complete result.
//   - Facets: the request's FacetFilter narrows the hits by the same facet
//     values the nodes carry when listed.
//   - Completeness: complete when the on-disk index covers exactly the
//     stories now in the corpus. After a fetch added stories but before the
//     index is rebuilt it is incomplete (the hits are right, newer stories
//     may be missing); cutting-garden then marks the query partial. With no
//     usable index at all, Search declines words mode.
func (Plugin) Search(ctx context.Context, req cg.SearchRequest) (cg.SearchResult, error) {
	if req.Mode != cg.SearchWords {
		return cg.SearchResult{}, fmt.Errorf(
			"%w: newsblur answers only words searches (`_words`); `_body` substring/regex queries are not accelerated here",
			cg.ErrSearchModeUnsupported)
	}
	if req.Root == nil {
		return cg.SearchResult{}, fmt.Errorf("newsblur plugin: Search requires a root URI")
	}
	if req.Query == "" {
		return cg.SearchResult{}, fmt.Errorf("newsblur plugin: Search requires a non-empty query")
	}
	if index == nil {
		return cg.SearchResult{}, fmt.Errorf("newsblur plugin: index not initialized")
	}
	if err := ctx.Err(); err != nil {
		return cg.SearchResult{}, err
	}

	inScope, err := searchScope(pathSegments(req.Root))
	if err != nil {
		return cg.SearchResult{}, err
	}

	hits, complete, err := index.SearchWords(req.Query)
	if err != nil {
		if errors.Is(err, tools.ErrWordIndexUnavailable) {
			return cg.SearchResult{}, fmt.Errorf("%w: %v", cg.ErrSearchModeUnsupported, err)
		}
		return cg.SearchResult{}, err
	}

	var refs []tools.StoryRef
	for _, s := range hits {
		if inScope(s) {
			refs = append(refs, s)
		}
	}
	nodes, err := storyNodes(refs, nil)
	if err != nil {
		return cg.SearchResult{}, err
	}

	matched := make([]cg.Node, 0, len(nodes))
	for _, n := range nodes {
		if req.Filter.Matches(n.Facets) {
			matched = append(matched, n)
		}
	}
	if req.Limit > 0 && len(matched) > req.Limit {
		matched = matched[:req.Limit]
		complete = false
	}
	return cg.SearchResult{Nodes: matched, Complete: complete}, nil
}

// searchScope turns a Root's path segments into a predicate over stories.
func searchScope(segs []string) (func(tools.StoryRef) bool, error) {
	all := func(tools.StoryRef) bool { return true }
	none := func(tools.StoryRef) bool { return false }
	switch {
	case len(segs) == 1 && (segs[0] == "stories" || segs[0] == "feeds"):
		return all, nil
	case len(segs) == 1 && segs[0] == "tags":
		return func(s tools.StoryRef) bool { return len(s.UserTags)+len(s.Tags) > 0 }, nil
	case len(segs) == 2 && segs[0] == "feed":
		id, err := strconv.Atoi(segs[1])
		if err != nil {
			return nil, fmt.Errorf("newsblur plugin: invalid feed id %q: %w", segs[1], err)
		}
		return func(s tools.StoryRef) bool { return s.FeedID == id }, nil
	case len(segs) == 2 && segs[0] == "tag":
		tag := segs[1]
		return func(s tools.StoryRef) bool { return hasTag(s, tag) }, nil
	case len(segs) == 2 && segs[0] == "story":
		hash := segs[1]
		return func(s tools.StoryRef) bool { return s.Hash == hash }, nil
	default:
		return none, nil
	}
}

// hasTag mirrors ReadIndex.StoriesByTag: a story carries a tag as a user
// tag or as a feed-assigned story tag.
func hasTag(s tools.StoryRef, tag string) bool {
	for _, t := range s.UserTags {
		if t == tag {
			return true
		}
	}
	for _, t := range s.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
