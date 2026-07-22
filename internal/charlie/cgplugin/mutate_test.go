package cgplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"code.linenisgreat.com/nebulous/internal/bravo/tools"
	deweyerrors "code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// fakeClient records every write call it receives, for asserting exactly
// which underlying newsblur operations a mutation dispatched to.
type fakeClient struct {
	calls []string
	err   error // if set, every call returns this error instead of succeeding
}

func (f *fakeClient) record(call string) (json.RawMessage, error) {
	f.calls = append(f.calls, call)
	if f.err != nil {
		return nil, f.err
	}
	return json.RawMessage(`{}`), nil
}

func (f *fakeClient) StarStory(_ context.Context, hash string, tags []string) (json.RawMessage, error) {
	return f.record("StarStory:" + hash)
}

func (f *fakeClient) SetStoryUserTags(_ context.Context, hash string, tags []string) (json.RawMessage, error) {
	return f.record("SetStoryUserTags:" + hash + ":" + strings.Join(tags, ","))
}

func (f *fakeClient) UnstarStory(_ context.Context, hash string) (json.RawMessage, error) {
	return f.record("UnstarStory:" + hash)
}

func (f *fakeClient) MarkStoriesRead(_ context.Context, hashes []string) (json.RawMessage, error) {
	call := "MarkStoriesRead:"
	for i, h := range hashes {
		if i > 0 {
			call += ","
		}
		call += h
	}
	return f.record(call)
}

func (f *fakeClient) MarkStoryUnread(_ context.Context, hash string) (json.RawMessage, error) {
	return f.record("MarkStoryUnread:" + hash)
}

func (f *fakeClient) RenameFeed(_ context.Context, feedID int, title string) (json.RawMessage, error) {
	return f.record("RenameFeed:" + strconv.Itoa(feedID) + ":" + title)
}

func (f *fakeClient) MoveFeed(_ context.Context, feedID int, inFolder, toFolder string) (json.RawMessage, error) {
	return f.record("MoveFeed:" + strconv.Itoa(feedID) + ":" + inFolder + "->" + toFolder)
}

func (f *fakeClient) Unsubscribe(_ context.Context, feedID int, inFolder string) (json.RawMessage, error) {
	return f.record("Unsubscribe:" + strconv.Itoa(feedID) + ":" + inFolder)
}

func (f *fakeClient) CreateFolder(_ context.Context, folderName, parentFolder string) (json.RawMessage, error) {
	return f.record("CreateFolder:" + folderName + ":" + parentFolder)
}

func (f *fakeClient) RenameFolder(_ context.Context, folderName, newFolderName, inFolder string) (json.RawMessage, error) {
	return f.record("RenameFolder:" + folderName + "->" + newFolderName + ":" + inFolder)
}

func (f *fakeClient) DeleteFolder(_ context.Context, folderName, inFolder string) (json.RawMessage, error) {
	return f.record("DeleteFolder:" + folderName + ":" + inFolder)
}

func (f *fakeClient) MoveFolder(_ context.Context, folderName, inFolder, toFolder string) (json.RawMessage, error) {
	return f.record("MoveFolder:" + folderName + ":" + inFolder + "->" + toFolder)
}

func (f *fakeClient) Subscribe(_ context.Context, feedURL, folder string) (json.RawMessage, error) {
	return f.record("Subscribe:" + feedURL + ":" + folder)
}

func setupMutateTest(t *testing.T) (*fakeIndex, *fakeClient) {
	t.Helper()
	fi := newFakeIndex()
	fc := &fakeClient{}
	index = fi
	client = fc
	t.Cleanup(func() {
		index = nil
		client = nil
	})
	return fi, fc
}

// setFeedFolder overwrites id's cached Folder, keeping every other field
// (including the raw JSON) as newFakeIndex() set it up.
func setFeedFolder(fi *fakeIndex, id, folder string) {
	entry := fi.feedMetadata[id]
	entry.view.Folder = folder
	fi.feedMetadata[id] = entry
}

