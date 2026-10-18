package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
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

// addUserTags sends each tag as its own repeated user_tags parameter, the
// shape NewsBlur's own client sends and its view reads
// (request.POST.getlist("user_tags")). A single comma-joined value would be
// read as ONE tag containing commas.
func addUserTags(form url.Values, userTags []string) {
	for _, tag := range userTags {
		form.Add("user_tags", tag)
	}
}

func (c *Client) StarStory(ctx context.Context, storyHash string, userTags []string) (json.RawMessage, error) {
	form := url.Values{"story_hash": {storyHash}}
	addUserTags(form, userTags)
	raw, err := c.post(ctx, "/reader/mark_story_hash_as_starred", form)
	if err == nil {
		_ = c.PatchCachedStarredStoryHashes(storyHash, "")
	}
	return raw, err
}

// SetStoryUserTags replaces an already-starred story's tags by re-calling
// NewsBlur's star endpoint (nebulous#50): mark_story_hash_as_starred
// REPLACES the existing user_tags with whatever's sent, rather than
// merging (verified live against a real account: starring with ["a"]
// then ["b"] left only "b", not both).
//
// Clearing (an empty userTags) OMITS the param. Verified live
// (just debug-probe-clear-cycle): after setting a tag, omitting user_tags
// leaves the story with user_tags [], while sending user_tags= with an empty
// value leaves it with [""], a single empty-string tag. The empty-value form
// this method used to send is where the empty tag on dozens of stories came
// from.
func (c *Client) SetStoryUserTags(ctx context.Context, storyHash string, userTags []string) (json.RawMessage, error) {
	form := url.Values{"story_hash": {storyHash}}
	addUserTags(form, userTags)
	raw, err := c.post(ctx, "/reader/mark_story_hash_as_starred", form)
	if err == nil {
		_ = c.PatchCachedStarredStoryHashes(storyHash, "")
		_ = c.PatchCachedStoryUserTags(storyHash, userTags)
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
