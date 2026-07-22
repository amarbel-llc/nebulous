package newsblur

import (
	"encoding/json"
	"fmt"
	"slices"
)

// PatchCachedStoryReadStatus updates one cached starred story's read_status
// field in place. See PatchCachedStoriesReadStatus for the batched form
// mark-stories-read uses; this is a single-hash convenience wrapper around
// it for mark-unread's single-story call site.
func (c *Client) PatchCachedStoryReadStatus(hash string, read bool) error {
	return c.PatchCachedStoriesReadStatus([]string{hash}, read)
}

// PatchCachedStoriesReadStatus updates read_status on every already-cached
// story in hashes, in a single manifest save, immediately after a
// successful mark-read/mark-unread call -- instead of leaving the cached
// blobs to lag until the next `nebulous fetch` overwrites them. Batched
// rather than one save per hash: Manifest.Record's own doc comment calls
// looping Record calls "the O(n^2) write pattern" that RecordBatch exists
// to avoid, and MarkStoriesRead can patch dozens of hashes from one API
// call. Best-effort per hash, not just overall: a hash that isn't cached
// locally, or whose cached blob fails to unmarshal, is skipped rather than
// aborting the whole batch -- one corrupted entry among many hashes must
// not block patching the rest, which have nothing wrong with them. The
// next fetch will repopulate whatever was skipped regardless.
func (c *Client) PatchCachedStoriesReadStatus(hashes []string, read bool) error {
	encoded := json.RawMessage("0")
	if read {
		encoded = json.RawMessage("1")
	}

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
		story["read_status"] = encoded
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

// PatchCachedStoryUserTags updates one cached starred story's user_tags
// field in place, immediately after a successful SetStoryUserTags call.
// Without this, the cached blob doesn't just lag until the next `nebulous
// fetch` (PatchCachedStoryReadStatus's own case) -- it stays stale
// PERMANENTLY: cmd/nebulous/main.go's Phase 2 only ever fetches a starred
// story hash ONCE (HasCachedStarredStory gates it out of every later
// fetch run), an immutable-content assumption user_tags breaks since it's
// re-settable after the initial star. Confirmed live against a real
// account: an already-cached story's tag patch stayed invisible through
// unlimited fetch cycles, not just until the next one (cutting-garden#180
// / nebulous#53's verification investigation -- the write itself was
// proven to reach NewsBlur by reading /reader/starred_stories directly,
// bypassing nebulous; the cache simply never had a path back to it).
// Best-effort like every other optimistic-cache-patch call site in this
// file: a hash that isn't cached locally, or whose cached blob fails to
// unmarshal, is skipped rather than erroring.
func (c *Client) PatchCachedStoryUserTags(hash string, userTags []string) error {
	raw, ok := c.CachedStarredStory(hash)
	if !ok {
		return nil
	}
	var story map[string]json.RawMessage
	if err := json.Unmarshal(raw, &story); err != nil {
		return nil
	}
	encoded, err := json.Marshal(userTags)
	if err != nil {
		return nil
	}
	story["user_tags"] = encoded
	body, err := json.Marshal(story)
	if err != nil {
		return nil
	}
	return c.PutCachedStarredStory(hash, body)
}

// PatchCachedStarredStoryHashes adds or removes one hash from the cached
// starred-story-hashes list in place, immediately after a successful
// star/unstar call. Pass the hash being added as add, the hash being
// removed as remove -- exactly one is non-empty per caller. This replaces
// deleting the whole cached list (the old role of
// InvalidateStarredStoryHashManifest): that left a window, between the
// mutation and the next fetch, where a read saw no cached hash list at
// all rather than an accurate one.
func (c *Client) PatchCachedStarredStoryHashes(add, remove string) error {
	raw, ok := c.CachedStarredStoryHashes()
	var hashes []string
	if ok {
		var err error
		hashes, err = ParseStarredHashes(raw)
		if err != nil {
			return fmt.Errorf("patching cached starred story hashes: %w", err)
		}
	}

	if remove != "" {
		if i := slices.Index(hashes, remove); i >= 0 {
			hashes = slices.Delete(hashes, i, i+1)
		}
	}
	if add != "" && !slices.Contains(hashes, add) {
		hashes = append(hashes, add)
	}

	patched, err := marshalStarredHashes(hashes)
	if err != nil {
		return fmt.Errorf("patching cached starred story hashes: %w", err)
	}
	return c.PutCachedStarredStoryHashes(patched)
}
