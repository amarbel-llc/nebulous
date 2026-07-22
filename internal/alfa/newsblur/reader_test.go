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

// nebulous#50: SetStoryUserTags must send an explicit user_tags= param
// even when clearing all tags -- unlike StarStory, which omits the param
// entirely when tags is empty (fine for a first star, since there's
// nothing yet to clear; verified live against a real account that an
// omitted param does not clear an already-tagged story's tags the way an
// explicit empty value does).
func TestSetStoryUserTagsSendsExplicitEmptyParam(t *testing.T) {
	var gotForm string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if !r.PostForm.Has("user_tags") {
			gotForm = "MISSING"
		} else {
			gotForm = "present:" + r.PostForm.Get("user_tags")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":"ok"}`))
	}))
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if _, err := c.SetStoryUserTags(context.Background(), "abc", nil); err != nil {
		t.Fatalf("SetStoryUserTags: %v", err)
	}
	if gotForm != "present:" {
		t.Errorf("user_tags form param = %q, want an explicit empty value sent", gotForm)
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
