package tools

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
)

func TestStoryStoreParseRecord(t *testing.T) {
	raw := json.RawMessage(`{
		"story_hash": "abc123",
		"story_title": "Test Story",
		"story_authors": "Alice",
		"story_feed_id": 42,
		"story_date": "2024-03-15 10:30:00",
		"story_permalink": "https://example.com/story",
		"story_tags": ["tech", "go"],
		"user_tags": ["interests", "zz-nyc"],
		"story_content": "<p>Hello world of Go programming</p>",
		"starred": true,
		"read_status": 1
	}`)

	rec, err := parseStoryRecord(raw, nil)
	if err != nil {
		t.Fatalf("parseStoryRecord: %v", err)
	}

	if rec.Hash != "abc123" {
		t.Errorf("Hash = %q, want %q", rec.Hash, "abc123")
	}
	if rec.Title != "Test Story" {
		t.Errorf("Title = %q, want %q", rec.Title, "Test Story")
	}
	if rec.FeedID != 42 {
		t.Errorf("FeedID = %d, want %d", rec.FeedID, 42)
	}
	if rec.Year != 2024 {
		t.Errorf("Year = %d, want %d", rec.Year, 2024)
	}
	if rec.Month != 3 {
		t.Errorf("Month = %d, want %d", rec.Month, int(time.March))
	}
	if len(rec.UserTags) != 2 || rec.UserTags[0] != "interests" {
		t.Errorf("UserTags = %v, want [interests zz-nyc]", rec.UserTags)
	}
	if !rec.Starred {
		t.Error("Starred = false, want true")
	}
	if !rec.Words["programming"] {
		t.Error("Words missing 'programming' from content")
	}
	if !rec.Words["hello"] {
		t.Error("Words missing 'hello' from content")
	}
	if !rec.Words["test"] {
		t.Error("Words missing 'test' from title")
	}
}

func TestStoryStoreParseRecordDateFormats(t *testing.T) {
	tests := []struct {
		name      string
		dateStr   string
		wantYear  int
		wantMonth int
	}{
		{"standard", `"2024-03-15 10:30:00"`, 2024, 3},
		{"with timezone", `"2024-12-01 08:00:00+00:00"`, 2024, 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := json.RawMessage(`{"story_hash":"h","story_title":"t","story_feed_id":1,"story_date":` + tt.dateStr + `}`)
			rec, err := parseStoryRecord(raw, nil)
			if err != nil {
				t.Fatalf("parseStoryRecord: %v", err)
			}
			if rec.Year != tt.wantYear {
				t.Errorf("Year = %d, want %d", rec.Year, tt.wantYear)
			}
			if rec.Month != tt.wantMonth {
				t.Errorf("Month = %d, want %d", rec.Month, tt.wantMonth)
			}
		})
	}
}

