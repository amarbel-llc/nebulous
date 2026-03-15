package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/server"
	"github.com/friedenberg/nebulous/internal/newsblur"
)

// feedResourceProvider wraps a ResourceRegistry to handle template URIs
// via prefix matching (same pattern as lux).
type feedResourceProvider struct {
	registry       *server.ResourceRegistry
	index          *feedIndex
	savedStories   *savedStoryIndex
	client         *newsblur.Client
}

func newFeedResourceProvider(registry *server.ResourceRegistry, index *feedIndex, savedStories *savedStoryIndex, client *newsblur.Client) *feedResourceProvider {
	return &feedResourceProvider{
		registry:     registry,
		index:        index,
		savedStories: savedStories,
		client:       client,
	}
}

func (p *feedResourceProvider) ListResources(ctx context.Context) ([]protocol.Resource, error) {
	return p.registry.ListResources(ctx)
}

func (p *feedResourceProvider) ListResourceTemplates(ctx context.Context) ([]protocol.ResourceTemplate, error) {
	return p.registry.ListResourceTemplates(ctx)
}

func (p *feedResourceProvider) ReadResource(ctx context.Context, uri string) (*protocol.ResourceReadResult, error) {
	if strings.HasPrefix(uri, "nebulous://feed_index/") {
		word := strings.TrimPrefix(uri, "nebulous://feed_index/")
		return p.readFeedIndexWord(ctx, uri, word)
	}
	if strings.HasPrefix(uri, "nebulous://saved_story_index/") {
		word := strings.TrimPrefix(uri, "nebulous://saved_story_index/")
		return p.readSavedStoryIndexWord(ctx, uri, word)
	}
	if strings.HasPrefix(uri, "nebulous://story/") {
		hash := strings.TrimPrefix(uri, "nebulous://story/")
		if strings.HasSuffix(hash, "/original") {
			hash = strings.TrimSuffix(hash, "/original")
			return p.readStoryOriginal(ctx, uri, hash)
		}
		return p.readStory(ctx, uri, hash)
	}
	if strings.HasPrefix(uri, "nebulous://feed/") {
		rest := strings.TrimPrefix(uri, "nebulous://feed/")
		if id, ok := strings.CutSuffix(rest, "/stories"); ok {
			return p.readFeedStories(ctx, uri, id)
		}
		return p.readFeed(ctx, uri, rest)
	}
	return p.registry.ReadResource(ctx, uri)
}

func (p *feedResourceProvider) readFeedIndexWord(ctx context.Context, resourceURI, word string) (*protocol.ResourceReadResult, error) {
	if err := p.index.ensureBuilt(ctx); err != nil {
		return nil, fmt.Errorf("building feed index: %w", err)
	}

	word = strings.ToLower(word)
	feeds := p.index.words[word]

	resp := struct {
		Word  string        `json:"word"`
		Count int           `json:"count"`
		Feeds []feedSummary `json:"feeds"`
	}{
		Word:  word,
		Count: len(feeds),
		Feeds: feeds,
	}

	if resp.Feeds == nil {
		resp.Feeds = []feedSummary{}
	}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return nil, err
	}

	return &protocol.ResourceReadResult{
		Contents: []protocol.ResourceContent{{
			URI:      resourceURI,
			MimeType: "application/json",
			Text:     string(data),
		}},
	}, nil
}

func (p *feedResourceProvider) readFeed(ctx context.Context, resourceURI, feedID string) (*protocol.ResourceReadResult, error) {
	if err := p.index.ensureBuilt(ctx); err != nil {
		return nil, fmt.Errorf("building feed index: %w", err)
	}

	raw, ok := p.index.feeds[feedID]
	if !ok {
		return nil, fmt.Errorf("unknown feed: %s", feedID)
	}

	return &protocol.ResourceReadResult{
		Contents: []protocol.ResourceContent{{
			URI:      resourceURI,
			MimeType: "application/json",
			Text:     string(raw),
		}},
	}, nil
}

