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

// LiveStoryUserTags reads one starred story's CURRENT tags straight from
// NewsBlur, never from the local cache: tags edited in the NewsBlur apps
// never reach the cache (fetch downloads a starred story once), so any
// read-modify-write of a tag set must start here, not from the cache.
func (c *Client) LiveStoryUserTags(ctx context.Context, storyHash string) ([]string, error) {
	raw, err := c.StoriesStarredByHash(ctx, []string{storyHash})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Stories []struct {
			Hash     string   `json:"story_hash"`
			UserTags []string `json:"user_tags"`
		} `json:"stories"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parsing starred story %s: %w", storyHash, err)
	}
	for _, s := range resp.Stories {
		if s.Hash == storyHash {
			if s.UserTags == nil {
				return []string{}, nil
			}
			return s.UserTags, nil
		}
	}
	return nil, fmt.Errorf("newsblur returned no starred story %s", storyHash)
}

func (c *Client) OriginalText(ctx context.Context, storyHash string) (json.RawMessage, error) {
	params := url.Values{"story_hash": {storyHash}}
	return c.get(ctx, "/rss_feeds/original_text", params)
}

// starredHashesEnvelope is the {"starred_story_hashes": [...]} shape
// NewsBlur's own /reader/starred_story_hashes API returns. Shared between
// ParseStarredHashes (read) and marshalStarredHashes (write) so the two
// stay in sync by construction.
type starredHashesEnvelope struct {
	Hashes []string `json:"starred_story_hashes"`
}

// ParseStarredHashes parses the response from /reader/starred_story_hashes.
// Accepts both the envelope format {"starred_story_hashes": [...]} and a flat
// array [...].
func ParseStarredHashes(raw json.RawMessage) ([]string, error) {
	var envelope starredHashesEnvelope
	if err := json.Unmarshal(raw, &envelope); err == nil {
		return envelope.Hashes, nil
	}

	var flat []string
	if err := json.Unmarshal(raw, &flat); err == nil {
		return flat, nil
	}

	return nil, fmt.Errorf("unrecognized starred_story_hashes format")
}

// marshalStarredHashes writes hashes back in the envelope format --
// ParseStarredHashes accepts either shape on read, but the envelope is what
// NewsBlur's own API returns, so writing it back the same way keeps the
// cached blob indistinguishable from one `nebulous fetch` would have written.
func marshalStarredHashes(hashes []string) (json.RawMessage, error) {
	return json.Marshal(starredHashesEnvelope{Hashes: hashes})
}
