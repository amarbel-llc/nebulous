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

// FeedRef is a feed's identity plus the fields the newsblur-feed-v1
// facet dimensions (folder, active) are drawn from.
type FeedRef struct {
	ID     string
	Title  string
	Folder string
	Active bool
}

// StoryRef is a story's identity plus the fields the newsblur-story-v1
// facet dimensions (year, tags, feed, read) are drawn from.
type StoryRef struct {
	Hash     string
	Title    string
	Year     int
	FeedID   int
	UserTags []string
	Tags     []string
	Read     bool
}

// Feeds returns every subscribed feed.
func (r *ReadIndex) Feeds(ctx context.Context) ([]FeedRef, error) {
	if err := r.feeds.ensureBuilt(ctx); err != nil {
		return nil, err
	}
	out := make([]FeedRef, 0, len(r.feeds.summaries))
	for idStr, s := range r.feeds.summaries {
		out = append(out, FeedRef{ID: idStr, Title: s.Title, Folder: s.Folder, Active: s.Active})
	}
	return out, nil
}

// Stories returns every indexed (starred) story.
func (r *ReadIndex) Stories() ([]StoryRef, error) {
	if err := r.stories.ensureBuilt(); err != nil {
		return nil, err
	}
	out := make([]StoryRef, 0, len(r.stories.stories))
	for _, rec := range r.stories.stories {
		out = append(out, storyRefOf(rec))
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
			out = append(out, storyRefOf(rec))
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
			out = append(out, storyRefOf(rec))
		}
	}
	return out, nil
}

func storyRefOf(rec *storyRecord) StoryRef {
	return StoryRef{
		Hash:     rec.Hash,
		Title:    rec.Title,
		Year:     rec.Year,
		FeedID:   rec.FeedID,
		UserTags: rec.UserTags,
		Tags:     rec.Tags,
		Read:     rec.Read,
	}
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

// FeedMetadataView is a feed's subscription-state projection — the
// …/metadata leaf's structured view.
type FeedMetadataView struct {
	ID       string `json:"id"`
	Title    string `json:"feed_title"`
	Link     string `json:"feed_link"`
	Folder   string `json:"folder,omitempty"`
	Active   bool   `json:"active"`
	Disabled bool   `json:"disabled"`
	NT       int    `json:"nt"`
	NG       int    `json:"ng"`
	PS       int    `json:"ps"`
}

// FeedMetadata returns the structured + raw views of a feed's cached
// subscription record. ok is false when id is not indexed.
func (r *ReadIndex) FeedMetadata(ctx context.Context, id string) (view FeedMetadataView, raw json.RawMessage, ok bool) {
	if err := r.feeds.ensureBuilt(ctx); err != nil {
		return FeedMetadataView{}, nil, false
	}
	raw, ok = r.feeds.feeds[id]
	if !ok {
		return FeedMetadataView{}, nil, false
	}
	// The cached feed's own "id" is a JSON number; ID is filled in from
	// the caller's string id below rather than unmarshaled, to avoid a
	// string/number type mismatch.
	var body struct {
		Title    string `json:"feed_title"`
		Link     string `json:"feed_link"`
		Active   bool   `json:"active"`
		Disabled bool   `json:"disabled"`
		NT       int    `json:"nt"`
		NG       int    `json:"ng"`
		PS       int    `json:"ps"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return FeedMetadataView{}, nil, false
	}
	folder := ""
	if s, ok := r.feeds.summaries[id]; ok {
		folder = s.Folder
	}
	view = FeedMetadataView{
		ID: id, Title: body.Title, Link: body.Link, Folder: folder,
		Active: body.Active, Disabled: body.Disabled,
		NT: body.NT, NG: body.NG, PS: body.PS,
	}
	return view, raw, true
}

// StoryMetadataView is a story's bibliographic projection — everything
// but the body text, which lives on the content/original leaves.
type StoryMetadataView struct {
	Hash      string   `json:"hash"`
	Title     string   `json:"title"`
	Authors   string   `json:"authors"`
	Permalink string   `json:"permalink"`
	FeedID    int      `json:"feed_id"`
	Date      string   `json:"date"`
	Tags      []string `json:"tags"`
	UserTags  []string `json:"user_tags"`
	Starred   bool     `json:"starred"`
	Read      bool     `json:"read"`
}

// StoryMetadata returns the structured + raw views of a story's
// bibliographic fields. ok is false when the story is not in the local
// store.
func (r *ReadIndex) StoryMetadata(hash string) (view StoryMetadataView, raw []byte, ok bool) {
	storyRaw, ok := r.stories.rawStoryByHash(hash)
	if !ok {
		return StoryMetadataView{}, nil, false
	}
	var full struct {
		Hash       string   `json:"story_hash"`
		Title      string   `json:"story_title"`
		Authors    string   `json:"story_authors"`
		Permalink  string   `json:"story_permalink"`
		FeedID     int      `json:"story_feed_id"`
		Date       string   `json:"story_date"`
		Tags       []string `json:"story_tags"`
		UserTags   []string `json:"user_tags"`
		Starred    bool     `json:"starred"`
		ReadStatus int      `json:"read_status"`
	}
	if err := json.Unmarshal(storyRaw, &full); err != nil {
		return StoryMetadataView{}, nil, false
	}
	view = StoryMetadataView{
		Hash: full.Hash, Title: full.Title, Authors: full.Authors,
		Permalink: full.Permalink, FeedID: full.FeedID, Date: full.Date,
		Tags: full.Tags, UserTags: full.UserTags, Starred: full.Starred,
		Read: full.ReadStatus == 1,
	}
	return view, storyRaw, true
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
