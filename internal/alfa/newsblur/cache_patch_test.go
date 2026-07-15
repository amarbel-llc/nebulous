package newsblur

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestPatchCachedStoryReadStatusFlipsField(t *testing.T) {
	c := testClientWithCache(t)
	story := json.RawMessage(`{"story_hash":"abc123","story_title":"Test","read_status":0}`)
	if err := c.PutCachedStarredStory("abc123", story); err != nil {
		t.Fatalf("put: %v", err)
	}

	if err := c.PatchCachedStoryReadStatus("abc123", true); err != nil {
		t.Fatalf("PatchCachedStoryReadStatus: %v", err)
	}

	got, ok := c.CachedStarredStory("abc123")
	if !ok {
		t.Fatal("CachedStarredStory returned false after patch")
	}
	var decoded struct {
		Hash       string `json:"story_hash"`
		Title      string `json:"story_title"`
		ReadStatus int    `json:"read_status"`
	}
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("unmarshal patched story: %v", err)
	}
	if decoded.ReadStatus != 1 {
		t.Errorf("read_status = %d, want 1", decoded.ReadStatus)
	}
	// Other fields must survive the patch untouched.
	if decoded.Hash != "abc123" || decoded.Title != "Test" {
		t.Errorf("patch clobbered unrelated fields: %+v", decoded)
	}

	if err := c.PatchCachedStoryReadStatus("abc123", false); err != nil {
		t.Fatalf("PatchCachedStoryReadStatus (unread): %v", err)
	}
	got, _ = c.CachedStarredStory("abc123")
	json.Unmarshal(got, &decoded)
	if decoded.ReadStatus != 0 {
		t.Errorf("read_status = %d, want 0 after marking unread", decoded.ReadStatus)
	}
}

func TestPatchCachedStoryReadStatusUncachedStoryIsNoop(t *testing.T) {
	c := testClientWithCache(t)
	if err := c.PatchCachedStoryReadStatus("missing", true); err != nil {
		t.Errorf("PatchCachedStoryReadStatus on an uncached story should be a no-op, got: %v", err)
	}
	if _, ok := c.CachedStarredStory("missing"); ok {
		t.Error("PatchCachedStoryReadStatus should not have created a cache entry for an uncached story")
	}
}

func TestPatchCachedStarredStoryHashesAdd(t *testing.T) {
	c := testClientWithCache(t)
	if err := c.PutCachedStarredStoryHashes(json.RawMessage(`{"starred_story_hashes":["a","b"]}`)); err != nil {
		t.Fatalf("put: %v", err)
	}

	if err := c.PatchCachedStarredStoryHashes("c", ""); err != nil {
		t.Fatalf("PatchCachedStarredStoryHashes: %v", err)
	}

	raw, ok := c.CachedStarredStoryHashes()
	if !ok {
		t.Fatal("CachedStarredStoryHashes returned false")
	}
	hashes, err := ParseStarredHashes(raw)
	if err != nil {
		t.Fatalf("ParseStarredHashes: %v", err)
	}
	if want := []string{"a", "b", "c"}; !slices.Equal(hashes, want) {
		t.Errorf("hashes = %v, want %v", hashes, want)
	}
}

func TestPatchCachedStarredStoryHashesAddIsIdempotent(t *testing.T) {
	c := testClientWithCache(t)
	if err := c.PutCachedStarredStoryHashes(json.RawMessage(`{"starred_story_hashes":["a"]}`)); err != nil {
		t.Fatalf("put: %v", err)
	}

	if err := c.PatchCachedStarredStoryHashes("a", ""); err != nil {
		t.Fatalf("PatchCachedStarredStoryHashes: %v", err)
	}

	raw, _ := c.CachedStarredStoryHashes()
	hashes, _ := ParseStarredHashes(raw)
	if want := []string{"a"}; !slices.Equal(hashes, want) {
		t.Errorf("hashes = %v, want %v (re-adding an existing hash should not duplicate it)", hashes, want)
	}
}

func TestPatchCachedStarredStoryHashesRemove(t *testing.T) {
	c := testClientWithCache(t)
	if err := c.PutCachedStarredStoryHashes(json.RawMessage(`{"starred_story_hashes":["a","b","c"]}`)); err != nil {
		t.Fatalf("put: %v", err)
	}

	if err := c.PatchCachedStarredStoryHashes("", "b"); err != nil {
		t.Fatalf("PatchCachedStarredStoryHashes: %v", err)
	}

	raw, _ := c.CachedStarredStoryHashes()
	hashes, _ := ParseStarredHashes(raw)
	if want := []string{"a", "c"}; !slices.Equal(hashes, want) {
		t.Errorf("hashes = %v, want %v", hashes, want)
	}
}

func TestPatchCachedStarredStoryHashesRemoveMissingIsNoop(t *testing.T) {
	c := testClientWithCache(t)
	if err := c.PutCachedStarredStoryHashes(json.RawMessage(`{"starred_story_hashes":["a"]}`)); err != nil {
		t.Fatalf("put: %v", err)
	}

	if err := c.PatchCachedStarredStoryHashes("", "missing"); err != nil {
		t.Fatalf("PatchCachedStarredStoryHashes: %v", err)
	}

	raw, _ := c.CachedStarredStoryHashes()
	hashes, _ := ParseStarredHashes(raw)
	if want := []string{"a"}; !slices.Equal(hashes, want) {
		t.Errorf("hashes = %v, want %v", hashes, want)
	}
}

func TestPatchCachedStarredStoryHashesAddWithNothingCachedYet(t *testing.T) {
	c := testClientWithCache(t)
	if err := c.PatchCachedStarredStoryHashes("a", ""); err != nil {
		t.Fatalf("PatchCachedStarredStoryHashes: %v", err)
	}

	raw, ok := c.CachedStarredStoryHashes()
	if !ok {
		t.Fatal("CachedStarredStoryHashes returned false")
	}
	hashes, _ := ParseStarredHashes(raw)
	if want := []string{"a"}; !slices.Equal(hashes, want) {
		t.Errorf("hashes = %v, want %v", hashes, want)
	}
}
