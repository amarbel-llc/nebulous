package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
	"code.linenisgreat.com/nebulous/internal/alfa/wordindex"
	"code.linenisgreat.com/nebulous/internal/bravo/tools"
)

func seedStarred(t *testing.T, c *newsblur.Client, hash, title string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"story_hash": hash, "story_title": title, "story_feed_id": 1,
		"story_date": "2024-06-15 10:00:00", "story_content": "<p>" + title + " body</p>",
	})
	if err := c.PutCachedStarredStory(hash, body); err != nil {
		t.Fatal(err)
	}
}

func setHashes(t *testing.T, c *newsblur.Client, hashes ...string) {
	t.Helper()
	raw, _ := json.Marshal(map[string][]string{"starred_story_hashes": hashes})
	if err := c.PutCachedStarredStoryHashes(raw); err != nil {
		t.Fatal(err)
	}
}

func indexBuiltAt(t *testing.T, path string) (stories int) {
	t.Helper()
	ix, err := wordindex.Open(path, tools.WordTokenizerVersion)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer ix.Close()
	return ix.Meta().Stories
}

func TestFetchIndexPhaseBuildsRebuildsAndSkips(t *testing.T) {
	c := testFetchClient(t)
	path := newsblur.WordIndexPath(c.ManifestPath())
	setHashes(t, c, "1:aaa")
	seedStarred(t, c, "1:aaa", "pelican")

	// -no-index leaves nothing behind.
	runFetchIndexPhase(context.Background(), c, true, true)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("index written despite -no-index (stat err = %v)", err)
	}

	// Missing file: builds even though the corpus is reported unchanged.
	runFetchIndexPhase(context.Background(), c, false, false)
	if got := indexBuiltAt(t, path); got != 1 {
		t.Fatalf("stories = %d, want 1", got)
	}
	fi1, _ := os.Stat(path)

	// Unchanged corpus, current file: left alone.
	runFetchIndexPhase(context.Background(), c, false, false)
	if fi2, _ := os.Stat(path); !fi2.ModTime().Equal(fi1.ModTime()) || fi2.Size() != fi1.Size() {
		t.Error("index rewritten although corpus unchanged and file current")
	}

	// A new story changes the corpus digest: rebuilt without being told.
	setHashes(t, c, "1:aaa", "2:bbb")
	seedStarred(t, c, "2:bbb", "heron")
	runFetchIndexPhase(context.Background(), c, false, false)
	if got := indexBuiltAt(t, path); got != 2 {
		t.Errorf("stories after new story = %d, want 2", got)
	}

	// corpusChanged forces a rebuild even when the digest still matches
	// (original text can arrive for an existing story).
	before, _ := os.Stat(path)
	runFetchIndexPhase(context.Background(), c, true, false)
	after, _ := os.Stat(path)
	if after.ModTime().Equal(before.ModTime()) && after.Size() == before.Size() {
		t.Error("corpusChanged=true did not rebuild")
	}
}

func TestFetchIndexPhaseFailureDoesNotPanicOrClobber(t *testing.T) {
	c := testFetchClient(t) // no hash manifest cached
	runFetchIndexPhase(context.Background(), c, true, false)
	if _, err := os.Stat(newsblur.WordIndexPath(c.ManifestPath())); !os.IsNotExist(err) {
		t.Errorf("index created without a corpus (stat err = %v)", err)
	}
}
