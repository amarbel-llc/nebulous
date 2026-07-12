package cgplugin

import (
	"context"
	"fmt"
	"net/url"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
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
	if len(segs) < 3 {
		return cg.LeafContent{}, false, nil
	}

	switch segs[0] {
	case "story":
		return readStoryLeaf(segs[1], segs[2:])
	case "feed":
		if len(segs) != 3 {
			return cg.LeafContent{}, false, nil
		}
		return readFeedLeaf(ctx, segs[1], segs[2])
	default:
		return cg.LeafContent{}, false, nil
	}
}

// readStoryLeaf dispatches on tail, the path segments after
// story/{hash}/: a single segment for content/original/metadata, or
// ["capture", format] for a capture leaf.
func readStoryLeaf(hash string, tail []string) (cg.LeafContent, bool, error) {
	if len(tail) == 2 && tail[0] == "capture" {
		return readStoryCaptureLeaf(hash, tail[1])
	}
	if len(tail) != 1 {
		return cg.LeafContent{}, false, nil
	}

	switch tail[0] {
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

// readStoryCaptureLeaf returns the completed capture record for
// (hash, format) — the receipt markl-id and when it was captured.
// Deliberately does not walk the receipt's RFC-0002 merkle tree itself
// (hyphence-parsing, typed blob refs) — that's cutting-garden/madder's
// job; a consumer resolves the receipt id further via
// madder://blobs/<digest>, the same mechanism used throughout this
// environment's own tooling.
func readStoryCaptureLeaf(hash, format string) (cg.LeafContent, bool, error) {
	rec, ok := index.CaptureRecord(hash, format)
	if !ok {
		return cg.LeafContent{}, false, nil
	}
	return cg.LeafContent{
		Structured:  rec,
		RawMimeType: jsonMime,
	}, true, nil
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
