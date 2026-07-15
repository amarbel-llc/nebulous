package newsblur

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func testClientWithCache(t *testing.T) *Client {
	t.Helper()
	c := NewClient("test-token")
	if err := c.WithCache(filepath.Join(t.TempDir(), "manifest.json"), time.Hour, newMemSink()); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestHasCachedStarredStory(t *testing.T) {
	c := testClientWithCache(t)
	story := json.RawMessage(`{"story_hash":"abc123","story_title":"Test"}`)

	if c.HasCachedStarredStory("abc123") {
		t.Error("HasCachedStarredStory returned true before put")
	}

	if err := c.PutCachedStarredStory("abc123", story); err != nil {
		t.Fatalf("PutCachedStarredStory: %v", err)
	}

	if !c.HasCachedStarredStory("abc123") {
		t.Error("HasCachedStarredStory returned false after put")
	}

	if c.HasCachedStarredStory("missing") {
		t.Error("HasCachedStarredStory returned true for missing hash")
	}
}

func TestCachedStarredStoryRoundTrip(t *testing.T) {
	c := testClientWithCache(t)
	story := json.RawMessage(`{"story_hash":"abc123","story_title":"Round Trip"}`)

	if err := c.PutCachedStarredStory("abc123", story); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, ok := c.CachedStarredStory("abc123")
	if !ok {
		t.Fatal("CachedStarredStory returned false")
	}
	if string(got) != string(story) {
		t.Errorf("got %s, want %s", got, story)
	}

	_, ok = c.CachedStarredStory("missing")
	if ok {
		t.Error("CachedStarredStory returned true for missing hash")
	}
}

func TestCachedStarredStoryHashesRoundTrip(t *testing.T) {
	c := testClientWithCache(t)
	manifest := json.RawMessage(`{"starred_story_hashes":["a","b","c"]}`)

	if err := c.PutCachedStarredStoryHashes(manifest); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, ok := c.CachedStarredStoryHashes()
	if !ok {
		t.Fatal("CachedStarredStoryHashes returned false")
	}
	if string(got) != string(manifest) {
		t.Errorf("got %s, want %s", got, manifest)
	}
}

// nebulous#41-followup: NewCacheOnlyClient's ttl=0 means get()'s
// TTL-checked cache path always treats a non-immutable entry (like the
// one client.Feeds()'s own doGet writes via the plain `put`) as expired,
// falling through to doGet — which nil-panics on a cache-only client's
// absent httpClient. Confirmed live: nebulous-cg mcp crashed exactly
// this way when cutting-garden's background facet-maintenance goroutine
// called FacetCounts -> ReadIndex.Feeds() -> feedIndex.build() ->
// client.Feeds().
func TestCacheOnlyClientFeedsReadsCachedEntryWithoutPanicking(t *testing.T) {
	sink := newMemSink()
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")

	// Seed the cache the way an online client's Feeds() would on a
	// cache miss: the regular (TTL-checked) `put`, not `putImmutable`.
	seed := NewClient("test-token")
	if err := seed.WithCache(manifestPath, time.Hour, sink); err != nil {
		t.Fatal(err)
	}
	feedsRaw := json.RawMessage(`{"feeds":{},"flat_folders_with_feeds":[]}`)
	key := seed.cache.cacheKey("/reader/feeds", url.Values{"flat": {"true"}})
	if err := seed.cache.put(key, feedsRaw); err != nil {
		t.Fatal(err)
	}

	cacheOnly, err := NewCacheOnlyClient(manifestPath, sink)
	if err != nil {
		t.Fatal(err)
	}

	got, err := cacheOnly.Feeds(context.Background(), false, true, false)
	if err != nil {
		t.Fatalf("Feeds: %v (want the cached entry, not a live-fetch error/panic)", err)
	}
	if string(got) != string(feedsRaw) {
		t.Errorf("Feeds = %s, want %s", got, feedsRaw)
	}
}

// A cache-only client with nothing cached must return a clean error, not
// attempt a live fetch against its nil httpClient.
func TestCacheOnlyClientFeedsUncachedReturnsCleanError(t *testing.T) {
	cacheOnly, err := NewCacheOnlyClient(filepath.Join(t.TempDir(), "manifest.json"), newMemSink())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cacheOnly.Feeds(context.Background(), false, true, false); err == nil {
		t.Error("Feeds() err = nil, want a clean 'not cached' error for a cache-only client with nothing cached")
	}
}

func TestCachedStarredStoryNilCache(t *testing.T) {
	c := NewClient("test-token") // no WithCache

	if c.HasCachedStarredStory("abc") {
		t.Error("HasCachedStarredStory should return false with nil cache")
	}

	_, ok := c.CachedStarredStory("abc")
	if ok {
		t.Error("CachedStarredStory should return false with nil cache")
	}

	if err := c.PutCachedStarredStory("abc", json.RawMessage(`{}`)); err != nil {
		t.Errorf("PutCachedStarredStory should not error with nil cache: %v", err)
	}

	_, ok = c.CachedStarredStoryHashes()
	if ok {
		t.Error("CachedStarredStoryHashes should return false with nil cache")
	}

	if err := c.PutCachedStarredStoryHashes(json.RawMessage(`{}`)); err != nil {
		t.Errorf("PutCachedStarredStoryHashes should not error with nil cache: %v", err)
	}

	if err := c.PatchCachedStoryReadStatus("abc", true); err != nil {
		t.Errorf("PatchCachedStoryReadStatus should not error with nil cache: %v", err)
	}

	if err := c.PatchCachedStarredStoryHashes("abc", ""); err != nil {
		t.Errorf("PatchCachedStarredStoryHashes should not error with nil cache: %v", err)
	}
}
