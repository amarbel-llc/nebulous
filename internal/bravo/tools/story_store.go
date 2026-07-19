package tools

import (
	"encoding/json"
	"log"
	"sort"
	"time"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
)

type storyRecord struct {
	Hash          string
	Title         string
	Authors       string
	FeedID        int
	Date          time.Time
	Year          int
	Month         int
	Permalink     string
	Tags          []string
	UserTags      []string
	Starred       bool
	Read          bool
	Words         map[string]bool
	HasContent    bool
	ContentTokens int
}

// storyStoreSnapshot is the immutable result of one build pass. current()
// swaps a fresh one in under storyStore's lock, so a reader that already
// holds a snapshot never observes a half-rebuilt index.
type storyStoreSnapshot struct {
	stories   []*storyRecord
	words     map[string][]*storyRecord
	userTags  map[string]int
	storyTags map[string]int
}

type storyStore struct {
	client *newsblur.Client
	cache  staleCache[storyStoreSnapshot]
}

func newStoryStore(client *newsblur.Client) *storyStore {
	return &storyStore{
		client: client,
		cache:  newStaleCache[storyStoreSnapshot](client.ManifestPath()),
	}
}

// current returns the up-to-date snapshot, rebuilding when the manifest
// changed since the last check — see staleCache's doc comment for why
// this replaced a plain sync.Once build.
func (s *storyStore) current() (*storyStoreSnapshot, error) {
	return s.cache.current(s.build)
}

// ensureBuilt preserves the old call-site contract (callers only check
// the error); the snapshot itself is fetched via current(). Kept
// distinct from a bare current() call because facets()/query() swallow
// build errors internally (empty-result degrade) — callers that need the
// error surfaced (resources.go, story_query_tool.go) call this first.
func (s *storyStore) ensureBuilt() error {
	_, err := s.current()
	return err
}

func (s *storyStore) build() (*storyStoreSnapshot, error) {
	// s.cache already decided the manifest file changed; force the
	// client's own manifest past its independent debounce window too, so
	// the Cached* reads below can't still be gated by it and bake
	// pre-write data into this rebuild (see staleCache's doc comment).
	s.client.ForceManifestRefresh()

	snap := &storyStoreSnapshot{
		words:     make(map[string][]*storyRecord),
		userTags:  make(map[string]int),
		storyTags: make(map[string]int),
	}

	raw, ok := s.client.CachedStarredStoryHashes()
	if !ok {
		log.Printf("story store: no cached hash manifest, run 'nebulous fetch' first")
		return snap, nil
	}

	hashes, err := newsblur.ParseStarredHashes(raw)
	if err != nil {
		return nil, err
	}

	for _, hash := range hashes {
		storyRaw, ok := s.client.CachedStarredStory(hash)
		if !ok {
			continue
		}

		rec, err := parseStoryRecord(storyRaw, s.client)
		if err != nil {
			continue
		}
		rec.Starred = true
		snap.stories = append(snap.stories, rec)

		for word := range rec.Words {
			snap.words[word] = append(snap.words[word], rec)
		}
		for _, t := range rec.UserTags {
			if t != "" {
				snap.userTags[t]++
			}
		}
		for _, t := range rec.Tags {
			if t != "" {
				snap.storyTags[t]++
			}
		}
	}

	sort.Slice(snap.stories, func(i, j int) bool {
		return snap.stories[i].Date.After(snap.stories[j].Date)
	})

	log.Printf("story store: indexed %d stories, %d words", len(snap.stories), len(snap.words))
	return snap, nil
}

var storyDateFormats = []string{
	"2006-01-02 15:04:05",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05+00:00",
}

func parseStoryRecord(raw json.RawMessage, client *newsblur.Client) (*storyRecord, error) {
	var story struct {
		Hash       string   `json:"story_hash"`
		Title      string   `json:"story_title"`
		Authors    string   `json:"story_authors"`
		Content    string   `json:"story_content"`
		FeedID     int      `json:"story_feed_id"`
		Date       string   `json:"story_date"`
		Permalink  string   `json:"story_permalink"`
		Tags       []string `json:"story_tags"`
		UserTags   []string `json:"user_tags"`
		Starred    bool     `json:"starred"`
		ReadStatus int      `json:"read_status"`
	}
	if err := json.Unmarshal(raw, &story); err != nil {
		return nil, err
	}

	var parsedDate time.Time
	for _, fmt := range storyDateFormats {
		if t, err := time.Parse(fmt, story.Date); err == nil {
			parsedDate = t
			break
		}
	}

	stripped := stripHTMLTags(story.Content)
	hasContent := len(stripped) > 200
	contentTokens := len(stripped) / 4

	words := make(map[string]bool)
	addWords := func(text string) {
		for _, w := range extractWords(text) {
			words[w] = true
		}
	}

	addWords(story.Title)
	if story.Content != "" {
		addWords(stripped)
	}

	if client != nil {
		if otRaw, ok := client.CachedOriginalText(story.Hash); ok {
			var ot struct {
				OriginalText string `json:"original_text"`
			}
			if json.Unmarshal(otRaw, &ot) == nil && ot.OriginalText != "" {
				addWords(stripHTMLTags(ot.OriginalText))
			}
		}
	}

	if story.Tags == nil {
		story.Tags = []string{}
	}
	if story.UserTags == nil {
		story.UserTags = []string{}
	}

	return &storyRecord{
		Hash:          story.Hash,
		Title:         story.Title,
		Authors:       story.Authors,
		FeedID:        story.FeedID,
		Date:          parsedDate,
		Year:          parsedDate.Year(),
		Month:         int(parsedDate.Month()),
		Permalink:     story.Permalink,
		Tags:          story.Tags,
		UserTags:      story.UserTags,
		Starred:       story.Starred,
		Read:          story.ReadStatus == 1,
		Words:         words,
		HasContent:    hasContent,
		ContentTokens: contentTokens,
	}, nil
}

func (s *storyStore) storyByHash(hash string) (*storyRecord, bool) {
	snap, err := s.current()
	if err != nil || snap == nil {
		return nil, false
	}
	for _, rec := range snap.stories {
		if rec.Hash == hash {
			return rec, true
		}
	}
	return nil, false
}

func (s *storyStore) rawStoryByHash(hash string) (json.RawMessage, bool) {
	return s.client.CachedStarredStory(hash)
}