// assertApplied checks PatchNode's applied return against an exact,
// ordered want -- cutting-garden#182: applied must be non-nil on every
// successful call (nebulous always reports, never opts out), so a nil
// got is itself a failure, not just a length mismatch.
func assertApplied(t *testing.T, got, want []string) {
	t.Helper()
	if got == nil {
		t.Errorf("applied = nil, want %v (non-nil)", want)
		return
	}
	if len(got) != len(want) {
		t.Errorf("applied = %v, want %v", got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("applied = %v, want %v", got, want)
			return
		}
	}
}

func TestCreateNodeStarsNewStory(t *testing.T) {
	_, fc := setupMutateTest(t)

	const newHash = "123:new"
	body := bytes.NewReader([]byte(`{"user_tags":["a","b"]}`))
	err := Plugin{}.CreateNode(context.Background(), mustURL(t, "newsblur://story/"+newHash), body, typeStory)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0] != "StarStory:"+newHash {
		t.Errorf("calls = %v, want [StarStory:%s]", fc.calls, newHash)
	}
}

func TestCreateNodeStoryRejectsUnrecognizedField(t *testing.T) {
	_, fc := setupMutateTest(t)

	const newHash = "123:new"
	body := bytes.NewReader([]byte(`{"userTags":["a","b"]}`)) // wrong key: user_tags is correct
	err := Plugin{}.CreateNode(context.Background(), mustURL(t, "newsblur://story/"+newHash), body, typeStory)
	if err == nil {
		t.Fatal("CreateNode with an unrecognized field: expected an error, got nil")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none (should have errored before calling the client)", fc.calls)
	}
}

func TestCreateNodeErrorsIfStoryExists(t *testing.T) {
	_, fc := setupMutateTest(t)

	err := Plugin{}.CreateNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), bytes.NewReader(nil), typeStory)
	if err == nil {
		t.Fatal("CreateNode on an existing story: expected an error, got nil")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none (should have errored before calling the client)", fc.calls)
	}
}

func TestCreateNodeRejectsNonStoryURI(t *testing.T) {
	setupMutateTest(t)

	err := Plugin{}.CreateNode(context.Background(), mustURL(t, "newsblur://feed/123"), bytes.NewReader(nil), typeFeed)
	if err == nil {
		t.Fatal("CreateNode on a feed URI: expected an error, got nil")
	}
}

func TestPutNodeIsNotSupported(t *testing.T) {
	setupMutateTest(t)

	err := Plugin{}.PutNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), bytes.NewReader(nil))
	if err == nil {
		t.Fatal("PutNode: expected an error (unsupported), got nil")
	}
}

func TestPatchNodeStoryMarksRead(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"read":true}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{"read"})
	if len(fc.calls) != 1 || fc.calls[0] != "MarkStoriesRead:"+sampleHash {
		t.Errorf("calls = %v, want [MarkStoriesRead:%s]", fc.calls, sampleHash)
	}
}

func TestPatchNodeStoryMarksUnread(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"read":false}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{"read"})
	if len(fc.calls) != 1 || fc.calls[0] != "MarkStoryUnread:"+sampleHash {
		t.Errorf("calls = %v, want [MarkStoryUnread:%s]", fc.calls, sampleHash)
	}
}

// nebulous#50: patch_node's user_tags REPLACES the story's tag set --
// verified live against a real account (starring with ["a"] then ["b"]
// left only "b", not both).
func TestPatchNodeStorySetsUserTags(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"user_tags":["a","b"]}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{"user_tags"})
	if len(fc.calls) != 1 || fc.calls[0] != "SetStoryUserTags:"+sampleHash+":a,b" {
		t.Errorf("calls = %v, want [SetStoryUserTags:%s:a,b]", fc.calls, sampleHash)
	}
}

// An explicitly-present-but-empty user_tags array clears all tags --
// distinct from user_tags being absent (TestPatchNodeStoryNoOpWhenUserTags
// Absent below), the whole reason UserTags is a *[]string rather than a
// plain []string on storyPatchBody.
func TestPatchNodeStoryEmptyUserTagsClearsTags(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"user_tags":[]}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{"user_tags"})
	if len(fc.calls) != 1 || fc.calls[0] != "SetStoryUserTags:"+sampleHash+":" {
		t.Errorf("calls = %v, want [SetStoryUserTags:%s:] (empty tags)", fc.calls, sampleHash)
	}
}

