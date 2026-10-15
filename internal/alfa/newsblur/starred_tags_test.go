package newsblur

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestLiveStarredTagCountsSkipsFeedEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reader/starred_counts" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"starred_count":9,"starred_counts":[{"tag":"a","count":4},{"tag":null,"feed_id":7,"count":3},{"tag":"b c","count":1}]}`))
	}))
	defer server.Close()
	c := testClientAgainstServer(t, server)

	got, err := c.LiveStarredTagCounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["a"] != 4 || got["b c"] != 1 {
		t.Errorf("counts = %v, want {a:4, 'b c':1}", got)
	}
}

func TestRenameAndDeleteStarredTagSendNewsBlursParameters(t *testing.T) {
	var path string
	var form map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		path, form = r.URL.Path, r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":1,"starred_counts":[]}`))
	}))
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if _, err := c.RenameStarredTag(context.Background(), "old", "new"); err != nil {
		t.Fatal(err)
	}
	if path != "/reader/rename_starred_tag" || form["old_tag_name"][0] != "old" || form["new_tag_name"][0] != "new" {
		t.Errorf("rename sent %s %v", path, form)
	}
	if _, err := c.DeleteStarredTag(context.Background(), "gone"); err != nil {
		t.Fatal(err)
	}
	if path != "/reader/delete_starred_tag" || form["tag_name"][0] != "gone" {
		t.Errorf("delete sent %s %v", path, form)
	}
}

func TestRenameStarredTagRejectionIsAnError(t *testing.T) {
	server := rejectServer(t, `{"code":-1,"message":"Failed to rename tag."}`)
	defer server.Close()
	c := testClientAgainstServer(t, server)
	if _, err := c.RenameStarredTag(context.Background(), "old", "new"); err == nil {
		t.Fatal("a code -1 rename was reported as success")
	}
}

func cachedTags(t *testing.T, c *Client, hash string) []string {
	t.Helper()
	raw, ok := c.CachedStarredStory(hash)
	if !ok {
		t.Fatalf("story %s not cached", hash)
	}
	var s struct {
		UserTags []string `json:"user_tags"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s.UserTags
}

func TestPatchCachedUserTagRenameDeleteAndCollapse(t *testing.T) {
	server := okServer(t)
	defer server.Close()
	c := testClientAgainstServer(t, server)

	for hash, body := range map[string]string{
		"a": `{"story_hash":"a","user_tags":["proj","x"]}`,
		"b": `{"story_hash":"b","user_tags":["project","proj"]}`, // already has the new name
		"c": `{"story_hash":"c","user_tags":["x"]}`,              // does not carry it
		"d": `{"story_hash":"d"}`,                                // no tags at all
	} {
		if err := c.PutCachedStarredStory(hash, json.RawMessage(body)); err != nil {
			t.Fatal(err)
		}
	}

	if err := c.PatchCachedUserTag([]string{"a", "b", "c", "d", "not-cached"}, "proj", "project"); err != nil {
		t.Fatal(err)
	}
	for hash, want := range map[string][]string{
		"a": {"project", "x"},
		"b": {"project"}, // collapsed, not duplicated
		"c": {"x"},
	} {
		if got := cachedTags(t, c, hash); !slices.Equal(got, want) {
			t.Errorf("story %s tags = %v, want %v", hash, got, want)
		}
	}

	if err := c.PatchCachedUserTag([]string{"a", "b"}, "project", ""); err != nil {
		t.Fatal(err)
	}
	if got := cachedTags(t, c, "a"); !slices.Equal(got, []string{"x"}) {
		t.Errorf("after delete, story a tags = %v, want [x]", got)
	}
	if got := cachedTags(t, c, "b"); len(got) != 0 {
		t.Errorf("after delete, story b tags = %v, want none", got)
	}
}
