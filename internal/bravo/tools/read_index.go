package tools

import (
	"context"
	"encoding/json"

	"github.com/friedenberg/nebulous/internal/alfa/newsblur"
)

// ReadIndex is the read-only façade over the local NewsBlur index (feeds
// + starred stories) that the cutting-garden newsblur plugin consumes.
// It wraps the same lazily-built feedIndex/storyStore the MCP server
// uses, so traversal reads hit only the local cache — no token, no API.
type ReadIndex struct {
	feeds   *feedIndex
	stories *storyStore
	client  *newsblur.Client
}

// NewReadIndex builds a ReadIndex over client's local cache. The
// underlying indices build lazily on first read (sync.Once).
func NewReadIndex(client *newsblur.Client) *ReadIndex {
	return &ReadIndex{
		feeds:   newFeedIndex(client),
		stories: newStoryStore(client),
		client:  client,
	}
}

// FeedRef is a feed's identity for a traversal listing.
type FeedRef struct {
	ID    string
	Title string
}

// StoryRef is a story's identity for a traversal listing.
type StoryRef struct {
	Hash  string
	Title string
}

// Feeds returns every subscribed feed (id + title).
func (r *ReadIndex) Feeds(ctx context.Context) ([]FeedRef, error) {
	if err := r.feeds.ensureBuilt(ctx); err != nil {
		return nil, err
	}
	out := make([]FeedRef, 0, len(r.feeds.feeds))
	for idStr, raw := range r.feeds.feeds {
		var f struct {
			Title string `json:"feed_title"`
		}
		_ = json.Unmarshal(raw, &f)
		out = append(out, FeedRef{ID: idStr, Title: f.Title})
	}
	return out, nil
}

// Stories returns every indexed (starred) story (hash + title).
func (r *ReadIndex) Stories() ([]StoryRef, error) {
	if err := r.stories.ensureBuilt(); err != nil {
		return nil, err
	}
	out := make([]StoryRef, 0, len(r.stories.stories))
	for _, rec := range r.stories.stories {
		out = append(out, StoryRef{Hash: rec.Hash, Title: rec.Title})
	}
	return out, nil
}

// FeedStories returns the indexed stories belonging to feedID.
func (r *ReadIndex) FeedStories(feedID int) ([]StoryRef, error) {
	if err := r.stories.ensureBuilt(); err != nil {
		return nil, err
	}
	var out []StoryRef
	for _, rec := range r.stories.stories {
		if rec.FeedID == feedID {
			out = append(out, StoryRef{Hash: rec.Hash, Title: rec.Title})
		}
	}
	return out, nil
}

// StoriesByTag returns the indexed stories carrying tag in either their
// user tags or their story (feed-assigned) tags.
func (r *ReadIndex) StoriesByTag(tag string) ([]StoryRef, error) {
	if err := r.stories.ensureBuilt(); err != nil {
		return nil, err
	}
	var out []StoryRef
	for _, rec := range r.stories.stories {
		if rec.hasTag(tag) {
			out = append(out, StoryRef{Hash: rec.Hash, Title: rec.Title})
		}
	}
	return out, nil
}

// Tags returns the union of user tags and story tags across the corpus.
func (r *ReadIndex) Tags() ([]string, error) {
	if err := r.stories.ensureBuilt(); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(r.stories.userTags)+len(r.stories.storyTags))
	for t := range r.stories.userTags {
		seen[t] = struct{}{}
	}
	for t := range r.stories.storyTags {
		seen[t] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	return out, nil
}

// StoryContentView is the structured projection of a story's cached
// summary content (HTML stripped, truncated) — the leaf's parsed view.
type StoryContentView struct {
	Hash       string `json:"hash"`
	Title      string `json:"title"`
	Permalink  string `json:"permalink"`
	Content    string `json:"content"`
	HasContent bool   `json:"has_content"`
	Truncated  bool   `json:"truncated"`
}

// StoryContent returns the structured + raw views of a story's cached
// story_content. ok is false when the story is not in the local store.
func (r *ReadIndex) StoryContent(hash string) (view StoryContentView, raw []byte, ok bool) {
	storyRaw, ok := r.stories.rawStoryByHash(hash)
	if !ok {
		return StoryContentView{}, nil, false
	}
	var full struct {
		Hash      string `json:"story_hash"`
		Title     string `json:"story_title"`
		Content   string `json:"story_content"`
		Permalink string `json:"story_permalink"`
	}
	if err := json.Unmarshal(storyRaw, &full); err != nil {
		return StoryContentView{}, nil, false
	}
	text := stripHTMLTags(full.Content)
	truncated := false
	if len(text) > 4000 {
		text = text[:4000]
		truncated = true
	}
	view = StoryContentView{
		Hash:       full.Hash,
		Title:      full.Title,
		Permalink:  full.Permalink,
		Content:    text,
		HasContent: len(text) > 200,
		Truncated:  truncated,
	}
	return view, []byte(full.Content), true
}

// StoryOriginal returns the cached original article text (HTML) for
// hash. ok is false when the original is not cached (run `nebulous
// fetch`).
func (r *ReadIndex) StoryOriginal(hash string) (raw []byte, ok bool) {
	otRaw, ok := r.client.CachedOriginalText(hash)
	if !ok {
		return nil, false
	}
	var ot struct {
		OriginalText string `json:"original_text"`
	}
	if err := json.Unmarshal(otRaw, &ot); err != nil || ot.OriginalText == "" {
		return nil, false
	}
	return []byte(ot.OriginalText), true
}

// hasTag reports whether the record carries tag in its user tags or its
// story (feed-assigned) tags.
func (rec *storyRecord) hasTag(tag string) bool {
	for _, t := range rec.UserTags {
		if t == tag {
			return true
		}
	}
	for _, t := range rec.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