// user_tags absent from the body must leave tags untouched: no
// SetStoryUserTags call at all, not a call with an empty tag list (that's
// TestPatchNodeStoryEmptyUserTagsClearsTags' job, a different body).
func TestPatchNodeStoryNoOpWhenUserTagsAbsent(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"read":true}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{"read"})
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "SetStoryUserTags:") {
			t.Errorf("calls = %v, want no SetStoryUserTags call (user_tags was absent)", fc.calls)
		}
	}
}

// read and user_tags both present in one body both fire, same as feed's
// rename+move combination.
func TestPatchNodeStoryReadAndUserTagsBothFireFromOneBody(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"read":true,"user_tags":["a"]}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{"read", "user_tags"})
	if len(fc.calls) != 2 {
		t.Fatalf("calls = %v, want 2 (mark read + set tags)", fc.calls)
	}
	if fc.calls[0] != "MarkStoriesRead:"+sampleHash {
		t.Errorf("calls[0] = %q, want MarkStoriesRead:%s", fc.calls[0], sampleHash)
	}
	if fc.calls[1] != "SetStoryUserTags:"+sampleHash+":a" {
		t.Errorf("calls[1] = %q, want SetStoryUserTags:%s:a", fc.calls[1], sampleHash)
	}
}

// A non-array user_tags value is a decode error, surfaced with field
// context rather than a generic "invalid body" message. cutting-garden#182:
// a recognized key with an unusable value is a caller bug, not a
// forward-compatibility concern -- tolerance only ever covers keys this
// plugin has never heard of, never a bad value for one it does.
func TestPatchNodeStoryUserTagsWrongTypeErrors(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"user_tags":"not-an-array"}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err == nil {
		t.Fatal("PatchNode with user_tags as a string: expected an error, got nil")
	}
	if applied != nil {
		t.Errorf("applied = %v, want nil on error", applied)
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
	}
}

// An unrecognized key alongside a recognized key with a bad value must
// still error on the bad value -- tolerance for unknown keys doesn't
// extend to masking a real decode failure elsewhere in the same body.
func TestPatchNodeStoryWrongTypeErrorsEvenAlongsideUnrecognizedField(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"read":"not-a-bool","starred":true}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err == nil {
		t.Fatal("PatchNode with read as a string, alongside an unrecognized field: expected an error, got nil")
	}
	if applied != nil {
		t.Errorf("applied = %v, want nil on error", applied)
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
	}
}

// A patch body with `read` absent must be a true no-op: no client call at
// all, not a degenerate "unread unchanged" call. applied must still be
// non-nil (empty, not nil) -- cutting-garden#182's authoritative "nothing
// applied" signal, distinct from a plugin that doesn't report at all.
func TestPatchNodeStoryNoOpWhenReadAbsent(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{})
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none (read absent from body)", fc.calls)
	}
}

