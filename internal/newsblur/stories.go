package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

func (c *Client) StarredStoryHashes(ctx context.Context) (json.RawMessage, error) {
	return c.getSkipCache(ctx, "/reader/starred_story_hashes", nil)
}

func (c *Client) StoriesStarredByHash(ctx context.Context, hashes []string) (json.RawMessage, error) {
	params := url.Values{}
	for _, h := range hashes {
		params.Add("h", h)
	}
	return c.doGet(ctx, "/reader/starred_stories", params)
}

func (c *Client) OriginalText(ctx context.Context, storyHash string) (json.RawMessage, error) {
	params := url.Values{"story_hash": {storyHash}}
	return c.get(ctx, "/rss_feeds/original_text", params)
}

// ParseStarredHashes parses the response from /reader/starred_story_hashes.
// Accepts both the envelope format {"starred_story_hashes": [...]} and a flat
// array [...].
func ParseStarredHashes(raw json.RawMessage) ([]string, error) {
	var envelope struct {
		Hashes []string `json:"starred_story_hashes"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		return envelope.Hashes, nil
	}

	var flat []string
	if err := json.Unmarshal(raw, &flat); err == nil {
		return flat, nil
	}

	return nil, fmt.Errorf("unrecognized starred_story_hashes format")
}
