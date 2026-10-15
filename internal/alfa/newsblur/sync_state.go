package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// StarredSyncState records, per starred story, the "last updated" stamp
// NewsBlur reported when the cached copy was last downloaded. Comparing it
// with NewsBlur's current stamps finds stories whose tags changed after we
// cached them without re-downloading anything (tag-mutation review, finding
// 2): fetch otherwise downloads a starred story exactly once.
type StarredSyncState struct {
	Version int `json:"version"`
	// Timestamps maps story hash to NewsBlur's stamp (a unix-seconds
	// string, exactly as /reader/starred_story_hashes?include_timestamps
	// returns it). A hash with no entry has never been synced.
	Timestamps map[string]string `json:"timestamps"`
	// FullSyncAt is when every cached story was last re-downloaded; the
	// periodic full re-sync (read state has no cheap change signal) is
	// scheduled from it. Zero means never.
	FullSyncAt time.Time `json:"full_sync_at"`
}

const starredSyncStateVersion = 1

func (c *Client) syncStateKey() string {
	return c.cache.cacheKey("/nebulous/starred_sync_state", nil)
}

// CachedStarredSyncState returns the recorded state, or false when none was
// ever recorded (or it cannot be read).
func (c *Client) CachedStarredSyncState() (StarredSyncState, bool) {
	if c.cache == nil {
		return StarredSyncState{}, false
	}
	raw, ok := c.cache.getNoTTL(c.syncStateKey())
	if !ok {
		return StarredSyncState{}, false
	}
	var s StarredSyncState
	if err := json.Unmarshal(raw, &s); err != nil || s.Version != starredSyncStateVersion {
		return StarredSyncState{}, false
	}
	if s.Timestamps == nil {
		s.Timestamps = map[string]string{}
	}
	return s, true
}

// PutCachedStarredSyncState stores the state, replacing any earlier one.
func (c *Client) PutCachedStarredSyncState(s StarredSyncState) error {
	if c.cache == nil {
		return nil
	}
	s.Version = starredSyncStateVersion
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return c.cache.putImmutable(c.syncStateKey(), raw)
}

// StarredHashTimestamps returns every starred story hash with NewsBlur's
// current "last updated" stamp, in one live request
// (/reader/starred_story_hashes?include_timestamps=true). The stamp is the
// story's starred_updated time, which NewsBlur sets on every save of the
// starred story (so a tag edit through the star endpoint or a tag rename
// bumps it); a tag DELETE and read-state changes do not.
func (c *Client) StarredHashTimestamps(ctx context.Context) (map[string]string, error) {
	raw, err := c.getSkipCache(ctx, "/reader/starred_story_hashes", map[string][]string{"include_timestamps": {"true"}})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Pairs [][]string `json:"starred_story_hashes"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parsing starred story hashes with timestamps: %w", err)
	}
	out := make(map[string]string, len(resp.Pairs))
	for _, p := range resp.Pairs {
		if len(p) != 2 {
			return nil, fmt.Errorf("starred story hash entry %v is not a [hash, timestamp] pair", p)
		}
		out[p[0]] = p[1]
	}
	return out, nil
}
