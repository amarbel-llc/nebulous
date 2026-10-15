package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
)

// LiveStarredTagCounts returns every starred-story tag NewsBlur knows right
// now with its story count, read from /reader/starred_counts (never the
// cache). Entries without a tag (the per-feed counts in the same list) are
// skipped.
func (c *Client) LiveStarredTagCounts(ctx context.Context) (map[string]int, error) {
	raw, err := c.doGet(ctx, "/reader/starred_counts", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Counts []struct {
			Tag   *string `json:"tag"`
			Count int     `json:"count"`
		} `json:"starred_counts"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parsing starred_counts: %w", err)
	}
	counts := make(map[string]int, len(resp.Counts))
	for _, e := range resp.Counts {
		if e.Tag != nil {
			counts[*e.Tag] = e.Count
		}
	}
	return counts, nil
}

// RenameStarredTag renames a starred-story tag across every story that
// carries it (NewsBlur's rename_starred_tag). NewsBlur trims both names and
// caps the new one at 128 characters; a rejected rename comes back as an
// HTTP 200 with code -1, which post() reports as an error. The caller is
// responsible for patching the local cache (PatchCachedUserTag).
func (c *Client) RenameStarredTag(ctx context.Context, oldTag, newTag string) (json.RawMessage, error) {
	form := url.Values{"old_tag_name": {oldTag}, "new_tag_name": {newTag}}
	return c.post(ctx, "/reader/rename_starred_tag", form)
}

// DeleteStarredTag removes a tag from every story that carries it; the
// stories stay starred (NewsBlur's delete_starred_tag).
func (c *Client) DeleteStarredTag(ctx context.Context, tag string) (json.RawMessage, error) {
	form := url.Values{"tag_name": {tag}}
	return c.post(ctx, "/reader/delete_starred_tag", form)
}

// PatchCachedUserTag rewrites the cached copy of each story in hashes after
// a tag rename (newTag non-empty: oldTag becomes newTag, collapsing a
// duplicate) or delete (newTag empty: oldTag is dropped), in one manifest
// save. Best-effort per story like the other cache patches: a story that
// is not cached or whose blob does not parse is skipped. Without this the
// cached tags would stay stale permanently, since fetch never re-downloads
// a starred story.
func (c *Client) PatchCachedUserTag(hashes []string, oldTag, newTag string) error {
	patched := make(map[string]json.RawMessage, len(hashes))
	for _, hash := range hashes {
		raw, ok := c.CachedStarredStory(hash)
		if !ok {
			continue
		}
		var story map[string]json.RawMessage
		if err := json.Unmarshal(raw, &story); err != nil {
			continue
		}
		var tags []string
		if t, ok := story["user_tags"]; ok {
			if err := json.Unmarshal(t, &tags); err != nil {
				continue
			}
		}
		if !slices.Contains(tags, oldTag) {
			continue
		}
		next := make([]string, 0, len(tags))
		for _, tag := range tags {
			switch {
			case tag != oldTag:
				if !slices.Contains(next, tag) {
					next = append(next, tag)
				}
			case newTag != "" && !slices.Contains(next, newTag):
				next = append(next, newTag)
			}
		}
		// A rename onto a tag the story already carries must not leave the
		// new name twice; the loop above only appends newTag if absent, but
		// the story's own existing newTag may come later in the list.
		next = dedupe(next)
		encoded, err := json.Marshal(next)
		if err != nil {
			continue
		}
		story["user_tags"] = encoded
		body, err := json.Marshal(story)
		if err != nil {
			continue
		}
		patched[hash] = body
	}
	if len(patched) == 0 {
		return nil
	}
	return c.PutCachedStarredStoriesBatch(patched)
}

func dedupe(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if !slices.Contains(out, tag) {
			out = append(out, tag)
		}
	}
	return out
}