func (p *feedResourceProvider) readSavedStoryIndexWord(ctx context.Context, resourceURI, word string) (*protocol.ResourceReadResult, error) {
	res := p.savedStories.ensureBuilt()
	if res.words == nil {
		return nil, fmt.Errorf("building saved story index: %s", res.warning)
	}

	word = strings.ToLower(word)
	stories := res.words[word]

	resp := struct {
		Word    string         `json:"word"`
		Count   int            `json:"count"`
		Stories []storySummary `json:"stories"`
	}{
		Word:    word,
		Count:   len(stories),
		Stories: stories,
	}

	if resp.Stories == nil {
		resp.Stories = []storySummary{}
	}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return nil, err
	}

	return &protocol.ResourceReadResult{
		Contents: []protocol.ResourceContent{{
			URI:      resourceURI,
			MimeType: "application/json",
			Text:     string(data),
		}},
	}, nil
}

func (p *feedResourceProvider) readFeedStories(ctx context.Context, resourceURI, feedID string) (*protocol.ResourceReadResult, error) {
	id, err := strconv.Atoi(feedID)
	if err != nil {
		return nil, fmt.Errorf("invalid feed ID: %s", feedID)
	}

	raw, err := p.client.StoriesFeed(ctx, id, 0, "", "", "")
	if err != nil {
		return nil, fmt.Errorf("fetching stories: %w", err)
	}

	return &protocol.ResourceReadResult{
		Contents: []protocol.ResourceContent{{
			URI:      resourceURI,
			MimeType: "application/json",
			Text:     string(raw),
		}},
	}, nil
}

func (p *feedResourceProvider) readStory(ctx context.Context, resourceURI, storyHash string) (*protocol.ResourceReadResult, error) {
	raw, ok := p.savedStories.storyByHash(storyHash)
	if !ok {
		return nil, fmt.Errorf("story not found in cache: %s", storyHash)
	}

	return &protocol.ResourceReadResult{
		Contents: []protocol.ResourceContent{{
			URI:      resourceURI,
			MimeType: "application/json",
			Text:     string(raw),
		}},
	}, nil
}

func (p *feedResourceProvider) readStoryOriginal(ctx context.Context, resourceURI, storyHash string) (*protocol.ResourceReadResult, error) {
	raw, err := p.client.OriginalText(ctx, storyHash)
	if err != nil {
		return nil, fmt.Errorf("fetching original text: %w", err)
	}

	return &protocol.ResourceReadResult{
		Contents: []protocol.ResourceContent{{
			URI:      resourceURI,
			MimeType: "application/json",
			Text:     string(raw),
		}},
	}, nil
}

