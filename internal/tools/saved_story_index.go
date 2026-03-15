package tools

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/friedenberg/nebulous/internal/newsblur"
)

type storySummary struct {
	Hash      string `json:"hash"`
	Title     string `json:"title"`
	FeedID    int    `json:"feed_id"`
	Date      string `json:"date"`
	Permalink string `json:"permalink"`
}

type savedStoryIndexResult struct {
	words   map[string][]storySummary
	warning string
}

type savedStoryIndex struct {
	client  *newsblur.Client
	once    sync.Once
	words   map[string][]storySummary
	stories map[string]json.RawMessage
	err     error
}

func newSavedStoryIndex(client *newsblur.Client) *savedStoryIndex {
	return &savedStoryIndex{
		client: client,
	}
}

func (idx *savedStoryIndex) ensureBuilt() savedStoryIndexResult {
	idx.once.Do(func() {
		idx.words = make(map[string][]storySummary)
		idx.err = idx.build()
	})

	if idx.err != nil {
		return savedStoryIndexResult{warning: idx.err.Error()}
	}
	return savedStoryIndexResult{words: idx.words}
}

func (idx *savedStoryIndex) storyByHash(hash string) (json.RawMessage, bool) {
	idx.ensureBuilt()
	raw, ok := idx.stories[hash]
	return raw, ok
}

func (idx *savedStoryIndex) build() error {
	totalStories := 0
	idx.stories = make(map[string]json.RawMessage)

	for page := 1; ; page++ {
		raw, ok := idx.client.CachedStarredStoryPage(page)
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
			idx.indexStory(storyRaw)
			idx.storeStory(storyRaw)
		}

		totalStories += len(resp.Stories)
	}

	log.Printf("saved story index: indexed %d stories, %d words", totalStories, len(idx.words))
	return nil
}

func (idx *savedStoryIndex) indexStory(storyRaw json.RawMessage) {
	var story struct {
		Hash      string `json:"story_hash"`
		Title     string `json:"story_title"`
		Content   string `json:"story_content"`
		FeedID    int    `json:"story_feed_id"`
		Date      string `json:"story_date"`
		Permalink string `json:"story_permalink"`
	}
	if err := json.Unmarshal(storyRaw, &story); err != nil {
		return
	}

	summary := storySummary{
		Hash:      story.Hash,
		Title:     story.Title,
		FeedID:    story.FeedID,
		Date:      story.Date,
		Permalink: story.Permalink,
	}

	seen := make(map[string]bool)
	addWords := func(text string) {
		for _, word := range extractWords(text) {
			if !seen[word] {
				seen[word] = true
				idx.words[word] = append(idx.words[word], summary)
			}
		}
	}

	addWords(story.Title)

	if story.Content != "" {
		addWords(stripHTMLTags(story.Content))
	}

	// Opportunistically index cached original_text
	if idx.client != nil {
		if raw, ok := idx.client.CachedOriginalText(story.Hash); ok {
			var ot struct {
				OriginalText string `json:"original_text"`
			}
			if json.Unmarshal(raw, &ot) == nil && ot.OriginalText != "" {
				addWords(stripHTMLTags(ot.OriginalText))
			}
		}
	}
}

func (idx *savedStoryIndex) storeStory(storyRaw json.RawMessage) {
	var story struct {
		Hash string `json:"story_hash"`
	}
	if err := json.Unmarshal(storyRaw, &story); err != nil || story.Hash == "" {
		return
	}
	idx.stories[story.Hash] = storyRaw
}

