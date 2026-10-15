package tools

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestIndexedWord(t *testing.T) {
	for query, want := range map[string]string{
		"programming":      "programming",
		"  Programming  ":  "programming",
		"Café":             "cafe",
		"machine-learning": "machine-learning",
	} {
		got, err := IndexedWord(query)
		if err != nil || got != want {
			t.Errorf("IndexedWord(%q) = %q, %v; want %q", query, got, err, want)
		}
	}
	for _, query := range []string{"", "go", "the", "2024", "two words", "roam research", "---"} {
		got, err := IndexedWord(query)
		if err == nil {
			t.Errorf("IndexedWord(%q) = %q, want an error (not a single indexed word)", query, got)
		} else if !errors.Is(err, ErrNotIndexedWord) {
			t.Errorf("IndexedWord(%q) error %v does not wrap ErrNotIndexedWord", query, err)
		}
	}
}

func TestSearchWordsFromOnDiskIndex(t *testing.T) {
	c := testClientWithCachedStories(t)
	r := NewReadIndex(c)

	if _, _, err := r.SearchWords("programming"); !errors.Is(err, ErrWordIndexUnavailable) {
		t.Fatalf("before the index exists: err = %v, want ErrWordIndexUnavailable", err)
	}

	buildIndexFor(t, c)

	hits, complete, err := r.SearchWords("Programming") // folded
	if err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Error("current index reported incomplete")
	}
	if len(hits) != 1 || hits[0].Hash != "hash1" || hits[0].FeedID != 1 {
		t.Errorf("hits = %+v, want only hash1", hits)
	}
	if hits, _, _ := r.SearchWords("zzzznope"); len(hits) != 0 {
		t.Errorf("unknown word matched %v", hits)
	}
	if _, _, err := r.SearchWords("the"); err == nil {
		t.Error("stop word accepted instead of reported")
	}
}

func TestSearchWordsIncompleteWhenCorpusMovedOn(t *testing.T) {
	c := testClientWithCachedStories(t)
	buildIndexFor(t, c)

	if err := c.PutCachedStarredStoryHashes(json.RawMessage(`{"starred_story_hashes":["hash1","hash2","hash3"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.PutCachedStarredStory("hash3", json.RawMessage(`{
		"story_hash":"hash3","story_title":"Third","story_feed_id":3,
		"story_date":"2024-07-01 09:00:00","story_content":"<p>programming again</p>"}`)); err != nil {
		t.Fatal(err)
	}

	r := NewReadIndex(c)
	hits, complete, err := r.SearchWords("programming")
	if err != nil {
		t.Fatal(err)
	}
	if complete {
		t.Error("index older than the corpus reported complete")
	}
	if len(hits) != 1 || hits[0].Hash != "hash1" {
		t.Errorf("hits = %+v, want only the indexed story hash1", hits)
	}
}

func TestSearchWordsReopensAfterSwap(t *testing.T) {
	c := testClientWithCachedStories(t)
	buildIndexFor(t, c)
	r := NewReadIndex(c)

	if hits, _, err := r.SearchWords("bikes"); err != nil || len(hits) != 1 {
		t.Fatalf("first lookup: %v, %v", hits, err)
	}
	if err := c.PutCachedStarredStory("hash2", json.RawMessage(`{
		"story_hash":"hash2","story_title":"Second Story","story_feed_id":2,
		"story_date":"2024-03-10 08:00:00","story_content":"<p>pelicans and herons</p>"}`)); err != nil {
		t.Fatal(err)
	}
	buildIndexFor(t, c) // swaps a new file in by rename

	if hits, _, err := r.SearchWords("pelicans"); err != nil || len(hits) != 1 {
		t.Errorf("lookup after swap: %v, %v (reader kept the old file open?)", hits, err)
	}
}
