package tools

import (
	"encoding/json"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/friedenberg/nebulous/internal/newsblur"
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

type storyStore struct {
	client    *newsblur.Client
	once      sync.Once
	stories   []*storyRecord
	words     map[string][]*storyRecord
	userTags  map[string]int
	storyTags map[string]int
	err       error
}

func newStoryStore(client *newsblur.Client) *storyStore {
	return &storyStore{client: client}
}

func (s *storyStore) ensureBuilt() error {
	s.once.Do(func() {
		s.words = make(map[string][]*storyRecord)
		s.userTags = make(map[string]int)
		s.storyTags = make(map[string]int)
		s.err = s.build()
	})
	return s.err
}

func (s *storyStore) build() error {
	total := 0

	for page := 1; ; page++ {
		raw, ok := s.client.CachedStarredStoryPage(page)
		if !ok {
			break
		}

		var resp struct {
			Stories []json.RawMessage `json:"stories"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return err
		}

		if len(resp.Stories) == 0 {
			break
		}

		for _, storyRaw := range resp.Stories {
			rec, err := parseStoryRecord(storyRaw, s.client)
			if err != nil {
				continue
			}
			rec.Starred = true
			s.stories = append(s.stories, rec)

			for word := range rec.Words {
				s.words[word] = append(s.words[word], rec)
			}
			for _, t := range rec.UserTags {
				if t != "" {
					s.userTags[t]++
				}
			}
			for _, t := range rec.Tags {
				if t != "" {
					s.storyTags[t]++
				}
			}
		}

		total += len(resp.Stories)
	}

	sort.Slice(s.stories, func(i, j int) bool {
		return s.stories[i].Date.After(s.stories[j].Date)
	})

	log.Printf("story store: indexed %d stories, %d words", total, len(s.words))
	return nil
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
	if err := s.ensureBuilt(); err != nil {
		return nil, false
	}
	for _, rec := range s.stories {
		if rec.Hash == hash {
			return rec, true
		}
	}
	return nil, false
}

// rawStoryByHash walks cached pages to find the raw JSON for a story hash.
// Used by readStoryContent which needs the original story_content HTML.
func (s *storyStore) rawStoryByHash(hash string) (json.RawMessage, bool) {
	if err := s.ensureBuilt(); err != nil {
		return nil, false
	}
	for page := 1; ; page++ {
		raw, ok := s.client.CachedStarredStoryPage(page)
		if !ok {
			break
		}
		var resp struct {
			Stories []json.RawMessage `json:"stories"`
		}
		if json.Unmarshal(raw, &resp) != nil {
			continue
		}
		for _, storyRaw := range resp.Stories {
			var story struct {
				Hash string `json:"story_hash"`
			}
			if json.Unmarshal(storyRaw, &story) == nil && story.Hash == hash {
				return storyRaw, true
			}
		}
	}
	return nil, false
}
