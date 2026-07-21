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
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0] != "MarkStoriesRead:"+sampleHash {
		t.Errorf("calls = %v, want [MarkStoriesRead:%s]", fc.calls, sampleHash)
	}
}

func TestPatchNodeStoryMarksUnread(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"read":false}`))
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
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
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
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
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
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
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
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
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
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
// context rather than a generic "invalid body" message.
func TestPatchNodeStoryUserTagsWrongTypeErrors(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"user_tags":"not-an-array"}`))
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err == nil {
		t.Fatal("PatchNode with user_tags as a string: expected an error, got nil")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
	}
}

// A patch body with `read` absent must be a true no-op: no client call at
// all, not a degenerate "unread unchanged" call.
func TestPatchNodeStoryNoOpWhenReadAbsent(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{}`))
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none (read absent from body)", fc.calls)
	}
}

func TestPatchNodeStoryErrorsIfAbsent(t *testing.T) {
	setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"read":true}`))
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/does-not-exist"), body)
	if err == nil {
		t.Fatal("PatchNode on a missing story: expected an error, got nil")
	}
}

// cutting-garden#180: patch_node on a story with a field storyPatchBody
// doesn't declare used to report success while calling the client zero
// times -- a plain (non-strict) struct decode silently drops fields it
// doesn't declare, so the mutation-gating check saw nothing to do and
// returned nil having done nothing. This is the shape of the original
// reported reproduction (which used "user_tags" -- since promoted to a
// real patchable field by nebulous#50, so a still-unrecognized name is
// used here instead): the fix must reject it, not just decode leniently
// and skip it.
func TestPatchNodeStoryRejectsUnrecognizedField(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"starred":true}`))
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err == nil {
		t.Fatal("PatchNode with only unrecognized fields: expected an error, got nil (this is cutting-garden#180 -- reporting success while doing nothing)")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none (should have errored before calling the client)", fc.calls)
	}
}

// A body mixing a recognized field with an unrecognized one must reject
// the whole body rather than silently applying the recognized half --
// partial, unannounced fulfillment is its own flavor of false success.
func TestPatchNodeStoryRejectsUnrecognizedFieldEvenAlongsideRecognizedOne(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"read":true,"starred":true}`))
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err == nil {
		t.Fatal("PatchNode with a mix of recognized and unrecognized fields: expected an error, got nil")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none (should have errored before calling the client, not partially applied \"read\")", fc.calls)
	}
}

func TestPatchNodeFeedRejectsUnrecognizedField(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"active":false}`))
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://feed/123"), body)
	if err == nil {
		t.Fatal("PatchNode(feed) with an unrecognized field: expected an error, got nil")
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
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://feed/123"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0] != "RenameFeed:123:New Title" {
		t.Errorf("calls = %v, want [RenameFeed:123:New Title]", fc.calls)
	}
}

func TestPatchNodeFeedMoveOnly(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"folder":"new","in_folder":"old"}`))
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://feed/123"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
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
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://feed/123"), body)
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
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://feed/123"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
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

	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), bytes.NewReader(nil))
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
	if err := (Plugin{}).PatchNode(context.Background(), mustURL(t, "newsblur://story/x"), bytes.NewReader([]byte(`{}`))); err == nil {
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
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), body)
	if err == nil {
		t.Fatal("PatchNode: expected the client's error to propagate, got nil")
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
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://folder/Blogs - OldName"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0] != "RenameFolder:OldName->NewName:Blogs" {
		t.Errorf("calls = %v, want [RenameFolder:OldName->NewName:Blogs]", fc.calls)
	}
}

func TestPatchNodeFolderMoveOnly(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"to_folder":"NewParent"}`))
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://folder/OldParent - Photoblogs"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0] != "MoveFolder:Photoblogs:OldParent->NewParent" {
		t.Errorf("calls = %v, want [MoveFolder:Photoblogs:OldParent->NewParent]", fc.calls)
	}
}

// A rename and a move firing from one PatchNode body must apply the
// rename first, then move using the RENAMED name -- not the original.
func TestPatchNodeFolderRenameAndMoveUsesRenamedNameForMove(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"name":"NewName","to_folder":"NewParent"}`))
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://folder/OldParent - OldName"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
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

func TestPatchNodeFolderRejectsUnrecognizedField(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{"parent":"NewParent"}`)) // wrong key: to_folder is correct
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://folder/Blogs"), body)
	if err == nil {
		t.Fatal("PatchNode(folder) with an unrecognized field: expected an error, got nil")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
	}
}

func TestPatchNodeFolderNoOpWhenBothFieldsAbsent(t *testing.T) {
	_, fc := setupMutateTest(t)

	body := bytes.NewReader([]byte(`{}`))
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://folder/Blogs"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
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