func registerResources(registry *server.ResourceRegistry, index *feedIndex, savedStories *savedStoryIndex) {
	registry.RegisterResource(
		protocol.Resource{
			URI:         "nebulous://feed_index",
			Name:        "Feed Index",
			Description: "Word index of all subscribed feeds. Returns word list for progressive discovery. Start here, then drill into feed_index/{word} → feed/{id} → feed/{id}/stories. Best used via subagent to keep main context lean.",
			MimeType:    "application/json",
		},
		func(ctx context.Context, uri string) (*protocol.ResourceReadResult, error) {
			if err := index.ensureBuilt(ctx); err != nil {
				return nil, fmt.Errorf("building feed index: %w", err)
			}

			words := make([]string, 0, len(index.words))
			for w := range index.words {
				words = append(words, w)
			}
			sort.Strings(words)

			// Count unique feeds
			feedsSeen := make(map[string]bool)
			for _, summaries := range index.words {
				for _, s := range summaries {
					feedsSeen[s.ID.String()] = true
				}
			}

			resp := struct {
				TotalWords int      `json:"total_words"`
				TotalFeeds int      `json:"total_feeds"`
				Words      []string `json:"words"`
			}{
				TotalWords: len(words),
				TotalFeeds: len(feedsSeen),
				Words:      words,
			}

			data, err := json.MarshalIndent(resp, "", "  ")
			if err != nil {
				return nil, err
			}

			return &protocol.ResourceReadResult{
				Contents: []protocol.ResourceContent{{
					URI:      uri,
					MimeType: "application/json",
					Text:     string(data),
				}},
			}, nil
		},
	)

	registry.RegisterTemplate(
		protocol.ResourceTemplate{
			URITemplate: "nebulous://feed_index/{word}",
			Name:        "Feed Index Word Lookup",
			Description: "Look up feeds matching a word. Returns compact feed summaries (id, title, folder, unread counts, active). Use feed IDs from results to drill into feed/{feed_id} or feed/{feed_id}/stories.",
			MimeType:    "application/json",
		},
		nil, // Template URIs handled by feedResourceProvider
	)

	registry.RegisterTemplate(
		protocol.ResourceTemplate{
			URITemplate: "nebulous://feed/{feed_id}",
			Name:        "Feed Details",
			Description: "Full feed metadata (~40 fields). Response is verbose — best consumed via subagent.",
			MimeType:    "application/json",
		},
		nil, // Template URIs handled by feedResourceProvider
	)

	registry.RegisterTemplate(
		protocol.ResourceTemplate{
			URITemplate: "nebulous://feed/{feed_id}/stories",
			Name:        "Feed Stories",
			Description: "Stories from a feed with full HTML content. Response is very large — delegate to a subagent.",
			MimeType:    "application/json",
		},
		nil,
	)

	registry.RegisterTemplate(
		protocol.ResourceTemplate{
			URITemplate: "nebulous://story/{story_hash}",
			Name:        "Story Details",
			Description: "Full story metadata and content from cache (title, content, tags, date, feed, permalink). Served from local cache — no API call. Use story hashes from index queries to fetch details for specific stories.",
			MimeType:    "application/json",
		},
		nil,
	)

	registry.RegisterTemplate(
		protocol.ResourceTemplate{
			URITemplate: "nebulous://story/{story_hash}/original",
			Name:        "Story Original Text",
			Description: "Full original article text fetched from source URL. Response is very large — delegate to a subagent.",
			MimeType:    "application/json",
		},
		nil,
	)

	registry.RegisterResource(
		protocol.Resource{
			URI:         "nebulous://saved_story_index",
			Name:        "Saved Story Index",
			Description: "Word index of starred/saved story titles and content (built from cache). Start here, then drill into saved_story_index/{word} → story/{hash}/original. Best used via subagent.",
			MimeType:    "application/json",
		},
		func(ctx context.Context, uri string) (*protocol.ResourceReadResult, error) {
			res := savedStories.ensureBuilt()
			if res.words == nil {
				return nil, fmt.Errorf("building saved story index: %s", res.warning)
			}

			words := make([]string, 0, len(res.words))
			for w := range res.words {
				words = append(words, w)
			}
			sort.Strings(words)

			totalStories := 0
			storiesSeen := make(map[string]bool)
			for _, summaries := range res.words {
				for _, s := range summaries {
					if !storiesSeen[s.Hash] {
						storiesSeen[s.Hash] = true
						totalStories++
					}
				}
			}

			resp := struct {
				TotalWords   int      `json:"total_words"`
				TotalStories int      `json:"total_stories"`
				Words        []string `json:"words"`
			}{
				TotalWords:   len(words),
				TotalStories: totalStories,
				Words:        words,
			}

			data, err := json.MarshalIndent(resp, "", "  ")
			if err != nil {
				return nil, err
			}

			return &protocol.ResourceReadResult{
				Contents: []protocol.ResourceContent{{
					URI:      uri,
					MimeType: "application/json",
					Text:     string(data),
				}},
			}, nil
		},
	)

	registry.RegisterTemplate(
		protocol.ResourceTemplate{
			URITemplate: "nebulous://saved_story_index/{word}",
			Name:        "Saved Story Index Word Lookup",
			Description: "Look up saved stories matching a word by title. Returns compact summaries (hash, title, feed_id, date, permalink). Use story hashes to drill into story/{hash}/original.",
			MimeType:    "application/json",
		},
		nil,
	)
}
