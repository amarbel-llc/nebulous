package cgplugin

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/nebulous/internal/bravo/tools"
	deweyerrors "code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// tagFixture seeds a tag "proj" held as a USER tag by two stories and as a
// feed-assigned story tag by a third (which tag renames must ignore).
func tagFixture(t *testing.T) *fakeClient {
	t.Helper()
	idx, fc := setupMutateTest(t)
	idx.tagStories["proj"] = []tools.StoryRef{
		{Hash: "1:a", UserTags: []string{"proj"}},
		{Hash: "2:b", Tags: []string{"proj"}},
		{Hash: "3:c", UserTags: []string{"proj", "other"}},
	}
	fc.liveCounts = map[string]int{"proj": 2, "other": 5}
	return fc
}

func patchTagNode(t *testing.T, tag, body string) ([]string, error) {
	t.Helper()
	return Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://tag/"+tag), bytes.NewReader([]byte(body)))
}

func TestRenameTagPatchesNewsBlurThenTheCachedHolders(t *testing.T) {
	fc := tagFixture(t)

	applied, err := patchTagNode(t, "proj", `{"name":"project"}`)
	if err != nil {
		t.Fatal(err)
	}
	assertApplied(t, applied, []string{"name"})
	want := []string{
		"LiveStarredTagCounts",
		"RenameStarredTag:proj>project",
		"PatchCachedUserTag:proj>project:1:a,3:c", // user-tag holders only; 2:b has it as a feed tag
	}
	if !reflect.DeepEqual(fc.calls, want) {
		t.Errorf("calls = %v, want %v", fc.calls, want)
	}
}

func TestRenameOntoExistingTagNeedsForce(t *testing.T) {
	fc := tagFixture(t)

	_, err := patchTagNode(t, "proj", `{"name":"other"}`)
	if err == nil || !deweyerrors.Is400BadRequest(err) || !strings.Contains(err.Error(), "merge") {
		t.Fatalf("err = %v, want a bad request that mentions merging", err)
	}
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "RenameStarredTag:") {
			t.Errorf("calls = %v, want no rename without force", fc.calls)
		}
	}

	fc.calls = nil
	if _, err := patchTagNode(t, "proj", `{"name":"other","force":true}`); err != nil {
		t.Fatalf("forced merge: %v", err)
	}
	found := false
	for _, c := range fc.calls {
		found = found || c == "RenameStarredTag:proj>other"
	}
	if !found {
		t.Errorf("calls = %v, want the forced rename to reach NewsBlur", fc.calls)
	}
}

// NewsBlur's rename_tag merges case-insensitively, so a rename onto a
// different casing of an existing tag is a merge and needs force; a rename to
// another casing of the tag ITSELF is just a spelling change.
func TestRenameMergeGuardIsCaseInsensitive(t *testing.T) {
	fc := tagFixture(t)
	fc.liveCounts = map[string]int{"proj": 2, "Other": 5}

	if _, err := patchTagNode(t, "proj", `{"name":"other"}`); err == nil || !deweyerrors.Is400BadRequest(err) || !strings.Contains(err.Error(), "Other") {
		t.Fatalf("err = %v, want a bad request naming the existing tag %q", err, "Other")
	}
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "RenameStarredTag:") {
			t.Errorf("calls = %v, want no rename onto a case variant without force", fc.calls)
		}
	}

	fc.calls = nil
	if _, err := patchTagNode(t, "proj", `{"name":"Proj"}`); err != nil {
		t.Fatalf("a casing-only rename of the tag itself must not need force: %v", err)
	}
}

func TestRenameTagValidationAndMissingTag(t *testing.T) {
	fc := tagFixture(t)

	for name, tc := range map[string]struct{ tag, body string }{
		"new name with a comma":      {"proj", `{"name":"a,b"}`},
		"new name with a space":      {"proj", `{"name":"a b"}`},
		"empty new name":             {"proj", `{"name":""}`},
		"same name":                  {"proj", `{"name":"proj"}`},
		"tag NewsBlur does not have": {"ghost", `{"name":"fresh"}`},
	} {
		if _, err := patchTagNode(t, tc.tag, tc.body); err == nil || !deweyerrors.Is400BadRequest(err) {
			t.Errorf("%s: err = %v, want a bad request", name, err)
		}
	}
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "RenameStarredTag:") {
			t.Errorf("calls = %v, want no rename for an invalid request", fc.calls)
		}
	}
}

func TestRenameTagBodyWithoutNameAppliesNothing(t *testing.T) {
	fc := tagFixture(t)
	applied, err := patchTagNode(t, "proj", `{"nonsense":1}`)
	if err != nil {
		t.Fatal(err)
	}
	assertApplied(t, applied, []string{})
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
	}
}

func TestRenameTagNewsBlurRejectionLeavesCacheAlone(t *testing.T) {
	fc := tagFixture(t)
	// The fake's record() returns fc.err for every write: a NewsBlur failure.
	fc.err = errors.New("newsblur rejected the request: Failed to rename tag.")

	_, err := patchTagNode(t, "proj", `{"name":"project"}`)
	if err == nil || deweyerrors.Is400BadRequest(err) {
		t.Fatalf("err = %v, want an upstream failure", err)
	}
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "PatchCachedUserTag:") {
			t.Errorf("calls = %v, want the cache untouched after a failed rename", fc.calls)
		}
	}
}

func TestDeleteTagRemovesItEverywhereThenPatchesTheCache(t *testing.T) {
	fc := tagFixture(t)

	if err := (Plugin{}).DeleteNode(context.Background(), mustURL(t, "newsblur://tag/proj")); err != nil {
		t.Fatal(err)
	}
	want := []string{"LiveStarredTagCounts", "DeleteStarredTag:proj", "PatchCachedUserTag:proj>:1:a,3:c"}
	if !reflect.DeepEqual(fc.calls, want) {
		t.Errorf("calls = %v, want %v", fc.calls, want)
	}
}

func TestDeleteTagThatNewsBlurDoesNotHave(t *testing.T) {
	fc := tagFixture(t)
	err := (Plugin{}).DeleteNode(context.Background(), mustURL(t, "newsblur://tag/ghost"))
	if err == nil || !deweyerrors.Is400BadRequest(err) {
		t.Fatalf("err = %v, want a bad request", err)
	}
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "DeleteStarredTag:") {
			t.Errorf("calls = %v, want no delete", fc.calls)
		}
	}
}

func TestTagNodeLiveCountFailureAborts(t *testing.T) {
	fc := tagFixture(t)
	fc.liveCountsErr = errors.New("newsblur unreachable")

	if err := (Plugin{}).DeleteNode(context.Background(), mustURL(t, "newsblur://tag/proj")); err == nil || deweyerrors.Is400BadRequest(err) {
		t.Errorf("delete: err = %v, want a non-bad-request failure", err)
	}
	if _, err := patchTagNode(t, "proj", `{"name":"project"}`); err == nil || deweyerrors.Is400BadRequest(err) {
		t.Errorf("rename: err = %v, want a non-bad-request failure", err)
	}
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "RenameStarredTag:") || strings.HasPrefix(c, "DeleteStarredTag:") {
			t.Errorf("calls = %v, want no write when NewsBlur's counts could not be read", fc.calls)
		}
	}
}
