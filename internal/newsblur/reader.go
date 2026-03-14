package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func (c *Client) MarkStoriesRead(ctx context.Context, storyHashes []string) (json.RawMessage, error) {
	form := url.Values{}
	for _, h := range storyHashes {
		form.Add("story_hash", h)
	}
	return c.post(ctx, "/reader/mark_story_hashes_as_read", form)
}

func (c *Client) MarkStoryUnread(ctx context.Context, storyHash string) (json.RawMessage, error) {
	form := url.Values{"story_hash": {storyHash}}
	return c.post(ctx, "/reader/mark_story_hash_as_unread", form)
}

func (c *Client) StarStory(ctx context.Context, storyHash string, userTags []string) (json.RawMessage, error) {
	form := url.Values{"story_hash": {storyHash}}
	if len(userTags) > 0 {
		form.Set("user_tags", strings.Join(userTags, ","))
	}
	raw, err := c.post(ctx, "/reader/mark_story_hash_as_starred", form)
	if err == nil {
		c.InvalidateStarredStoryPages()
	}
	return raw, err
}

func (c *Client) UnstarStory(ctx context.Context, storyHash string) (json.RawMessage, error) {
	form := url.Values{"story_hash": {storyHash}}
	raw, err := c.post(ctx, "/reader/mark_story_hash_as_unstarred", form)
	if err == nil {
		c.InvalidateStarredStoryPages()
	}
	return raw, err
}

func (c *Client) MarkFeedRead(ctx context.Context, feedID int) (json.RawMessage, error) {
	form := url.Values{"feed_id": {fmt.Sprintf("%d", feedID)}}
	return c.post(ctx, "/reader/mark_feed_as_read", form)
}

func (c *Client) MarkAllRead(ctx context.Context, days int) (json.RawMessage, error) {
	form := url.Values{}
	if days > 0 {
		form.Set("days", fmt.Sprintf("%d", days))
	}
	return c.post(ctx, "/reader/mark_all_as_read", form)
}
