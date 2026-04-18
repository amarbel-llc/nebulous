package newsblur

import (
	"encoding/json"
	"testing"
	"time"
)

func testClientWithCache(t *testing.T) *Client {
	t.Helper()
	c := NewClient("test-token")
	if err := c.WithCache(t.TempDir(), time.Hour, nil); err != nil {
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

func TestInvalidateStarredStoryHashManifest(t *testing.T) {
	c := testClientWithCache(t)
	manifest := json.RawMessage(`{"starred_story_hashes":["a"]}`)

	if err := c.PutCachedStarredStoryHashes(manifest); err != nil {
		t.Fatalf("put: %v", err)
	}

	c.InvalidateStarredStoryHashManifest()

	_, ok := c.CachedStarredStoryHashes()
	if ok {
		t.Error("CachedStarredStoryHashes returned true after invalidate")
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

	// Should not panic
	c.InvalidateStarredStoryHashManifest()
}
