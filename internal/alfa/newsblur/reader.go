package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// MarkStoriesRead marks the given stories read on NewsBlur, then
// optimistically patches their cached read_status (one manifest save for
// all of them, see PatchCachedStoriesReadStatus) so a read immediately
// afterward reflects the change instead of lagging until the next
// `nebulous fetch` -- the patch is best-effort and its error is
// deliberately swallowed: the live mutation above already succeeded, and
// a patch failure only means the read stays lagged as before, not that
// the mutation itself failed.
func (c *Client) MarkStoriesRead(ctx context.Context, storyHashes []string) (json.RawMessage, error) {
	form := url.Values{}
	for _, h := range storyHashes {
		form.Add("story_hash", h)
	}
	raw, err := c.post(ctx, "/reader/mark_story_hashes_as_read", form)
	if err == nil {
		_ = c.PatchCachedStoriesReadStatus(storyHashes, true)
	}
	return raw, err
}

func (c *Client) MarkStoryUnread(ctx context.Context, storyHash string) (json.RawMessage, error) {
	form := url.Values{"story_hash": {storyHash}}
	raw, err := c.post(ctx, "/reader/mark_story_hash_as_unread", form)
	if err == nil {
		_ = c.PatchCachedStoryReadStatus(storyHash, false)
	}
	return raw, err
}

func (c *Client) StarStory(ctx context.Context, storyHash string, userTags []string) (json.RawMessage, error) {
	form := url.Values{"story_hash": {storyHash}}
	if len(userTags) > 0 {
		form.Set("user_tags", strings.Join(userTags, ","))
	}
	raw, err := c.post(ctx, "/reader/mark_story_hash_as_starred", form)
	if err == nil {
		_ = c.PatchCachedStarredStoryHashes(storyHash, "")
	}
	return raw, err
}

func (c *Client) UnstarStory(ctx context.Context, storyHash string) (json.RawMessage, error) {
	form := url.Values{"story_hash": {storyHash}}
	raw, err := c.post(ctx, "/reader/mark_story_hash_as_unstarred", form)
	if err == nil {
		_ = c.PatchCachedStarredStoryHashes("", storyHash)
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
