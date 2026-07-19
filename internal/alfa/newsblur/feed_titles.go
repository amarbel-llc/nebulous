package newsblur

import "encoding/json"

// seenFeedTitlesCacheKey is a single accumulating registry of every feed
// title nebulous has ever observed via a live /reader/feeds fetch, keyed by
// feed id. Unlike the /reader/feeds response cache itself (overwritten
// wholesale on every fetch), this only ever grows: a feed the user later
// unsubscribes from drops out of future /reader/feeds responses, but its
// last-known title here survives — the fallback ResolveFacetLabels
// (internal/charlie/cgplugin/facets.go) needs to keep resolving the `feed`
// facet dimension for stories starred from feeds no longer subscribed.
func (c *Client) seenFeedTitlesCacheKey() string {
	return c.cache.cacheKey("/feeds/seen-titles", nil)
}

// SeenFeedTitles returns every feed title ever recorded by PutSeenFeedTitles,
// keyed by feed id — including feeds no longer in the live subscription
// list.
func (c *Client) SeenFeedTitles() map[string]string {
	if c.cache == nil {
		return nil
	}
	raw, ok := c.cache.getNoTTL(c.seenFeedTitlesCacheKey())
	if !ok {
		return nil
	}
	var titles map[string]string
	if err := json.Unmarshal(raw, &titles); err != nil {
		return nil
	}
	return titles
}

// PutSeenFeedTitles unions titles into the persisted registry (new entries
// added, existing ids refreshed to their latest title — never deleted, so a
// feed that later disappears from /reader/feeds keeps its last-known
// title). Called once per feed-index rebuild, after a fresh /reader/feeds
// fetch, with that response's full id-to-title map.
func (c *Client) PutSeenFeedTitles(titles map[string]string) error {
	if c.cache == nil || len(titles) == 0 {
		return nil
	}
	merged := c.SeenFeedTitles()
	if merged == nil {
		merged = make(map[string]string, len(titles))
	}
	changed := false
	for id, title := range titles {
		if merged[id] != title {
			merged[id] = title
			changed = true
		}
	}
	if !changed {
		return nil
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return c.cache.putImmutable(c.seenFeedTitlesCacheKey(), raw)
}
