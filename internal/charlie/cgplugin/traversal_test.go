package cgplugin

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"code.linenisgreat.com/nebulous/internal/bravo/tools"
)

// fakeIndex is a canned Index for exercising the plugin's traversal and
// leaf routing without a live cache.
type fakeIndex struct {
	feeds          []tools.FeedRef
	stories        []tools.StoryRef
	feedStories    map[int][]tools.StoryRef
	tagStories     map[string][]tools.StoryRef
	tags           []string
	feedMetadata   map[string]feedMetadataEntry
	content        map[string]contentEntry
	original       map[string][]byte
	storyMetadata  map[string]storyMetadataEntry
	manifestPath   string
	captureRecords map[string]tools.CaptureRecordView // key: hash+"/"+format
	captureFormats []string
	seenFeedTitles map[string]string
}

type contentEntry struct {
	view tools.StoryContentView
	raw  []byte
}

type feedMetadataEntry struct {
	view tools.FeedMetadataView
	raw  json.RawMessage
}

type storyMetadataEntry struct {
	view tools.StoryMetadataView
	raw  []byte
}

func (f *fakeIndex) Feeds(context.Context) ([]tools.FeedRef, error) { return f.feeds, nil }
func (f *fakeIndex) Stories() ([]tools.StoryRef, error)             { return f.stories, nil }
func (f *fakeIndex) FeedStories(id int) ([]tools.StoryRef, error)   { return f.feedStories[id], nil }

func (f *fakeIndex) StoriesByTag(t string) ([]tools.StoryRef, error) {
	return f.tagStories[t], nil
}
func (f *fakeIndex) Tags() ([]string, error) { return f.tags, nil }

func (f *fakeIndex) FeedMetadata(_ context.Context, id string) (tools.FeedMetadataView, json.RawMessage, bool) {
	m, ok := f.feedMetadata[id]
	return m.view, m.raw, ok
}

func (f *fakeIndex) StoryContent(h string) (tools.StoryContentView, []byte, bool) {
	c, ok := f.content[h]
	return c.view, c.raw, ok
}

func (f *fakeIndex) StoryOriginal(h string) ([]byte, bool) {
	o, ok := f.original[h]
	return o, ok
}

func (f *fakeIndex) StoryMetadata(h string) (tools.StoryMetadataView, []byte, bool) {
	m, ok := f.storyMetadata[h]
	return m.view, m.raw, ok
}

func (f *fakeIndex) ManifestPath() string { return f.manifestPath }

func (f *fakeIndex) SeenFeedTitles() map[string]string { return f.seenFeedTitles }

func (f *fakeIndex) CaptureRecord(hash, format string) (tools.CaptureRecordView, bool) {
	rec, ok := f.captureRecords[hash+"/"+format]
	return rec, ok
}

func (f *fakeIndex) CaptureFormats() []string {
	if f.captureFormats != nil {
		return f.captureFormats
	}
	return tools.DefaultCaptureFormats
}

const sampleHash = "123:abc" // feed_id:guid — exercises the colon in a path

