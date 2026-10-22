package cgplugin

import (
	"context"
	"slices"

	"code.linenisgreat.com/nebulous/internal/bravo/tools"
)

const (
	// externalFeedID is the story_feed_id NewsBlur gives a story saved from
	// a URL with no feed behind it (see create_child.go's saveStoryChild).
	// No real feed has this id.
	externalFeedID    = "0"
	externalFeedTitle = "Saved (external)"
)

// feedsWithExternal is the subscription list plus, when any indexed story
// has feed id 0, one synthetic feed standing for them. Without it those
// stories sit under a feed the feeds root never lists and the feed facet can
// only label as a bare "0".
//
// The synthetic feed is a read-side grouping only. It is deliberately not
// known to Index.FeedMetadata, which patchFeed and deleteFeed require, so it
// can never be renamed, moved or "unsubscribed from".
func feedsWithExternal(ctx context.Context) ([]tools.FeedRef, error) {
	feeds, err := index.Feeds(ctx)
	if err != nil {
		return nil, err
	}
	if slices.ContainsFunc(feeds, func(f tools.FeedRef) bool { return f.ID == externalFeedID }) {
		return feeds, nil
	}
	external, err := index.FeedStories(0)
	if err != nil {
		return nil, err
	}
	if len(external) == 0 {
		return feeds, nil
	}
	return append(slices.Clone(feeds), tools.FeedRef{ID: externalFeedID, Title: externalFeedTitle, Active: true}), nil
}
