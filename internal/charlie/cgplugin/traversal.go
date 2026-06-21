package cgplugin

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	cg "github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/friedenberg/nebulous/internal/bravo/tools"
)

const (
	typeFeed          = "newsblur-feed-v1"
	typeTag           = "newsblur-tag-v1"
	typeStory         = "newsblur-story-v1"
	typeStoryContent  = "newsblur-story-content-v1"
	typeStoryOriginal = "newsblur-story-original-v1"
)

const htmlMime = "text/html; charset=utf-8"

// Types declares every node type this plugin emits. Feeds, tags, and
// stories are containers; a story's content and original are leaves.
func (Plugin) Types() []cg.NodeType {
	return []cg.NodeType{
		{Tag: typeFeed, Container: true},
		{Tag: typeTag, Container: true},
		{Tag: typeStory, Container: true},
		{Tag: typeStoryContent, Container: false, MimeType: htmlMime},
		{Tag: typeStoryOriginal, Container: false, MimeType: htmlMime},
	}
}

// Roots returns the plugin's top-level entry points: the feed list, the
// starred-story corpus, and the tag dictionary.
func (Plugin) Roots(context.Context) ([]*url.URL, error) {
	return []*url.URL{
		nodeURL("feeds"),
		nodeURL("stories"),
		nodeURL("tags"),
	}, nil
}

// ListRoots returns the immediate children of node. Roots fan out to
// their member nodes; a feed/tag fans out to its stories; a story fans
// out to its content + original leaves; a leaf has no children.
func (Plugin) ListRoots(ctx context.Context, node *url.URL) ([]cg.Node, error) {
	if node == nil {
		return nil, fmt.Errorf("newsblur plugin: ListRoots requires a node URI")
	}
	if index == nil {
		return nil, fmt.Errorf("newsblur plugin: index not initialized")
	}

	segs := pathSegments(node)
	switch {
	case len(segs) == 1 && segs[0] == "feeds":
		return feedNodes(ctx)
	case len(segs) == 1 && segs[0] == "stories":
		return storyNodes(index.Stories())
	case len(segs) == 1 && segs[0] == "tags":
		return tagNodes()
	case len(segs) == 2 && segs[0] == "feed":
		id, err := strconv.Atoi(segs[1])
		if err != nil {
			return nil, fmt.Errorf("newsblur plugin: invalid feed id %q: %w", segs[1], err)
		}
		return storyNodes(index.FeedStories(id))
	case len(segs) == 2 && segs[0] == "tag":
		return storyNodes(index.StoriesByTag(segs[1]))
	case len(segs) == 2 && segs[0] == "story":
		return storyLeafNodes(segs[1]), nil
	default:
		// A leaf (story/{hash}/{content,original}) or an unknown node:
		// no children.
		return nil, nil
	}
}

func feedNodes(ctx context.Context) ([]cg.Node, error) {
	feeds, err := index.Feeds(ctx)
	if err != nil {
		return nil, err
	}
	nodes := make([]cg.Node, 0, len(feeds))
	for _, f := range feeds {
		nodes = append(nodes, cg.Node{
			URI:  nodeURL("feed", f.ID),
			Name: f.Title,
			Type: typeFeed,
		})
	}
	return nodes, nil
}

// storyNodes adapts a (refs, err) read-index result into story
// container nodes, so callers can write storyNodes(index.Stories()).
func storyNodes(refs []tools.StoryRef, err error) ([]cg.Node, error) {
	if err != nil {
		return nil, err
	}
	nodes := make([]cg.Node, 0, len(refs))
	for _, s := range refs {
		nodes = append(nodes, cg.Node{
			URI:  nodeURL("story", s.Hash),
			Name: s.Title,
			Type: typeStory,
		})
	}
	return nodes, nil
}

func tagNodes() ([]cg.Node, error) {
	tags, err := index.Tags()
	if err != nil {
		return nil, err
	}
	nodes := make([]cg.Node, 0, len(tags))
	for _, t := range tags {
		nodes = append(nodes, cg.Node{
			URI:  nodeURL("tag", t),
			Name: t,
			Type: typeTag,
		})
	}
	return nodes, nil
}

func storyLeafNodes(hash string) []cg.Node {
	return []cg.Node{
		{URI: nodeURL("story", hash, "content"), Name: "content", Type: typeStoryContent},
		{URI: nodeURL("story", hash, "original"), Name: "original", Type: typeStoryOriginal},
	}
}
