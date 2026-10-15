package newsblur

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// The bodies below are the shapes NewsBlur's reader views return (apps/reader
// views.py: _mark_story_as_starred, _mark_story_as_unstarred, add_url).
func TestMutationFailureRecognisesNewsBlurShapes(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantFail bool
		wantIn   []string // substrings expected in the error text
	}{
		{"star success", `{"code":1,"message":"","starred_count":3,"starred_counts":[]}`, false, nil},
		{
			"star story not found", `{"code":-1,"message":"Could not save story, not found","story_hash":"1:abc"}`, true,
			[]string{"Could not save story, not found", "1:abc"},
		},
		{
			"multi-story star, one failed", `[{"code":1,"message":""},{"code":-1,"message":"Could not save story, not found","story_hash":"2:def"}]`, true,
			[]string{"2:def"},
		},
		{"multi-story star, all ok", `[{"code":1,"message":""},{"code":1,"message":""}]`, false, nil},
		{"unstar success", `{"code":1,"starred_counts":[],"messages":[]}`, false, nil},
		{
			"unstar not found hides in messages", `{"code":1,"starred_counts":null,"messages":[{"code":-1,"message":"Could not unsave story, not found","story_hash":"3:ghi"}]}`, true,
			[]string{"Could not unsave story, not found", "3:ghi"},
		},
		{
			"subscribe failure", `{"code":-1,"message":"This address does not point to an RSS feed."}`, true,
			[]string{"does not point to an RSS feed"},
		},
		{"no code at all", `{"result":"ok"}`, false, nil},
		{"non-integer code is not our shape", `{"code":"weird"}`, false, nil},
		{"not json", `<html>oops</html>`, false, nil},
		{"empty", ``, false, nil},
		{"json scalar", `42`, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := mutationFailure([]byte(tc.body))
			if (err != nil) != tc.wantFail {
				t.Fatalf("mutationFailure(%s) = %v, wantFail %v", tc.body, err, tc.wantFail)
			}
			if err == nil {
				return
			}
			var me *MutationError
			if !errors.As(err, &me) || len(me.Failures) == 0 {
				t.Fatalf("error %v is not a *MutationError with failures", err)
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err.Error(), want)
				}
			}
		})
	}
}

func rejectServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
}

// A write NewsBlur rejects inside an HTTP 200 must fail, and must NOT patch
// the local cache to claim the new tags (tag-mutation review, finding 1).
func TestSetStoryUserTagsRejectedBodyFailsAndLeavesCacheAlone(t *testing.T) {
	server := rejectServer(t, `{"code":-1,"message":"Could not save story, not found","story_hash":"existing"}`)
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if err := c.PutCachedStarredStory("existing", json.RawMessage(`{"story_hash":"existing","user_tags":["old"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.PutCachedStarredStoryHashes(json.RawMessage(`{"starred_story_hashes":["existing"]}`)); err != nil {
		t.Fatal(err)
	}

	_, err := c.SetStoryUserTags(context.Background(), "existing", []string{"new"})
	var me *MutationError
	if !errors.As(err, &me) {
		t.Fatalf("SetStoryUserTags err = %v, want a *MutationError", err)
	}

	raw, _ := c.CachedStarredStory("existing")
	var decoded struct {
		UserTags []string `json:"user_tags"`
	}
	json.Unmarshal(raw, &decoded)
	if want := []string{"old"}; !slices.Equal(decoded.UserTags, want) {
		t.Errorf("cached user_tags = %v, want %v untouched after a rejected write", decoded.UserTags, want)
	}
}

func TestStarAndUnstarRejectedBodyDoNotPatchHashList(t *testing.T) {
	t.Run("star", func(t *testing.T) {
		server := rejectServer(t, `{"code":-1,"message":"Could not save story, not found","story_hash":"ghost"}`)
		defer server.Close()
		c := testClientAgainstServer(t, server)
		if err := c.PutCachedStarredStoryHashes(json.RawMessage(`{"starred_story_hashes":["a"]}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := c.StarStory(context.Background(), "ghost", nil); err == nil {
			t.Fatal("StarStory accepted a code -1 body")
		}
		raw, _ := c.CachedStarredStoryHashes()
		if hashes, _ := ParseStarredHashes(raw); !slices.Equal(hashes, []string{"a"}) {
			t.Errorf("hash list = %v, want a bogus hash NOT added", hashes)
		}
	})
	t.Run("unstar", func(t *testing.T) {
		server := rejectServer(t, `{"code":1,"starred_counts":null,"messages":[{"code":-1,"message":"Could not unsave story, not found","story_hash":"a"}]}`)
		defer server.Close()
		c := testClientAgainstServer(t, server)
		if err := c.PutCachedStarredStoryHashes(json.RawMessage(`{"starred_story_hashes":["a","b"]}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := c.UnstarStory(context.Background(), "a"); err == nil {
			t.Fatal("UnstarStory accepted a body whose messages report failure")
		}
		raw, _ := c.CachedStarredStoryHashes()
		if hashes, _ := ParseStarredHashes(raw); !slices.Equal(hashes, []string{"a", "b"}) {
			t.Errorf("hash list = %v, want unchanged after a rejected unstar", hashes)
		}
	})
}