func TestParseStarredHashes(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "envelope format",
			input: `{"starred_story_hashes":["abc","def","ghi"]}`,
			want:  []string{"abc", "def", "ghi"},
		},
		{
			name:  "flat array",
			input: `["abc","def"]`,
			want:  []string{"abc", "def"},
		},
		{
			name:  "empty envelope returns empty slice",
			input: `{"starred_story_hashes":[]}`,
			want:  []string{},
		},
		{
			name:    "invalid json",
			input:   `not json`,
			wantErr: true,
		},
		{
			name:    "wrong type",
			input:   `42`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newsblur.ParseStarredHashes(json.RawMessage(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d hashes, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("hash[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func testClientWithCachedStories(t *testing.T) *newsblur.Client {
	t.Helper()
	c := newsblur.NewClient("test-token")
	if err := c.WithCache(filepath.Join(t.TempDir(), "manifest.json"), time.Hour, newMemSink()); err != nil {
		t.Fatal(err)
	}

	manifest := json.RawMessage(`{"starred_story_hashes":["hash1","hash2"]}`)
	if err := c.PutCachedStarredStoryHashes(manifest); err != nil {
		t.Fatalf("PutCachedStarredStoryHashes: %v", err)
	}

	story1 := json.RawMessage(`{
		"story_hash": "hash1",
		"story_title": "First Story",
		"story_authors": "Alice",
		"story_feed_id": 1,
		"story_date": "2024-06-15 10:00:00",
		"story_permalink": "https://example.com/1",
		"story_tags": ["tech"],
		"user_tags": ["interests"],
		"story_content": "<p>Content about Go programming</p>",
		"starred": true,
		"read_status": 1
	}`)

	story2 := json.RawMessage(`{
		"story_hash": "hash2",
		"story_title": "Second Story",
		"story_authors": "Bob",
		"story_feed_id": 2,
		"story_date": "2024-03-10 08:00:00",
		"story_permalink": "https://example.com/2",
		"story_tags": ["news"],
		"user_tags": ["zz-nyc"],
		"story_content": "<p>NYC bikes and transit</p>",
		"starred": true,
		"read_status": 0
	}`)

	if err := c.PutCachedStarredStory("hash1", story1); err != nil {
		t.Fatalf("PutCachedStarredStory: %v", err)
	}
	if err := c.PutCachedStarredStory("hash2", story2); err != nil {
		t.Fatalf("PutCachedStarredStory: %v", err)
	}

	return c
}

func TestBuildFromCachedStories(t *testing.T) {
	c := testClientWithCachedStories(t)
	s := newStoryStore(c)

	if err := s.ensureBuilt(); err != nil {
		t.Fatalf("ensureBuilt: %v", err)
	}

	snap, err := s.current()
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	if len(snap.stories) != 2 {
		t.Fatalf("expected 2 stories, got %d", len(snap.stories))
	}

	// Should be sorted by date descending: hash1 (2024-06) before hash2 (2024-03)
	if snap.stories[0].Hash != "hash1" {
		t.Errorf("first story = %q, want hash1 (newest)", snap.stories[0].Hash)
	}
	if snap.stories[1].Hash != "hash2" {
		t.Errorf("second story = %q, want hash2", snap.stories[1].Hash)
	}

	// All stories should be marked starred
	for _, rec := range snap.stories {
		if !rec.Starred {
			t.Errorf("story %s not marked starred", rec.Hash)
		}
	}

	// Word index should contain words from both stories
	if len(snap.words["programming"]) == 0 {
		t.Error("word index missing 'programming'")
	}
	if len(snap.words["bikes"]) == 0 {
		t.Error("word index missing 'bikes'")
	}

	// User tags should be counted
	if snap.userTags["interests"] != 1 {
		t.Errorf("userTags[interests] = %d, want 1", snap.userTags["interests"])
	}
	if snap.userTags["zz-nyc"] != 1 {
		t.Errorf("userTags[zz-nyc] = %d, want 1", snap.userTags["zz-nyc"])
	}
}

func TestRawStoryByHash(t *testing.T) {
	c := testClientWithCachedStories(t)
	s := newStoryStore(c)

	raw, ok := s.rawStoryByHash("hash1")
	if !ok {
		t.Fatal("rawStoryByHash returned false for existing hash")
	}

	var story struct {
		Hash  string `json:"story_hash"`
		Title string `json:"story_title"`
	}
	if err := json.Unmarshal(raw, &story); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if story.Hash != "hash1" {
		t.Errorf("Hash = %q, want hash1", story.Hash)
	}
	if story.Title != "First Story" {
		t.Errorf("Title = %q, want 'First Story'", story.Title)
	}

	_, ok = s.rawStoryByHash("missing")
	if ok {
		t.Error("rawStoryByHash returned true for missing hash")
	}
}

func TestBuildEmptyManifest(t *testing.T) {
	c := newsblur.NewClient("test-token")
	if err := c.WithCache(filepath.Join(t.TempDir(), "manifest.json"), time.Hour, newMemSink()); err != nil {
		t.Fatal(err)
	}

	// No manifest cached at all
	s := newStoryStore(c)
	snap, err := s.current()
	if err != nil {
		t.Fatalf("current with no manifest: %v", err)
	}
	if len(snap.stories) != 0 {
		t.Errorf("expected 0 stories, got %d", len(snap.stories))
	}
}

// storyStore used to build its index exactly once via sync.Once and never
// rebuild — a concurrently-running `nebulous fetch` process's newly-cached
// stories were invisible to story_query/facets/tag discovery for the
// server's whole lifetime. This simulates that scenario directly: a
// SEPARATE newsblur.Client instance over the same manifest file stands in
// for the independent fetch process (mirrors
// internal/0/manifest's TestManifestLookupPicksUpConcurrentWriterAfterStaleness).
func TestStoryStorePicksUpConcurrentWriterAfterStaleness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	// Shared sink: production's two processes share one on-disk madder blob
	// store, so a per-client in-memory sink would only simulate the
	// manifest's file being shared, not the blob content behind it.
	sink := newMemSink()

	c := newsblur.NewClient("test-token")
	if err := c.WithCache(path, time.Hour, sink); err != nil {
		t.Fatal(err)
	}
	s := newStoryStore(c)

	snap, err := s.current()
	if err != nil {
		t.Fatalf("current (initial): %v", err)
	}
	if len(snap.stories) != 0 {
		t.Fatalf("expected 0 stories before any writer, got %d", len(snap.stories))
	}

	other := newsblur.NewClient("test-token")
	if err := other.WithCache(path, time.Hour, sink); err != nil {
		t.Fatal(err)
	}
	manifest := json.RawMessage(`{"starred_story_hashes":["hash1"]}`)
	if err := other.PutCachedStarredStoryHashes(manifest); err != nil {
		t.Fatalf("other.PutCachedStarredStoryHashes: %v", err)
	}
	story := json.RawMessage(`{
		"story_hash": "hash1",
		"story_title": "First Story",
		"story_feed_id": 1,
		"story_date": "2024-06-15 10:00:00",
		"story_content": "",
		"starred": true
	}`)
	if err := other.PutCachedStarredStory("hash1", story); err != nil {
		t.Fatalf("other.PutCachedStarredStory: %v", err)
	}

	// Wait out both debounce windows: storyStore's own lastCheckedAt AND
	// the underlying manifest.Manifest's independent lastCheckedAt (a
	// separate instance owned by c's cache, unexported and unreachable
	// from this package) — c's Lookup won't re-stat the file until its
	// own window elapses either.
	time.Sleep(staleCheckDebounce + 100*time.Millisecond)

	snap, err = s.current()
	if err != nil {
		t.Fatalf("current (after concurrent write): %v", err)
	}
	if len(snap.stories) != 1 {
		t.Fatalf("expected 1 story after concurrent writer + staleness check, got %d", len(snap.stories))
	}
	if snap.stories[0].Hash != "hash1" {
		t.Errorf("story hash = %q, want hash1", snap.stories[0].Hash)
	}
}

// The outer staleCache and the client's own underlying manifest.Manifest
// each track their own independent debounce clock over the same file. A
// direct Cached* read (as read_index.go's per-story reads do, bypassing
// storyStore entirely) resets the manifest's clock without touching
// storyStore's — so if a rebuild only forced storyStore's own debounce
// past its window and relied on the manifest to have *naturally* elapsed
// its own, a rebuild could read stale in-memory manifest data and then
// believe itself caught up (recording the file's true current mtime)
// until the file changes again. build() calls client.ForceManifestRefresh
// specifically to close this gap; this reproduces the desync directly by
// only clearing storyStore's clock, leaving the manifest's freshly reset.
func TestStoryStorePicksUpConcurrentWriterDespiteManifestDebounceDesync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	sink := newMemSink()

	c := newsblur.NewClient("test-token")
	if err := c.WithCache(path, time.Hour, sink); err != nil {
		t.Fatal(err)
	}
	s := newStoryStore(c)

	if _, err := s.current(); err != nil {
		t.Fatalf("current (initial): %v", err)
	}

	// Direct read resets the manifest's own debounce clock to "now",
	// independent of storyStore's clock.
	c.CachedStarredStoryHashes()

	other := newsblur.NewClient("test-token")
	if err := other.WithCache(path, time.Hour, sink); err != nil {
		t.Fatal(err)
	}
	manifest := json.RawMessage(`{"starred_story_hashes":["hash1"]}`)
	if err := other.PutCachedStarredStoryHashes(manifest); err != nil {
		t.Fatalf("other.PutCachedStarredStoryHashes: %v", err)
	}
	story := json.RawMessage(`{
		"story_hash": "hash1",
		"story_title": "First Story",
		"story_feed_id": 1,
		"story_date": "2024-06-15 10:00:00",
		"story_content": "",
		"starred": true
	}`)
	if err := other.PutCachedStarredStory("hash1", story); err != nil {
		t.Fatalf("other.PutCachedStarredStory: %v", err)
	}

	// Only clear storyStore's own clock — deliberately NOT waiting out a
	// second window for the manifest's independently-timed clock, which
	// the direct read above just reset to "now". Without
	// ForceManifestRefresh in build(), this would still observe pre-write
	// data despite storyStore believing a rebuild is due.
	s.cache.mu.Lock()
	s.cache.lastCheckedAt = time.Time{}
	s.cache.mu.Unlock()

	snap, err := s.current()
	if err != nil {
		t.Fatalf("current (after desync): %v", err)
	}
	if len(snap.stories) != 1 {
		t.Fatalf("expected 1 story despite manifest debounce desync, got %d", len(snap.stories))
	}
	if snap.stories[0].Hash != "hash1" {
		t.Errorf("story hash = %q, want hash1", snap.stories[0].Hash)
	}
}

// Within the debounce window, current() must NOT reload even if the
// manifest changed underneath it — the cost-bounding half of the fix (a
// busy fetch run rewrites the manifest on every single Record(), and
// storyStore's build does a full corpus scan).
func TestStoryStoreDoesNotReloadWithinDebounceWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	sink := newMemSink()

	c := newsblur.NewClient("test-token")
	if err := c.WithCache(path, time.Hour, sink); err != nil {
		t.Fatal(err)
	}
	s := newStoryStore(c)

	if _, err := s.current(); err != nil {
		t.Fatalf("current (initial): %v", err)
	}

	other := newsblur.NewClient("test-token")
	if err := other.WithCache(path, time.Hour, sink); err != nil {
		t.Fatal(err)
	}
	manifest := json.RawMessage(`{"starred_story_hashes":["hash1"]}`)
	if err := other.PutCachedStarredStoryHashes(manifest); err != nil {
		t.Fatalf("other.PutCachedStarredStoryHashes: %v", err)
	}
	story := json.RawMessage(`{
		"story_hash": "hash1",
		"story_title": "First Story",
		"story_feed_id": 1,
		"story_date": "2024-06-15 10:00:00",
		"story_content": "",
		"starred": true
	}`)
	if err := other.PutCachedStarredStory("hash1", story); err != nil {
		t.Fatalf("other.PutCachedStarredStory: %v", err)
	}

	// s's debounce window (started at construction, moments ago) hasn't
	// elapsed — it must not see the other process's write yet.
	snap, err := s.current()
	if err != nil {
		t.Fatalf("current (within debounce window): %v", err)
	}
	if len(snap.stories) != 0 {
		t.Errorf("current picked up a concurrent write within the debounce window, want debounced (0 stories, got %d)", len(snap.stories))
	}
}

func TestBuildSkipsMissingStories(t *testing.T) {
	c := newsblur.NewClient("test-token")
	if err := c.WithCache(filepath.Join(t.TempDir(), "manifest.json"), time.Hour, newMemSink()); err != nil {
		t.Fatal(err)
	}

	// Manifest references hashes but only one is cached
	manifest := json.RawMessage(`{"starred_story_hashes":["cached","missing"]}`)
	if err := c.PutCachedStarredStoryHashes(manifest); err != nil {
		t.Fatalf("put manifest: %v", err)
	}

	story := json.RawMessage(`{
		"story_hash": "cached",
		"story_title": "Cached Story",
		"story_feed_id": 1,
		"story_date": "2024-01-01 00:00:00",
		"story_content": ""
	}`)
	if err := c.PutCachedStarredStory("cached", story); err != nil {
		t.Fatalf("put story: %v", err)
	}

	s := newStoryStore(c)
	snap, err := s.current()
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	if len(snap.stories) != 1 {
		t.Fatalf("expected 1 story (skipping missing), got %d", len(snap.stories))
	}
	if snap.stories[0].Hash != "cached" {
		t.Errorf("story hash = %q, want 'cached'", snap.stories[0].Hash)
	}
}
