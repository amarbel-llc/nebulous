package newsblur

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// savedStoryEnvelope is /api/save_story's success body as captured live
// (just debug-probe-save-story): "story" is a JSON document encoded AGAIN as
// a string, and code 0 (not 1) is success.
const savedStoryEnvelope = `{"code": 0, "message": "Saving story from site: https://example.com/a: ['poc']", ` +
	`"story": "{\"_id\": {\"$oid\": \"6ada0bf26e689b4a088ef980\"}, \"story_feed_id\": 0, ` +
	`\"story_title\": \"A title\", \"story_permalink\": \"https://example.com/a\", ` +
	`\"story_hash\": \"0:45788b\", \"user_tags\": [\"poc\"]}"}`

// saveStoryServer answers /api/save_story with saveBody and the follow-up
// /reader/starred_stories read with the canonical stories envelope,
// recording the form the save POST carried.
func saveStoryServer(t *testing.T, saveBody string, gotForm *url.Values) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/save_story":
			if err := r.ParseForm(); err != nil {
				t.Errorf("ParseForm: %v", err)
			}
			*gotForm = r.PostForm
			w.Write([]byte(saveBody))
		case "/reader/starred_stories":
			w.Write([]byte(`{"stories":[{"story_hash":"0:45788b","story_feed_id":0,` +
				`"story_title":"A title","story_permalink":"https://example.com/a","user_tags":["poc"]}]}`))
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
		}
	}))
}

func TestSaveStorySendsFormAndReturnsHash(t *testing.T) {
	var form url.Values
	server := saveStoryServer(t, savedStoryEnvelope, &form)
	defer server.Close()
	c := testClientAgainstServer(t, server)

	saved, err := c.SaveStory(context.Background(), SaveStoryInput{
		URL:     "https://example.com/a",
		Title:   "A title",
		Content: "<p>body</p>",
		Notes:   "a note",
		Tags:    []string{"poc", "two"},
	})
	if err != nil {
		t.Fatalf("SaveStory: %v", err)
	}
	if saved.Hash != "0:45788b" {
		t.Errorf("Hash = %q, want 0:45788b", saved.Hash)
	}
	if saved.FeedID != 0 {
		t.Errorf("FeedID = %d, want 0", saved.FeedID)
	}

	if got := form.Get("story_url"); got != "https://example.com/a" {
		t.Errorf("story_url = %q", got)
	}
	if got := form.Get("title"); got != "A title" {
		t.Errorf("title = %q", got)
	}
	if got := form.Get("content"); got != "<p>body</p>" {
		t.Errorf("content = %q", got)
	}
	if got := form.Get("user_notes"); got != "a note" {
		t.Errorf("user_notes = %q", got)
	}
	// Each tag its own repeated param: save_story reads getlist("user_tags").
	if got := form["user_tags"]; !slices.Equal(got, []string{"poc", "two"}) {
		t.Errorf("user_tags = %v, want [poc two]", got)
	}
	for _, absent := range []string{"feed_id", "rss_url"} {
		if _, ok := form[absent]; ok {
			t.Errorf("form carries %s, want it omitted when unset", absent)
		}
	}
}

func TestSaveStoryOmitsUnsetOptionalFields(t *testing.T) {
	var form url.Values
	server := saveStoryServer(t, savedStoryEnvelope, &form)
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if _, err := c.SaveStory(context.Background(), SaveStoryInput{URL: "https://example.com/a", Title: "A title"}); err != nil {
		t.Fatalf("SaveStory: %v", err)
	}
	for _, absent := range []string{"content", "user_notes", "user_tags", "feed_id", "rss_url"} {
		if _, ok := form[absent]; ok {
			t.Errorf("form carries %s, want it omitted when unset (an empty content would suppress NewsBlur's own page fetch)", absent)
		}
	}
}

func TestSaveStoryCachesTheStoryAndItsHash(t *testing.T) {
	var form url.Values
	server := saveStoryServer(t, savedStoryEnvelope, &form)
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if _, err := c.SaveStory(context.Background(), SaveStoryInput{URL: "https://example.com/a", Title: "A title"}); err != nil {
		t.Fatalf("SaveStory: %v", err)
	}

	raw, ok := c.CachedStarredStory("0:45788b")
	if !ok {
		t.Fatal("saved story is not cached; a read right after the save would not see it")
	}
	var story struct {
		Hash   string `json:"story_hash"`
		FeedID int    `json:"story_feed_id"`
	}
	if err := json.Unmarshal(raw, &story); err != nil || story.Hash != "0:45788b" {
		t.Errorf("cached story = %s (err %v), want the canonical starred_stories record", raw, err)
	}

	hashesRaw, ok := c.CachedStarredStoryHashes()
	if !ok {
		t.Fatal("starred hash list not patched")
	}
	hashes, err := ParseStarredHashes(hashesRaw)
	if err != nil || !slices.Contains(hashes, "0:45788b") {
		t.Errorf("cached hashes = %v (err %v), want to contain 0:45788b", hashes, err)
	}
}

func TestSaveStoryRejectsFailureAndCachesNothing(t *testing.T) {
	var form url.Values
	server := saveStoryServer(t, `{"code": -1, "message": "Not authenticated, no token supplied and not authenticated.", "story": null}`, &form)
	defer server.Close()
	c := testClientAgainstServer(t, server)

	_, err := c.SaveStory(context.Background(), SaveStoryInput{URL: "https://example.com/a", Title: "A title"})
	if err == nil {
		t.Fatal("SaveStory: expected an error for code -1")
	}
	if !strings.Contains(err.Error(), "Not authenticated") {
		t.Errorf("error %q does not carry NewsBlur's message", err)
	}
	if _, ok := c.CachedStarredStoryHashes(); ok {
		t.Error("hash list was patched for a rejected save")
	}
}

func TestSaveStoryRejectsSuccessWithoutAHash(t *testing.T) {
	var form url.Values
	server := saveStoryServer(t, `{"code": 0, "message": "ok", "story": null}`, &form)
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if _, err := c.SaveStory(context.Background(), SaveStoryInput{URL: "https://example.com/a", Title: "A title"}); err == nil {
		t.Fatal("SaveStory: expected an error when the response carries no story hash")
	}
}

func TestSaveStoryRequiresURLAndTitle(t *testing.T) {
	c := &Client{}
	if _, err := c.SaveStory(context.Background(), SaveStoryInput{Title: "t"}); err == nil {
		t.Error("expected an error for a missing URL")
	}
	if _, err := c.SaveStory(context.Background(), SaveStoryInput{URL: "https://example.com/a"}); err == nil {
		t.Error("expected an error for a missing title")
	}
}