func TestPatchNodeStoryErrorsIfAbsent(t *testing.T) {
	setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"read":true}`))
	_, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/does-not-exist"), body)
	if err == nil {
		t.Fatal("PatchNode on a missing story: expected an error, got nil")
	}
}

// cutting-garden#182 (following directly from #180): a field storyPatchBody
// doesn't declare is now TOLERATED, not rejected -- a newer caller naming
// a field this build doesn't know about must still succeed. The
// forward-compatibility #180's blanket rejection gave up is recovered
// here; #180's actual defect (reporting plain success for a request that
// changed nothing) stays fixed via the authoritative empty applied
// instead of an error.
func TestPatchNodeStoryToleratesUnrecognizedField(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"starred":true}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode with only an unrecognized field: expected no error (tolerated), got %v", err)
	}
	assertApplied(t, applied, []string{})
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none (nothing recognized to apply)", fc.calls)
	}
}

// A body mixing a recognized field with an unrecognized one applies the
// recognized one and reports it in applied; the unrecognized one is
// silently dropped, not reported and not an error.
func TestPatchNodeStoryAppliesRecognizedFieldIgnoringUnrecognizedOne(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"read":true,"starred":true}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{"read"})
	if len(fc.calls) != 1 || fc.calls[0] != "MarkStoriesRead:"+sampleHash {
		t.Errorf("calls = %v, want [MarkStoriesRead:%s]", fc.calls, sampleHash)
	}
}

func TestPatchNodeFeedToleratesUnrecognizedField(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"active":false}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://feed/123"), body)
	if err != nil {
		t.Fatalf("PatchNode(feed) with only an unrecognized field: expected no error (tolerated), got %v", err)
	}
	assertApplied(t, applied, []string{})
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
	}
}

// A recognized field with an unusable value is still a hard error --
// cutting-garden#182's tolerance covers unknown KEYS only.
func TestPatchNodeFeedTitleWrongTypeErrors(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"title":123}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://feed/123"), body)
	if err == nil {
		t.Fatal("PatchNode(feed) with title as a number: expected an error, got nil")
	}
	if applied != nil {
		t.Errorf("applied = %v, want nil on error", applied)
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
	}
}

func TestCreateChildRejectsUnrecognizedField(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"url":"https://example.com/feed","folderr":"Blogs"}`)) // typo'd field
	_, err := Plugin{}.CreateChild(context.Background(), mustURL(t, "newsblur://feeds"), body, typeFeed)
	if err == nil {
		t.Fatal("CreateChild with an unrecognized field: expected an error, got nil")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none (should have errored before calling the client)", fc.calls)
	}
}

func TestPatchNodeFeedRenameOnly(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"title":"New Title"}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://feed/123"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{"title"})
	if len(fc.calls) != 1 || fc.calls[0] != "RenameFeed:123:New Title" {
		t.Errorf("calls = %v, want [RenameFeed:123:New Title]", fc.calls)
	}
}

func TestPatchNodeFeedMoveOnly(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"folder":"new","in_folder":"old"}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://feed/123"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{"folder"})
	if len(fc.calls) != 1 || fc.calls[0] != "MoveFeed:123:old->new" {
		t.Errorf("calls = %v, want [MoveFeed:123:old->new]", fc.calls)
	}
}

