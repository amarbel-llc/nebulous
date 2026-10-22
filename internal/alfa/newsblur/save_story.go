package newsblur

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

// SaveStoryInput is the payload of NewsBlur's /api/save_story, the endpoint
// behind its "save this page" bookmarklet. Unlike starring, it creates a
// saved story from an arbitrary URL, one that never came from a feed.
type SaveStoryInput struct {
	URL   string
	Title string
	// Content is the story's HTML. Left empty, NewsBlur fetches the page
	// itself and extracts the content, falling back to no content if that
	// fails; a non-empty value is stored verbatim.
	Content string
	Notes   string
	Tags    []string
	// FeedID and RSSURL optionally attach the saved story to an existing
	// feed. With neither, NewsBlur files it under feed 0 (observed live
	// for a site with no feed; no subscription is created either way).
	FeedID int
	RSSURL string
}

// SavedStory identifies what /api/save_story created or updated.
type SavedStory struct {
	Hash      string
	FeedID    int
	Permalink string
}

// SaveStory saves an external URL as a starred story. Saved stories are
// unique on (user, story_url), so saving a URL twice updates the existing
// story (title, content, tags, notes) instead of duplicating it; the hash is
// stable across both.
//
// /api/save_story is undocumented in NewsBlur's public API page; its
// contract here is from the server source (apps/api/views.py::save_story)
// and verified live (just debug-probe-save-story): success is code 0 (not
// 1, unlike most mutations), failure a negative code (surfaced by post), and
// the "story" member is a JSON document encoded again as a string.
//
// On success the saved story is cached the way `nebulous fetch` would
// cache it, so a read right afterwards sees it. That caching is
// best-effort, like every other post-mutation cache patch.
func (c *Client) SaveStory(ctx context.Context, in SaveStoryInput) (SavedStory, error) {
	if in.URL == "" {
		return SavedStory{}, errors.New("newsblur: SaveStory requires a URL")
	}
	if in.Title == "" {
		return SavedStory{}, errors.New("newsblur: SaveStory requires a title")
	}

	form := url.Values{"story_url": {in.URL}, "title": {in.Title}}
	if in.Content != "" {
		form.Set("content", in.Content)
	}
	if in.Notes != "" {
		form.Set("user_notes", in.Notes)
	}
	if in.FeedID > 0 {
		form.Set("feed_id", strconv.Itoa(in.FeedID))
	}
	if in.RSSURL != "" {
		form.Set("rss_url", in.RSSURL)
	}
	addUserTags(form, in.Tags)

	raw, err := c.post(ctx, "/api/save_story", form)
	if err != nil {
		return SavedStory{}, err
	}
	saved, err := parseSavedStory(raw)
	if err != nil {
		return SavedStory{}, err
	}

	_ = c.PatchCachedStarredStoryHashes(saved.Hash, "")
	c.cacheSavedStory(ctx, saved.Hash)
	return saved, nil
}

// parseSavedStory decodes /api/save_story's response. The "story" member
// arrives as a JSON string holding the story document; an already-decoded
// object is accepted too in case NewsBlur ever stops double-encoding.
func parseSavedStory(raw json.RawMessage) (SavedStory, error) {
	var envelope struct {
		Message string          `json:"message"`
		Story   json.RawMessage `json:"story"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return SavedStory{}, fmt.Errorf("parsing save_story response: %w", err)
	}

	story := bytes.TrimSpace(envelope.Story)
	if len(story) > 0 && story[0] == '"' {
		var inner string
		if err := json.Unmarshal(story, &inner); err != nil {
			return SavedStory{}, fmt.Errorf("parsing save_story story string: %w", err)
		}
		story = []byte(inner)
	}

	var doc struct {
		Hash      string `json:"story_hash"`
		FeedID    int    `json:"story_feed_id"`
		Permalink string `json:"story_permalink"`
	}
	if len(story) > 0 {
		if err := json.Unmarshal(story, &doc); err != nil {
			return SavedStory{}, fmt.Errorf("parsing save_story story: %w", err)
		}
	}
	if doc.Hash == "" {
		return SavedStory{}, fmt.Errorf("save_story response carries no story hash (message: %q)", envelope.Message)
	}
	return SavedStory{Hash: doc.Hash, FeedID: doc.FeedID, Permalink: doc.Permalink}, nil
}

// cacheSavedStory stores the saved story in the shape `nebulous fetch`
// caches starred stories. save_story's own "story" is a raw database record
// (Mongo-style {"$date": ...} timestamps), not the /reader/starred_stories
// shape every reader of the cache parses, so the canonical record is
// re-read from NewsBlur. Best-effort: on any failure the story simply
// arrives with the next `nebulous fetch`.
func (c *Client) cacheSavedStory(ctx context.Context, hash string) {
	raw, err := c.StoriesStarredByHash(ctx, []string{hash})
	if err != nil {
		return
	}
	var resp struct {
		Stories []json.RawMessage `json:"stories"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return
	}
	for _, storyRaw := range resp.Stories {
		var story struct {
			Hash string `json:"story_hash"`
		}
		if json.Unmarshal(storyRaw, &story) == nil && story.Hash == hash {
			_ = c.PutCachedStarredStory(hash, storyRaw)
			return
		}
	}
}
