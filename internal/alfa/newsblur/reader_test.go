package newsblur

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// testClientAgainstServer builds a Client with cache, but with baseURL and
// httpClient pointed at a local httptest.Server instead of NewsBlur, so
// StarStory/UnstarStory/MarkStoriesRead/MarkStoryUnread's HTTP POST
// actually executes -- letting these tests observe the cache-patch side
// effect that only fires when c.post() succeeds.
func testClientAgainstServer(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	c := &Client{baseURL: server.URL, token: "test-token", httpClient: server.Client()}
	if err := c.WithCache(filepath.Join(t.TempDir(), "manifest.json"), time.Hour, newMemSink()); err != nil {
		t.Fatal(err)
	}
	return c
}

func okServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":"ok"}`))
	}))
}

func TestMarkStoryUnreadPatchesCachedReadStatus(t *testing.T) {
	server := okServer(t)
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if err := c.PutCachedStarredStory("abc", json.RawMessage(`{"story_hash":"abc","read_status":1}`)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := c.MarkStoryUnread(context.Background(), "abc"); err != nil {
		t.Fatalf("MarkStoryUnread: %v", err)
	}

	raw, _ := c.CachedStarredStory("abc")
	var decoded struct {
		ReadStatus int `json:"read_status"`
	}
	json.Unmarshal(raw, &decoded)
	if decoded.ReadStatus != 0 {
		t.Errorf("read_status = %d, want 0 immediately after MarkStoryUnread", decoded.ReadStatus)
	}
}

func TestMarkStoriesReadPatchesCachedReadStatus(t *testing.T) {
	server := okServer(t)
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if err := c.PutCachedStarredStory("a", json.RawMessage(`{"story_hash":"a","read_status":0}`)); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := c.PutCachedStarredStory("b", json.RawMessage(`{"story_hash":"b","read_status":0}`)); err != nil {
		t.Fatalf("seed b: %v", err)
	}

	if _, err := c.MarkStoriesRead(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatalf("MarkStoriesRead: %v", err)
	}

	for _, hash := range []string{"a", "b"} {
		raw, _ := c.CachedStarredStory(hash)
		var decoded struct {
			ReadStatus int `json:"read_status"`
		}
		json.Unmarshal(raw, &decoded)
		if decoded.ReadStatus != 1 {
			t.Errorf("story %s: read_status = %d, want 1 immediately after MarkStoriesRead", hash, decoded.ReadStatus)
		}
	}
}

func TestStarStoryPatchesCachedHashList(t *testing.T) {
	server := okServer(t)
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if err := c.PutCachedStarredStoryHashes(json.RawMessage(`{"starred_story_hashes":["existing"]}`)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := c.StarStory(context.Background(), "new-hash", nil); err != nil {
		t.Fatalf("StarStory: %v", err)
	}

	raw, ok := c.CachedStarredStoryHashes()
	if !ok {
		t.Fatal("CachedStarredStoryHashes returned false")
	}
	hashes, _ := ParseStarredHashes(raw)
	if want := []string{"existing", "new-hash"}; !slices.Equal(hashes, want) {
		t.Errorf("hashes = %v, want %v immediately after StarStory", hashes, want)
	}
}

// Clearing a story's tags must OMIT user_tags. Verified live
// (just debug-probe-clear-cycle): omitting it leaves user_tags [], while
// user_tags= (an explicit empty value, what nebulous#50 sent) stores a single
// empty-string tag [""]. A regression here recreates the empty tags the
// corpus cleanup removes.
func TestSetStoryUserTagsClearsByOmittingTheParam(t *testing.T) {
	var sent bool
	var values []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sent = r.PostForm.Has("user_tags")
		values = r.PostForm["user_tags"]
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":"ok"}`))
	}))
	defer server.Close()
	c := testClientAgainstServer(t, server)

	for _, tags := range [][]string{nil, {}} {
		if _, err := c.SetStoryUserTags(context.Background(), "abc", tags); err != nil {
			t.Fatalf("SetStoryUserTags(%#v): %v", tags, err)
		}
		if sent {
			t.Errorf("SetStoryUserTags(%#v) sent user_tags = %q, want the param omitted (an empty value stores [\"\"])", tags, values)
		}
	}
}

// Multi-tag writes send one user_tags parameter per tag, the shape NewsBlur's
// view reads with getlist; a single comma-joined value would be stored as
// one tag containing commas.
func TestStarAndSetUserTagsSendOneParamPerTag(t *testing.T) {
	var got []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		got = r.PostForm["user_tags"]
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":1}`))
	}))
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if _, err := c.StarStory(context.Background(), "abc", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("StarStory user_tags = %q, want one parameter per tag %q", got, want)
	}
	if _, err := c.SetStoryUserTags(context.Background(), "abc", []string{"x", "y", "z"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"x", "y", "z"}; !slices.Equal(got, want) {
		t.Errorf("SetStoryUserTags user_tags = %q, want one parameter per tag %q", got, want)
	}
}

