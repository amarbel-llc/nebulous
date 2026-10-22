package newsblur

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

// OriginalTextState says what the local cache holds for one story's original
// text, which is more than whether it holds anything: NewsBlur answers a page
// it could not extract with a valid, cacheable JSON body that carries no text
// ({"original_text": null, "failed": true}).
type OriginalTextState int

const (
	// OriginalTextMissing: nothing usable is cached; ask NewsBlur.
	OriginalTextMissing OriginalTextState = iota
	// OriginalTextFinal: an answer worth keeping. Asking again would not
	// change it.
	OriginalTextFinal
	// OriginalTextRetryable: a cached answer that holds no text because the
	// extraction failed (or the body was not JSON at all). A later attempt may
	// well succeed, since the failure can be transient on NewsBlur's side.
	OriginalTextRetryable
)

// OriginalTextStatus classifies the cached original-text answer for hash and
// returns when it was written. HasCachedOriginalText only says an entry
// exists; fetch uses this to tell a kept answer from one worth asking for
// again.
func (c *Client) OriginalTextStatus(hash string) (OriginalTextState, time.Time) {
	if c.cache == nil {
		return OriginalTextMissing, time.Time{}
	}
	entry, ok := c.cache.manifest.Lookup(c.originalTextCacheKey(hash))
	if !ok {
		return OriginalTextMissing, time.Time{}
	}
	raw, ok := c.cache.readBlob(entry.Digest)
	if !ok {
		return OriginalTextMissing, time.Time{}
	}
	return classifyOriginalText(raw), entry.WrittenAt
}

func classifyOriginalText(raw json.RawMessage) OriginalTextState {
	var answer struct {
		OriginalText string `json:"original_text"`
		Failed       bool   `json:"failed"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return OriginalTextRetryable
	}
	if answer.OriginalText == "" && answer.Failed {
		return OriginalTextRetryable
	}
	return OriginalTextFinal
}

// RefetchOriginalText asks NewsBlur for a story's original text and records
// the answer, whatever the cache holds: OriginalText would return a cached
// entry unchanged (an immutable one forever), which is exactly the answer a
// retry exists to replace. The write re-stamps the entry, so a failure that
// persists is retried one interval after THIS attempt, not the first.
func (c *Client) RefetchOriginalText(ctx context.Context, storyHash string) (json.RawMessage, error) {
	params := url.Values{"story_hash": {storyHash}}
	return c.getSkipCache(ctx, "/rss_feeds/original_text", params)
}
