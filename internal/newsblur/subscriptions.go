package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

func (c *Client) Subscribe(ctx context.Context, feedURL, folder string) (json.RawMessage, error) {
	form := url.Values{"url": {feedURL}}
	if folder != "" {
		form.Set("folder", folder)
	}
	return c.post(ctx, "/reader/add_url", form)
}

func (c *Client) Unsubscribe(ctx context.Context, feedID int, inFolder string) (json.RawMessage, error) {
	form := url.Values{"feed_id": {fmt.Sprintf("%d", feedID)}}
	if inFolder != "" {
		form.Set("in_folder", inFolder)
	}
	return c.post(ctx, "/reader/delete_feed", form)
}

func (c *Client) RenameFeed(ctx context.Context, feedID int, feedTitle string) (json.RawMessage, error) {
	form := url.Values{
		"feed_id":    {fmt.Sprintf("%d", feedID)},
		"feed_title": {feedTitle},
	}
	return c.post(ctx, "/reader/rename_feed", form)
}