func TestLiveStoryUserTagsReadsFromNewsBlurNotTheCache(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reader/starred_stories" || r.URL.Query().Get("h") != "abc" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"stories":[{"story_hash":"other","user_tags":["no"]},{"story_hash":"abc","user_tags":["live","tags"]}]}`))
	}))
	defer server.Close()
	c := testClientAgainstServer(t, server)

	// A stale cached copy must not influence the answer.
	if err := c.PutCachedStarredStory("abc", json.RawMessage(`{"story_hash":"abc","user_tags":["stale"]}`)); err != nil {
		t.Fatal(err)
	}
	got, err := c.LiveStoryUserTags(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"live", "tags"}; !slices.Equal(got, want) {
		t.Errorf("LiveStoryUserTags = %v, want %v", got, want)
	}
}

func TestLiveStoryUserTagsMissingStoryAndNoTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("h") == "untagged" {
			w.Write([]byte(`{"stories":[{"story_hash":"untagged"}]}`))
			return
		}
		w.Write([]byte(`{"stories":[]}`))
	}))
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if got, err := c.LiveStoryUserTags(context.Background(), "untagged"); err != nil || got == nil || len(got) != 0 {
		t.Errorf("untagged story: got %v, %v; want an empty non-nil slice", got, err)
	}
	if _, err := c.LiveStoryUserTags(context.Background(), "gone"); err == nil {
		t.Error("a story NewsBlur does not return was reported as having no tags instead of an error")
	}
}

func TestSetStoryUserTagsPatchesCachedHashList(t *testing.T) {
	server := okServer(t)
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if err := c.PutCachedStarredStoryHashes(json.RawMessage(`{"starred_story_hashes":["existing"]}`)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := c.SetStoryUserTags(context.Background(), "existing", []string{"a", "b"}); err != nil {
		t.Fatalf("SetStoryUserTags: %v", err)
	}

	raw, ok := c.CachedStarredStoryHashes()
	if !ok {
		t.Fatal("CachedStarredStoryHashes returned false")
	}
	hashes, _ := ParseStarredHashes(raw)
	if want := []string{"existing"}; !slices.Equal(hashes, want) {
		t.Errorf("hashes = %v, want %v (SetStoryUserTags on an already-starred story shouldn't duplicate its hash)", hashes, want)
	}
}

// nebulous#53: without patching the cached story blob's own user_tags
// field (not just the hash list above), a read immediately after this call
// stays stale not just until the next fetch but permanently, since
// cmd/nebulous/main.go's fetch never re-fetches an already-cached hash.
func TestSetStoryUserTagsPatchesCachedStoryBlob(t *testing.T) {
	server := okServer(t)
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if err := c.PutCachedStarredStory("existing", json.RawMessage(`{"story_hash":"existing","user_tags":["old"]}`)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := c.SetStoryUserTags(context.Background(), "existing", []string{"a", "b"}); err != nil {
		t.Fatalf("SetStoryUserTags: %v", err)
	}

	raw, ok := c.CachedStarredStory("existing")
	if !ok {
		t.Fatal("CachedStarredStory returned false")
	}
	var decoded struct {
		UserTags []string `json:"user_tags"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal cached story: %v", err)
	}
	if want := []string{"a", "b"}; !slices.Equal(decoded.UserTags, want) {
		t.Errorf("cached story user_tags = %v, want %v immediately after SetStoryUserTags", decoded.UserTags, want)
	}
}

func TestUnstarStoryPatchesCachedHashList(t *testing.T) {
	server := okServer(t)
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if err := c.PutCachedStarredStoryHashes(json.RawMessage(`{"starred_story_hashes":["a","b"]}`)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := c.UnstarStory(context.Background(), "a"); err != nil {
		t.Fatalf("UnstarStory: %v", err)
	}

	raw, _ := c.CachedStarredStoryHashes()
	hashes, _ := ParseStarredHashes(raw)
	if want := []string{"b"}; !slices.Equal(hashes, want) {
		t.Errorf("hashes = %v, want %v immediately after UnstarStory", hashes, want)
	}
}

func TestMarkStoryUnreadDoesNotPatchCacheOnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if err := c.PutCachedStarredStory("abc", json.RawMessage(`{"story_hash":"abc","read_status":1}`)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := c.MarkStoryUnread(context.Background(), "abc"); err == nil {
		t.Fatal("MarkStoryUnread: want error from a failing server")
	}

	raw, _ := c.CachedStarredStory("abc")
	var decoded struct {
		ReadStatus int `json:"read_status"`
	}
	json.Unmarshal(raw, &decoded)
	if decoded.ReadStatus != 1 {
		t.Errorf("read_status = %d, want unchanged 1 when the live call failed", decoded.ReadStatus)
	}
}
