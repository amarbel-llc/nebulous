package newsblur

import (
	"encoding/json"
	"net/url"
	"time"
)

// CaptureRecord marks a completed cutting-garden capture for one
// (story hash, format) pair — the "done, skip" signal the capture loop's
// gap-filling scan checks before re-attempting a capture.
type CaptureRecord struct {
	ReceiptID  string    `json:"receipt_id"`
	CapturedAt time.Time `json:"captured_at"`
}

func (c *Client) captureRecordCacheKey(storyHash, format string) string {
	params := url.Values{"story_hash": {storyHash}, "format": {format}}
	return c.cache.cacheKey("/capture/record", params)
}

// HasCaptureRecord reports whether storyHash already has a completed
// capture for format.
func (c *Client) HasCaptureRecord(storyHash, format string) bool {
	if c.cache == nil {
		return false
	}
	return c.cache.has(c.captureRecordCacheKey(storyHash, format))
}

// CaptureRecordFor returns the completed capture record for
// (storyHash, format), if any.
func (c *Client) CaptureRecordFor(storyHash, format string) (CaptureRecord, bool) {
	var rec CaptureRecord
	if c.cache == nil {
		return rec, false
	}
	raw, ok := c.cache.getNoTTL(c.captureRecordCacheKey(storyHash, format))
	if !ok {
		return rec, false
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return rec, false
	}
	return rec, true
}

// PutCaptureRecordFor records a successful capture for (storyHash, format).
// Written once per pair — the capture loop never re-captures a pair with
// an existing record, so this is write-once in practice. Also unions
// format into the persisted known-formats registry (see CaptureFormats)
// so tree traversal can discover it.
func (c *Client) PutCaptureRecordFor(storyHash, format string, rec CaptureRecord) error {
	if c.cache == nil {
		return nil
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := c.cache.putImmutable(c.captureRecordCacheKey(storyHash, format), raw); err != nil {
		return err
	}
	return c.recordCaptureFormat(format)
}

func (c *Client) captureFormatsCacheKey() string {
	return c.cache.cacheKey("/capture/formats", nil)
}

// CaptureFormats returns every format string that has ever produced a
// completed capture record. Per-(hash, format) completion records are
// keyed by an opaque cache-key hash with no enumeration primitive of
// their own — a story's own record doesn't say what OTHER formats
// exist for OTHER stories — so this registry is the source of truth
// tree traversal consults to know which capture/{format} leaves might
// exist anywhere in the corpus (a story only actually carries a leaf
// for a format it itself has a record for; see
// tools.ReadIndex.CaptureFormats / cgplugin's storyLeafNodes).
func (c *Client) CaptureFormats() []string {
	if c.cache == nil {
		return nil
	}
	raw, ok := c.cache.getNoTTL(c.captureFormatsCacheKey())
	if !ok {
		return nil
	}
	var formats []string
	if err := json.Unmarshal(raw, &formats); err != nil {
		return nil
	}
	return formats
}

// recordCaptureFormat unions format into the persisted known-formats
// registry, idempotently.
func (c *Client) recordCaptureFormat(format string) error {
	formats := c.CaptureFormats()
	for _, f := range formats {
		if f == format {
			return nil
		}
	}
	formats = append(formats, format)
	raw, err := json.Marshal(formats)
	if err != nil {
		return err
	}
	return c.cache.putImmutable(c.captureFormatsCacheKey(), raw)
}

func (c *Client) captureWatermarkCacheKey() string {
	return c.cache.cacheKey("/capture/watermark", nil)
}

// captureWatermark is the persisted JSON body for the capture-eligibility
// anchor: stories published before Since are not eligible for the
// gap-filling scan unless --backfill overrides it.
type captureWatermark struct {
	Since time.Time `json:"since"`
}

// CaptureWatermark returns the persisted capture-eligibility watermark,
// if one has been established (by a prior `nebulous capture` run).
func (c *Client) CaptureWatermark() (time.Time, bool) {
	var w captureWatermark
	if c.cache == nil {
		return time.Time{}, false
	}
	raw, ok := c.cache.getNoTTL(c.captureWatermarkCacheKey())
	if !ok {
		return time.Time{}, false
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return time.Time{}, false
	}
	return w.Since, true
}

// PutCaptureWatermark persists the capture-eligibility watermark. Called
// once, the first time `nebulous capture` ever runs.
func (c *Client) PutCaptureWatermark(since time.Time) error {
	if c.cache == nil {
		return nil
	}
	raw, err := json.Marshal(captureWatermark{Since: since})
	if err != nil {
		return err
	}
	return c.cache.putImmutable(c.captureWatermarkCacheKey(), raw)
}
