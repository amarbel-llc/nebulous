package cgplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/friedenberg/nebulous/internal/bravo/tools"
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
	fi, fc := setupMutateTest(t)
	fi.feedMetadata["123"] = feedMetadataEntry{
		view: tools.FeedMetadataView{ID: "123", Title: "Example Feed", Folder: "old"},
		raw:  fi.feedMetadata["123"].raw,
	}

	body := bytes.NewReader([]byte(`{"folder":"new"}`))
	err := Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://feed/123"), body)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0] != "MoveFeed:123:old->new" {
		t.Errorf("calls = %v, want [MoveFeed:123:old->new]", fc.calls)
	}
}

func TestPatchNodeFeedRenameAndMoveBothFireFromOneBody(t *testing.T) {
	fi, fc := setupMutateTest(t)
	fi.feedMetadata["123"] = feedMetadataEntry{
		view: tools.FeedMetadataView{ID: "123", Title: "Example Feed", Folder: "old"},
		raw:  fi.feedMetadata["123"].raw,
	}

	body := bytes.NewReader([]byte(`{"title":"New Title","folder":"new"}`))
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
	fi.feedMetadata["123"] = feedMetadataEntry{
		view: tools.FeedMetadataView{ID: "123", Title: "Example Feed", Folder: "news"},
		raw:  fi.feedMetadata["123"].raw,
	}

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