func newFakeIndex() *fakeIndex {
	return &fakeIndex{
		feeds:       []tools.FeedRef{{ID: "123", Title: "Example Feed"}},
		stories:     []tools.StoryRef{{Hash: sampleHash, Title: "A Story"}},
		feedStories: map[int][]tools.StoryRef{123: {{Hash: sampleHash, Title: "A Story"}}},
		tagStories:  map[string][]tools.StoryRef{"news": {{Hash: sampleHash, Title: "A Story"}}},
		tags:        []string{"news"},
		feedMetadata: map[string]feedMetadataEntry{
			"123": {
				view: tools.FeedMetadataView{ID: "123", Title: "Example Feed"},
				raw:  json.RawMessage(`{"id":123,"feed_title":"Example Feed"}`),
			},
		},
		content: map[string]contentEntry{
			sampleHash: {
				view: tools.StoryContentView{Hash: sampleHash, Title: "A Story", Content: "hello", HasContent: false},
				raw:  []byte("<p>hello</p>"),
			},
		},
		original: map[string][]byte{sampleHash: []byte("<html>full</html>")},
		storyMetadata: map[string]storyMetadataEntry{
			sampleHash: {
				view: tools.StoryMetadataView{Hash: sampleHash, Title: "A Story"},
				raw:  []byte(`{"story_hash":"123:abc","story_title":"A Story"}`),
			},
		},
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestRoots(t *testing.T) {
	got, err := Plugin{}.Roots(context.Background())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	want := []string{"newsblur://feeds", "newsblur://stories", "newsblur://tags"}
	if len(got) != len(want) {
		t.Fatalf("Roots returned %d urls, want %d", len(got), len(want))
	}
	for i, u := range got {
		if u.String() != want[i] {
			t.Errorf("root[%d] = %q, want %q", i, u.String(), want[i])
		}
	}
}

func TestTypes(t *testing.T) {
	types := Plugin{}.Types()
	byTag := map[string]bool{}
	for _, ty := range types {
		byTag[ty.Tag] = ty.Container
	}
	if c, ok := byTag[typeStory]; !ok || !c {
		t.Errorf("story type missing or not a container: %v", byTag)
	}
	if c, ok := byTag[typeStoryContent]; !ok || c {
		t.Errorf("story-content type missing or wrongly a container: %v", byTag)
	}
}

func TestListRoots(t *testing.T) {
	index = newFakeIndex()
	t.Cleanup(func() { index = nil })

	cases := []struct {
		name     string
		uri      string
		wantURIs []string
		wantType string
	}{
		{"feeds", "newsblur://feeds", []string{"newsblur://feed/123"}, typeFeed},
		{"stories", "newsblur://stories", []string{"newsblur://story/123:abc"}, typeStory},
		{"tags", "newsblur://tags", []string{"newsblur://tag/news"}, typeTag},
		{
			"feed stories", "newsblur://feed/123",
			[]string{"newsblur://story/123:abc", "newsblur://feed/123/metadata"},
			"",
		},
		{"tag stories", "newsblur://tag/news", []string{"newsblur://story/123:abc"}, typeStory},
		{
			"story leaves", "newsblur://story/123:abc",
			[]string{
				"newsblur://story/123:abc/content",
				"newsblur://story/123:abc/original",
				"newsblur://story/123:abc/metadata",
			},
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodes, err := Plugin{}.ListRoots(context.Background(), mustURL(t, tc.uri))
			if err != nil {
				t.Fatalf("ListRoots(%s): %v", tc.uri, err)
			}
			if len(nodes) != len(tc.wantURIs) {
				t.Fatalf("ListRoots(%s) returned %d nodes, want %d", tc.uri, len(nodes), len(tc.wantURIs))
			}
			for i, n := range nodes {
				if n.URI.String() != tc.wantURIs[i] {
					t.Errorf("node[%d].URI = %q, want %q", i, n.URI.String(), tc.wantURIs[i])
				}
				if tc.wantType != "" && n.Type != tc.wantType {
					t.Errorf("node[%d].Type = %q, want %q", i, n.Type, tc.wantType)
				}
			}
		})
	}
}

func TestListRootsLeafHasNoChildren(t *testing.T) {
	index = newFakeIndex()
	t.Cleanup(func() { index = nil })

	nodes, err := Plugin{}.ListRoots(context.Background(), mustURL(t, "newsblur://story/123:abc/content"))
	if err != nil {
		t.Fatalf("ListRoots(content leaf): %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("content leaf reported %d children, want 0", len(nodes))
	}
}

// A story with a completed capture record lists a capture/{format} leaf
// alongside content/original/metadata; a story with none (the common
// newFakeIndex() case, exercised by "story leaves" in TestListRoots)
// lists only the three static leaves — no phantom capture leaves for
// uncaptured formats.
func TestListRootsStoryCaptureLeaf(t *testing.T) {
	fi := newFakeIndex()
	format := tools.DefaultCaptureFormats[0]
	fi.captureRecords = map[string]tools.CaptureRecordView{
		sampleHash + "/" + format: {ReceiptID: "blake2b256-abc"},
	}
	index = fi
	t.Cleanup(func() { index = nil })

	nodes, err := Plugin{}.ListRoots(context.Background(), mustURL(t, "newsblur://story/123:abc"))
	if err != nil {
		t.Fatalf("ListRoots(story): %v", err)
	}
	wantURI := "newsblur://story/123:abc/capture/" + format
	var found bool
	for _, n := range nodes {
		if n.URI.String() == wantURI {
			found = true
			if n.Type != typeStoryCapture {
				t.Errorf("capture leaf Type = %q, want %q", n.Type, typeStoryCapture)
			}
		}
	}
	if !found {
		t.Errorf("ListRoots(story) = %v, want a %q node", nodes, wantURI)
	}
	if len(nodes) != 4 {
		t.Errorf("ListRoots(story) returned %d nodes, want 4 (content/original/metadata/capture)", len(nodes))
	}
}

// A capture record for a format OUTSIDE tools.DefaultCaptureFormats
// (e.g. an operator ran `nebulous capture --formats pdf`) must still
// surface as a leaf: storyLeafNodes consults index.CaptureFormats(),
// not the hardcoded default list.
func TestListRootsStoryCaptureLeafNonDefaultFormat(t *testing.T) {
	fi := newFakeIndex()
	const format = "pdf"
	fi.captureFormats = []string{format}
	fi.captureRecords = map[string]tools.CaptureRecordView{
		sampleHash + "/" + format: {ReceiptID: "blake2b256-pdf"},
	}
	index = fi
	t.Cleanup(func() { index = nil })

	nodes, err := Plugin{}.ListRoots(context.Background(), mustURL(t, "newsblur://story/123:abc"))
	if err != nil {
		t.Fatalf("ListRoots(story): %v", err)
	}
	wantURI := "newsblur://story/123:abc/capture/" + format
	for _, n := range nodes {
		if n.URI.String() == wantURI {
			return
		}
	}
	t.Errorf("ListRoots(story) = %v, want a %q node", nodes, wantURI)
}
