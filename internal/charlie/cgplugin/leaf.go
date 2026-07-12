package cgplugin

import (
	"context"
	"fmt"
	"net/url"

	cg "github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

// ReadLeaf fetches a leaf's body: a story's content/original/metadata,
// or a feed's metadata. Any other node — a populated container, or an
// uncached story — returns ok=false so the consumer falls back to the
// child listing.
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
	if len(segs) != 3 {
		return cg.LeafContent{}, false, nil
	}

	switch segs[0] {
	case "story":
		return readStoryLeaf(segs[1], segs[2])
	case "feed":
		return readFeedLeaf(ctx, segs[1], segs[2])
	default:
		return cg.LeafContent{}, false, nil
	}
}

func readStoryLeaf(hash, tier string) (cg.LeafContent, bool, error) {
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
	case "metadata":
		view, raw, ok := index.StoryMetadata(hash)
		if !ok {
			return cg.LeafContent{}, false, nil
		}
		return cg.LeafContent{
			Structured:  view,
			Raw:         raw,
			RawMimeType: jsonMime,
		}, true, nil
	default:
		return cg.LeafContent{}, false, nil
	}
}

func readFeedLeaf(ctx context.Context, id, tier string) (cg.LeafContent, bool, error) {
	if tier != "metadata" {
		return cg.LeafContent{}, false, nil
	}
	view, raw, ok := index.FeedMetadata(ctx, id)
	if !ok {
		return cg.LeafContent{}, false, nil
	}
	return cg.LeafContent{
		Structured:  view,
		Raw:         raw,
		RawMimeType: jsonMime,
	}, true, nil
}