// A "folder" field without an accompanying "in_folder" must error, not
// silently fall back to the local index's (potentially stale) view of
// the feed's current folder -- see feedPatchBody's own doc comment.
func TestPatchNodeFeedMoveWithoutInFolderErrors(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"folder":"new"}`))
	_, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://feed/123"), body)
	if err == nil {
		t.Fatal("PatchNode with folder but no in_folder: expected an error, got nil")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none (should have errored before calling the client)", fc.calls)
	}
}

func TestPatchNodeFeedRenameAndMoveBothFireFromOneBody(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"title":"New Title","folder":"new","in_folder":"old"}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://feed/123"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{"title", "folder"})
	if len(fc.calls) != 2 {
		t.Fatalf("calls = %v, want 2 (rename + move)", fc.calls)
	}
	if fc.calls[0] != "RenameFeed:123:New Title" {
		t.Errorf("calls[0] = %q, want RenameFeed:123:New Title", fc.calls[0])
	}
	if fc.calls[1] != "MoveFeed:123:old->new" {
		t.Errorf("calls[1] = %q, want MoveFeed:123:old->new", fc.calls[1])
	}
}

func TestPatchNodeEmptyBodyErrors(t *testing.T) {
	setupMutateTest(t)

	_, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), bytes.NewReader(nil))
	if err == nil {
		t.Fatal("PatchNode with an empty body: expected an error, got nil")
	}
}

func TestDeleteNodeUnstarsStory(t *testing.T) {
	_, fc := setupMutateTest(t)

	err := Plugin{}.DeleteNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash))
	if err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0] != "UnstarStory:"+sampleHash {
		t.Errorf("calls = %v, want [UnstarStory:%s]", fc.calls, sampleHash)
	}
}

func TestDeleteNodeUnsubscribesFeed(t *testing.T) {
	fi, fc := setupMutateTest(t)
	setFeedFolder(fi, "123", "news")

	err := Plugin{}.DeleteNode(context.Background(), mustURL(t, "newsblur://feed/123"))
	if err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0] != "Unsubscribe:123:news" {
		t.Errorf("calls = %v, want [Unsubscribe:123:news]", fc.calls)
	}
}

func TestDeleteNodeErrorsIfAbsent(t *testing.T) {
	setupMutateTest(t)

	err := Plugin{}.DeleteNode(context.Background(), mustURL(t, "newsblur://story/does-not-exist"))
	if err == nil {
		t.Fatal("DeleteNode on a missing story: expected an error, got nil")
	}
}

func TestMutatorsErrorWhenNotInitialized(t *testing.T) {
	index = nil
	client = nil

	if err := (Plugin{}).CreateNode(context.Background(), mustURL(t, "newsblur://story/x"), bytes.NewReader(nil), typeStory); err == nil {
		t.Error("CreateNode with no client/index: expected an error, got nil")
	}
	if _, err := (Plugin{}).PatchNode(context.Background(), mustURL(t, "newsblur://story/x"), bytes.NewReader([]byte(`{}`))); err == nil {
		t.Error("PatchNode with no client/index: expected an error, got nil")
	}
	if err := (Plugin{}).DeleteNode(context.Background(), mustURL(t, "newsblur://story/x")); err == nil {
		t.Error("DeleteNode with no client/index: expected an error, got nil")
	}
}

// A star's created story should become visible to a subsequent
// index.StoryMetadata read once the fake index is updated to reflect it
// -- this exercises the create-then-read shape a real caller relies on,
// even though wiring the write into the fake's own state is the test's
// job here (a real Client's write lands via the live API, observed only
// after the next fetch, per this file's own staleness-lag comment).
func TestCreateNodeStarredStoryVisibleAfterIndexUpdate(t *testing.T) {
	fi, _ := setupMutateTest(t)

	const newHash = "123:new"
	if err := (Plugin{}).CreateNode(context.Background(), mustURL(t, "newsblur://story/"+newHash), bytes.NewReader(nil), typeStory); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	fi.storyMetadata[newHash] = storyMetadataEntry{
		view: tools.StoryMetadataView{Hash: newHash, Title: "New Story", Starred: true},
	}
	view, _, ok := index.StoryMetadata(newHash)
	if !ok {
		t.Fatal("StoryMetadata: expected the newly-starred story to be found")
	}
	if !view.Starred {
		t.Error("StoryMetadata: expected Starred=true")
	}
}

func TestPatchNodeStoryPropagatesClientError(t *testing.T) {
	_, fc := setupMutateTest(t)
	fc.err = errors.New("newsblur: rate limited")

	body := bytes.NewReader([]byte(`{"read":true}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err == nil {
		t.Fatal("PatchNode: expected the client's error to propagate, got nil")
	}
	if applied != nil {
		t.Errorf("applied = %v, want nil on error", applied)
	}
}

func TestPatchNodeFeedPropagatesClientError(t *testing.T) {
	_, fc := setupMutateTest(t)
	fc.err = errors.New("newsblur: rate limited")

	body := bytes.NewReader([]byte(`{"title":"New Title"}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://feed/123"), body)
	if err == nil {
		t.Fatal("PatchNode: expected the client's error to propagate, got nil")
	}
	if applied != nil {
		t.Errorf("applied = %v, want nil on error", applied)
	}
}

func TestPatchNodeFolderPropagatesClientError(t *testing.T) {
	_, fc := setupMutateTest(t)
	fc.err = errors.New("newsblur: rate limited")

	body := bytes.NewReader([]byte(`{"name":"NewName"}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://folder/Blogs - OldName"), body)
	if err == nil {
		t.Fatal("PatchNode: expected the client's error to propagate, got nil")
	}
	if applied != nil {
		t.Errorf("applied = %v, want nil on error", applied)
	}
}

func TestSplitFolderPath(t *testing.T) {
	cases := []struct {
		in         string
		ownName    string
		parentPath string
	}{
		{"Blogs", "Blogs", ""},
		{"Blogs - Photoblogs", "Photoblogs", "Blogs"},
		{"A - B - C", "C", "A - B"},
	}
	for _, c := range cases {
		ownName, parentPath := splitFolderPath(c.in)
		if ownName != c.ownName || parentPath != c.parentPath {
			t.Errorf("splitFolderPath(%q) = (%q, %q), want (%q, %q)",
				c.in, ownName, parentPath, c.ownName, c.parentPath)
		}
	}
}

func TestCreateNodeCreatesTopLevelFolder(t *testing.T) {
	_, fc := setupMutateTest(t)

	err := Plugin{}.CreateNode(context.Background(), mustURL(t, "newsblur://folder/Blogs"), bytes.NewReader(nil), typeFolder)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0] != "CreateFolder:Blogs:" {
		t.Errorf("calls = %v, want [CreateFolder:Blogs:]", fc.calls)
	}
}

func TestCreateNodeCreatesNestedFolder(t *testing.T) {
	_, fc := setupMutateTest(t)

	err := Plugin{}.CreateNode(context.Background(), mustURL(t, "newsblur://folder/Blogs - Photoblogs"), bytes.NewReader(nil), typeFolder)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0] != "CreateFolder:Photoblogs:Blogs" {
		t.Errorf("calls = %v, want [CreateFolder:Photoblogs:Blogs]", fc.calls)
	}
}

