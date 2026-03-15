package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

func (c *Client) StoriesFeed(ctx context.Context, feedID int, page int, order, readFilter, query string) (json.RawMessage, error) {
	if page < 1 {
		page = 1
	}
	params := url.Values{"page": {fmt.Sprintf("%d", page)}}
	if order != "" {
		params.Set("order", order)
	}
	if readFilter != "" {
		params.Set("read_filter", readFilter)
	}
	if query != "" {
		params.Set("query", query)
	}
	return c.get(ctx, fmt.Sprintf("/reader/feed/%d", feedID), params)
}

func (c *Client) StoriesRiver(ctx context.Context, feedIDs []int, page int) (json.RawMessage, error) {
	params := url.Values{}
	for _, id := range feedIDs {
		params.Add("feeds", fmt.Sprintf("%d", id))
	}
	if page < 1 {
		page = 1
	}
	params.Set("page", fmt.Sprintf("%d", page))
	return c.get(ctx, "/reader/river_stories", params)
}

func (c *Client) StoriesStarred(ctx context.Context, page int, tag, query string) (json.RawMessage, error) {
	if page < 1 {
		page = 1
	}
	params := url.Values{"page": {fmt.Sprintf("%d", page)}}
	if tag != "" {
		params.Set("tag", tag)
	}
	if query != "" {
		params.Set("query", query)
	}
	return c.get(ctx, "/reader/starred_stories", params)
}

func (c *Client) UnreadStoryHashes(ctx context.Context, feedIDs []int) (json.RawMessage, error) {
	params := url.Values{}
	for _, id := range feedIDs {
		params.Add("feed_id", fmt.Sprintf("%d", id))
	}
	return c.get(ctx, "/reader/unread_story_hashes", params)
}

func (c *Client) StarredStoryHashes(ctx context.Context) (json.RawMessage, error) {
	return c.getSkipCache(ctx, "/reader/starred_story_hashes", nil)
}

func (c *Client) OriginalText(ctx context.Context, storyHash string) (json.RawMessage, error) {
	params := url.Values{"story_hash": {storyHash}}
	return c.get(ctx, "/rss_feeds/original_text", params)
}
