package cgplugin

import (
	"context"
	"fmt"
	"net/url"

	cg "github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

// ReadLeaf fetches a story leaf's body. Only the two per-story leaves are
// fetchable: story/{hash}/content (the cached summary content) and
// story/{hash}/original (the cached original article text). Any other
// node — a populated container, or an uncached story — returns ok=false
// so the consumer falls back to the (empty) child listing.
func (Plugin) ReadLeaf(
	ctx context.Context, node *url.URL,
) (cg.LeafContent, bool, error) {
	if node == nil {
		return cg.LeafContent{}, false, fmt.Errorf("newsblur plugin: ReadLeaf requires a node URI")
	}
	if index == nil {
		return cg.LeafContent{}, false, fmt.Errorf("newsblur plugin: index not initialized")
	}

	segs := pathSegments(node)
	if len(segs) != 3 || segs[0] != "story" {
		return cg.LeafContent{}, false, nil
	}
	hash, tier := segs[1], segs[2]

	switch tier {
	case "content":
		view, raw, ok := index.StoryContent(hash)
		if !ok {
			return cg.LeafContent{}, false, nil
		}
		return cg.LeafContent{
			Structured:  view,
			Raw:         raw,
			RawMimeType: htmlMime,
		}, true, nil
	case "original":
		raw, ok := index.StoryOriginal(hash)
		if !ok {
			return cg.LeafContent{}, false, nil
		}
		return cg.LeafContent{
			Raw:         raw,
			RawMimeType: htmlMime,
		}, true, nil
	default:
		return cg.LeafContent{}, false, nil
	}
}