func TestCreateNodeRejectsWrongTypeForFolder(t *testing.T) {
	setupMutateTest(t)

	err := Plugin{}.CreateNode(context.Background(), mustURL(t, "newsblur://folder/Blogs"), bytes.NewReader(nil), typeStory)
	if err == nil {
		t.Fatal("CreateNode(folder) with typeStory: expected an error, got nil")
	}
}

func TestPatchNodeFolderRenameOnly(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"name":"NewName"}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://folder/Blogs - OldName"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{"name"})
	if len(fc.calls) != 1 || fc.calls[0] != "RenameFolder:OldName->NewName:Blogs" {
		t.Errorf("calls = %v, want [RenameFolder:OldName->NewName:Blogs]", fc.calls)
	}
}

func TestPatchNodeFolderMoveOnly(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"to_folder":"NewParent"}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://folder/OldParent - Photoblogs"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{"to_folder"})
	if len(fc.calls) != 1 || fc.calls[0] != "MoveFolder:Photoblogs:OldParent->NewParent" {
		t.Errorf("calls = %v, want [MoveFolder:Photoblogs:OldParent->NewParent]", fc.calls)
	}
}

// A rename and a move firing from one PatchNode body must apply the
// rename first, then move using the RENAMED name -- not the original.
func TestPatchNodeFolderRenameAndMoveUsesRenamedNameForMove(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"name":"NewName","to_folder":"NewParent"}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://folder/OldParent - OldName"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{"name", "to_folder"})
	if len(fc.calls) != 2 {
		t.Fatalf("calls = %v, want 2 (rename + move)", fc.calls)
	}
	if fc.calls[0] != "RenameFolder:OldName->NewName:OldParent" {
		t.Errorf("calls[0] = %q, want RenameFolder:OldName->NewName:OldParent", fc.calls[0])
	}
	if fc.calls[1] != "MoveFolder:NewName:OldParent->NewParent" {
		t.Errorf("calls[1] = %q, want MoveFolder:NewName:OldParent->NewParent", fc.calls[1])
	}
}

func TestPatchNodeFolderToleratesUnrecognizedField(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"parent":"NewParent"}`)) // wrong key: to_folder is correct
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://folder/Blogs"), body)
	if err != nil {
		t.Fatalf("PatchNode(folder) with only an unrecognized field: expected no error (tolerated), got %v", err)
	}
	assertApplied(t, applied, []string{})
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
	}
}

func TestPatchNodeFolderNoOpWhenBothFieldsAbsent(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{}`))
	applied, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://folder/Blogs"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertApplied(t, applied, []string{})
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none (name/to_folder both absent)", fc.calls)
	}
}

func TestDeleteNodeDeletesFolder(t *testing.T) {
	_, fc := setupMutateTest(t)

	err := Plugin{}.DeleteNode(context.Background(), mustURL(t, "newsblur://folder/Blogs - Photoblogs"))
	if err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0] != "DeleteFolder:Photoblogs:Blogs" {
		t.Errorf("calls = %v, want [DeleteFolder:Photoblogs:Blogs]", fc.calls)
	}
}

