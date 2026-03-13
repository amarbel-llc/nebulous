package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

func (c *Client) Feeds(ctx context.Context, includeFavicons, flat, updateCounts bool) (json.RawMessage, error) {
	params := url.Values{}
	if includeFavicons {
		params.Set("include_favicons", "true")
	}
	if flat {
		params.Set("flat", "true")
	}
	if updateCounts {
		params.Set("update_counts", "true")
	}
	return c.get(ctx, "/reader/feeds", params)
}

func (c *Client) SearchFeed(ctx context.Context, address string) (json.RawMessage, error) {
	params := url.Values{"address": {address}}
	return c.get(ctx, "/rss_feeds/search_feed", params)
}

func (c *Client) FeedStats(ctx context.Context, feedID int) (json.RawMessage, error) {
	return c.get(ctx, fmt.Sprintf("/rss_feeds/statistics/%d", feedID), nil)
}

func (c *Client) FeedAutocomplete(ctx context.Context, term string) (json.RawMessage, error) {
	params := url.Values{"term": {term}}
	return c.get(ctx, "/rss_feeds/feed_autocomplete", params)
}
