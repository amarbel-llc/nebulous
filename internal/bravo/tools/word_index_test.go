package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
)

func buildIndexFor(t *testing.T, c *newsblur.Client) string {
	t.Helper()
	path := newsblur.WordIndexPath(c.ManifestPath())
	if _, err := BuildWordIndex(context.Background(), c, path); err != nil {
		t.Fatalf("BuildWordIndex: %v", err)
	}
	return path
}

// The on-disk index must answer exactly what the in-memory word index does.
func TestOnDiskWordIndexMatchesInMemory(t *testing.T) {
	c := testClientWithCachedStories(t)

	memStore := newStoryStore(c) // no index file yet: in-memory fallback
	if snap, err := memStore.current(); err != nil || snap.index != nil {
		t.Fatalf("expected in-memory fallback before the index exists (err=%v)", err)
	}

	buildIndexFor(t, c)
	diskStore := newStoryStore(c)
	snap, err := diskStore.current()
	if err != nil || snap.index == nil {
		t.Fatalf("expected on-disk index to be used (err=%v)", err)
	}
	if len(snap.words) != 0 {
		t.Errorf("on-disk mode still built an in-memory word map (%d words)", len(snap.words))
	}

	year := 2024
	tag := "interests"
	for name, q := range map[string]storyQuery{
		"word in first story":     {Words: []string{"programming"}},
		"word in second story":    {Words: []string{"bikes"}},
		"OR across words":         {Words: []string{"programming", "bikes", "transit"}},
		"word from title":         {Words: []string{"first"}},
		"unknown word":            {Words: []string{"zzzznope"}},
		"word plus year":          {Words: []string{"programming", "bikes"}, Year: &year},
		"word plus tag":           {Words: []string{"programming", "bikes"}, Tag: &tag},
		"no words, plain filters": {Year: &year},
	} {
		want := hashesOf(memStore.query(q))
		got := hashesOf(diskStore.query(q))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: disk = %v, in-memory = %v", name, got, want)
		}
	}
}

// Regression for the first production build: one story containing a giant
// token (a base64 blob) failed the whole index with bbolt's "key too large".
func TestIndexBuildSurvivesGiantTokenAndBothPathsDropIt(t *testing.T) {
	c := testClientWithCachedStories(t)
	giant := strings.Repeat("a", 40000)
	if err := c.PutCachedStarredStory("hash1", json.RawMessage(
		`{"story_hash":"hash1","story_title":"First Story","story_feed_id":1,`+
			`"story_date":"2024-06-15 10:00:00","story_content":"<p>programming `+giant+`</p>"}`)); err != nil {
		t.Fatal(err)
	}

	memStore := newStoryStore(c)
	buildIndexFor(t, c) // must not fail
	diskStore := newStoryStore(c)

	for _, w := range []string{"programming", giant} {
		mem := hashesOf(memStore.query(storyQuery{Words: []string{w}}))
		disk := hashesOf(diskStore.query(storyQuery{Words: []string{w}}))
		if !reflect.DeepEqual(mem, disk) {
			t.Errorf("word %.12q...: in-memory %v, on-disk %v", w, mem, disk)
		}
	}
	if got := hashesOf(diskStore.query(storyQuery{Words: []string{giant}})); len(got) != 0 {
		t.Errorf("giant token was indexed: %v", got)
	}
	if _, err := IndexedWord(giant); err == nil {
		t.Error("IndexedWord accepted a token longer than the index stores")
	}
}

func TestStaleOnDiskIndexIsServedWithNote(t *testing.T) {
	c := testClientWithCachedStories(t)
	buildIndexFor(t, c)

	// A third story arrives after the index was built.
	if err := c.PutCachedStarredStoryHashes(json.RawMessage(`{"starred_story_hashes":["hash1","hash2","hash3"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.PutCachedStarredStory("hash3", json.RawMessage(`{
		"story_hash":"hash3","story_title":"Third","story_feed_id":3,
		"story_date":"2024-07-01 09:00:00","story_content":"<p>Freshly arrived pelican</p>"}`)); err != nil {
		t.Fatal(err)
	}

	s := newStoryStore(c)
	recs, note, err := s.queryDetailed(storyQuery{Words: []string{"programming"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Hash != "hash1" {
		t.Errorf("stale index should still answer for indexed stories, got %v", hashesOf(recs))
	}
	if !strings.Contains(note, "stale") {
		t.Errorf("note = %q, want a stale-index note", note)
	}
	if recs, _, _ := s.queryDetailed(storyQuery{Words: []string{"pelican"}}); len(recs) != 0 {
		t.Errorf("story newer than the index matched a word query: %v", hashesOf(recs))
	}
	if _, ok := s.storyByHash("hash3"); !ok {
		t.Error("metadata for the new story should be visible even though its words are not indexed yet")
	}
}

func TestCorruptOnDiskIndexFallsBackToMemory(t *testing.T) {
	c := testClientWithCachedStories(t)
	path := newsblur.WordIndexPath(c.ManifestPath())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newStoryStore(c)
	snap, err := s.current()
	if err != nil {
		t.Fatal(err)
	}
	if snap.index != nil {
		t.Fatal("corrupt index file was used")
	}
	if recs := s.query(storyQuery{Words: []string{"programming"}}); len(recs) != 1 {
		t.Errorf("fallback in-memory query returned %v", hashesOf(recs))
	}
}

func TestMetadataStoreIgnoresOnDiskIndex(t *testing.T) {
	c := testClientWithCachedStories(t)
	buildIndexFor(t, c)
	snap, err := newMetadataStoryStore(c).current()
	if err != nil {
		t.Fatal(err)
	}
	if snap.index != nil || len(snap.words) != 0 {
		t.Error("metadata-only store opened or built a word index")
	}
}