func TestDeleteNodeDeletesTopLevelFolder(t *testing.T) {
	_, fc := setupMutateTest(t)

	err := Plugin{}.DeleteNode(context.Background(), mustURL(t, "newsblur://folder/Blogs"))
	if err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0] != "DeleteFolder:Blogs:" {
		t.Errorf("calls = %v, want [DeleteFolder:Blogs:]", fc.calls)
	}
}

// TestErrorClassification checks RFC 0013 §Errors' caller-fault/plugin-fault
// split (cutting-garden#185): the wire transport (cutting-garden's own
// server.Handle) reclassifies an error as CodeInvalidParams (-32602) only
// when deweyerrors.Is400BadRequest(err) is true, and otherwise defaults it
// to CodeInternalError (-32603) -- so a caller mistake (bad URI, bad JSON
// value, patching something that doesn't exist) must be tagged, while a
// backend/setup failure (a live API call failing, "not initialized") must
// NOT be, or the two wire codes collapse into the exact "uniform mapping"
// the RFC calls non-conformant. This isn't about whether an error occurs --
// every case below already has its own dedicated test for that -- it's
// about whether the RIGHT errors carry the 400 tag and the WRONG ones don't.
func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name           string
		action         func(t *testing.T) error
		wantBadRequest bool
	}{
		{
			name: "PatchNode nil node URI",
			action: func(t *testing.T) error {
				setupMutateTest(t)
				_, err := Plugin{}.PatchNode(context.Background(), nil, bytes.NewReader([]byte(`{}`)))
				return err
			},
			wantBadRequest: true,
		},
		{
			name: "PatchNode empty body",
			action: func(t *testing.T) error {
				setupMutateTest(t)
				_, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), bytes.NewReader(nil))
				return err
			},
			wantBadRequest: true,
		},
		{
			name: "PatchNode story not found",
			action: func(t *testing.T) error {
				setupMutateTest(t)
				_, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/does-not-exist"), bytes.NewReader([]byte(`{"read":true}`)))
				return err
			},
			wantBadRequest: true,
		},
		{
			name: "PatchNode story wrong-typed field",
			action: func(t *testing.T) error {
				setupMutateTest(t)
				_, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), bytes.NewReader([]byte(`{"user_tags":"not-an-array"}`)))
				return err
			},
			wantBadRequest: true,
		},
		{
			name: "PatchNode feed folder without in_folder",
			action: func(t *testing.T) error {
				setupMutateTest(t)
				_, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://feed/123"), bytes.NewReader([]byte(`{"folder":"new"}`)))
				return err
			},
			wantBadRequest: true,
		},
		{
			name: "CreateNode story already exists",
			action: func(t *testing.T) error {
				setupMutateTest(t)
				return Plugin{}.CreateNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), nil, typeStory)
			},
			wantBadRequest: true,
		},
		{
			name: "CreateNode unrecognized field",
			action: func(t *testing.T) error {
				setupMutateTest(t)
				return Plugin{}.CreateNode(context.Background(), mustURL(t, "newsblur://story/new-hash"), bytes.NewReader([]byte(`{"starred":true}`)), typeStory)
			},
			wantBadRequest: true,
		},
		{
			name: "DeleteNode story not found",
			action: func(t *testing.T) error {
				setupMutateTest(t)
				return Plugin{}.DeleteNode(context.Background(), mustURL(t, "newsblur://story/does-not-exist"))
			},
			wantBadRequest: true,
		},
		{
			name: "CreateChild missing url field",
			action: func(t *testing.T) error {
				setupMutateTest(t)
				_, err := Plugin{}.CreateChild(context.Background(), mustURL(t, "newsblur://feeds"), bytes.NewReader([]byte(`{"folder":"News"}`)), typeFeed)
				return err
			},
			wantBadRequest: true,
		},
		{
			name: "PutNode always unsupported",
			action: func(t *testing.T) error {
				setupMutateTest(t)
				return Plugin{}.PutNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), bytes.NewReader([]byte(`{}`)))
			},
			wantBadRequest: true,
		},
		{
			// pathSegments (url.go) drops empty path elements, so no real
			// newsblur:// URI can reach createFolder/patchFolder/
			// deleteFolder's own "path must not be empty" guard with segs
			// still routed there -- exercised directly against the
			// unexported function instead, same as their own package
			// affords the other patch*/delete* helpers no dispatcher-level
			// route to.
			name: "createFolder empty path",
			action: func(t *testing.T) error {
				setupMutateTest(t)
				return createFolder(context.Background(), "", typeFolder)
			},
			wantBadRequest: true,
		},
		{
			name: "patchFolder empty path",
			action: func(t *testing.T) error {
				setupMutateTest(t)
				_, err := patchFolder(context.Background(), "", []byte(`{"name":"New"}`))
				return err
			},
			wantBadRequest: true,
		},
		{
			name: "DeleteNode feed not found",
			action: func(t *testing.T) error {
				setupMutateTest(t)
				return Plugin{}.DeleteNode(context.Background(), mustURL(t, "newsblur://feed/does-not-exist"))
			},
			wantBadRequest: true,
		},
		{
			name: "deleteFolder empty path",
			action: func(t *testing.T) error {
				setupMutateTest(t)
				return deleteFolder(context.Background(), "")
			},
			wantBadRequest: true,
		},
		{
			name: "CreateNode folder backend call failure is a plugin fault",
			action: func(t *testing.T) error {
				_, fc := setupMutateTest(t)
				fc.err = errors.New("newsblur: rate limited")
				return Plugin{}.CreateNode(context.Background(), mustURL(t, "newsblur://folder/Blogs"), nil, typeFolder)
			},
			wantBadRequest: false,
		},
		{
			name: "PatchNode folder backend call failure is a plugin fault",
			action: func(t *testing.T) error {
				_, fc := setupMutateTest(t)
				fc.err = errors.New("newsblur: rate limited")
				_, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://folder/Blogs"), bytes.NewReader([]byte(`{"name":"New"}`)))
				return err
			},
			wantBadRequest: false,
		},
		{
			name: "DeleteNode feed backend call failure is a plugin fault",
			action: func(t *testing.T) error {
				_, fc := setupMutateTest(t)
				fc.err = errors.New("newsblur: rate limited")
				return Plugin{}.DeleteNode(context.Background(), mustURL(t, "newsblur://feed/123"))
			},
			wantBadRequest: false,
		},
		{
			name: "DeleteNode folder backend call failure is a plugin fault",
			action: func(t *testing.T) error {
				_, fc := setupMutateTest(t)
				fc.err = errors.New("newsblur: rate limited")
				return Plugin{}.DeleteNode(context.Background(), mustURL(t, "newsblur://folder/Blogs"))
			},
			wantBadRequest: false,
		},
		{
			name: "not initialized is a plugin fault, not a caller fault",
			action: func(t *testing.T) error {
				index = nil
				client = nil
				_, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/x"), bytes.NewReader([]byte(`{}`)))
				return err
			},
			wantBadRequest: false,
		},
		{
			name: "PatchNode backend call failure is a plugin fault",
			action: func(t *testing.T) error {
				_, fc := setupMutateTest(t)
				fc.err = errors.New("newsblur: rate limited")
				_, err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), bytes.NewReader([]byte(`{"read":true}`)))
				return err
			},
			wantBadRequest: false,
		},
		{
			name: "CreateChild backend Subscribe failure is a plugin fault",
			action: func(t *testing.T) error {
				_, fc := setupMutateTest(t)
				fc.err = errors.New("newsblur: rate limited")
				_, err := Plugin{}.CreateChild(context.Background(), mustURL(t, "newsblur://feeds"), bytes.NewReader([]byte(`{"url":"https://example.com/feed"}`)), typeFeed)
				return err
			},
			wantBadRequest: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.action(t)
			if err == nil {
				t.Fatal("action: expected an error, got nil")
			}
			if got := deweyerrors.Is400BadRequest(err); got != c.wantBadRequest {
				t.Errorf("Is400BadRequest(%v) = %v, want %v", err, got, c.wantBadRequest)
			}
		})
	}
}
