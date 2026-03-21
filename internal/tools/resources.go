package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/server"
	"github.com/friedenberg/nebulous/internal/newsblur"
)

// feedResourceProvider wraps a ResourceRegistry to handle template URIs
// via prefix matching (same pattern as lux).
type feedResourceProvider struct {
	registry *server.ResourceRegistry
	index    *feedIndex
	stories  *storyStore
	client   *newsblur.Client
}

func newFeedResourceProvider(
	registry *server.ResourceRegistry,
	index *feedIndex,
	stories *storyStore,
	client *newsblur.Client,
) *feedResourceProvider {
	return &feedResourceProvider{
		registry: registry,
		index:    index,
		stories:  stories,
		client:   client,
	}
}

func (p *feedResourceProvider) ListResources(
	ctx context.Context,
) ([]protocol.Resource, error) {
	return p.registry.ListResources(ctx)
}

func (p *feedResourceProvider) ListResourceTemplates(
	ctx context.Context,
) ([]protocol.ResourceTemplate, error) {
	return p.registry.ListResourceTemplates(ctx)
}

func (p *feedResourceProvider) ReadResource(
	ctx context.Context,
	uri string,
) (*protocol.ResourceReadResult, error) {
	if uri == "nebulous://tags" {
		return p.readTags(ctx, uri)
	}
	if uri == "nebulous://stories/facets" {
		return p.readStoryFacets(ctx, uri)
	}
	if uri == "nebulous://feeds/facets" {
		return p.readFeedFacets(ctx, uri)
	}
	if strings.HasPrefix(uri, "nebulous://feed_index/") {
		word := strings.TrimPrefix(uri, "nebulous://feed_index/")
		return p.readFeedIndexWord(ctx, uri, word)
	}
	if uri == "nebulous://river" {
		return p.readRiver(ctx, uri)
	}
	if strings.HasPrefix(uri, "nebulous://river/") {
		pageStr := strings.TrimPrefix(uri, "nebulous://river/")
		page, err := strconv.Atoi(pageStr)
		if err != nil {
			return nil, fmt.Errorf("invalid page number: %s", pageStr)
		}
		return p.readRiverPage(ctx, uri, page)
	}
	if strings.HasPrefix(uri, "nebulous://story/") {
		hash := strings.TrimPrefix(uri, "nebulous://story/")
		if strings.HasSuffix(hash, "/original") {
			hash = strings.TrimSuffix(hash, "/original")
			return p.readStoryOriginal(ctx, uri, hash)
		}
		if strings.HasSuffix(hash, "/content") {
			hash = strings.TrimSuffix(hash, "/content")
			return p.readStoryContent(ctx, uri, hash)
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

func (p *feedResourceProvider) readFeedIndexWord(
	ctx context.Context,
	resourceURI, word string,
) (*protocol.ResourceReadResult, error) {
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

func (p *feedResourceProvider) readFeed(
	ctx context.Context,
	resourceURI, feedID string,
) (*protocol.ResourceReadResult, error) {
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

func (p *feedResourceProvider) readTags(
	ctx context.Context,
	resourceURI string,
) (*protocol.ResourceReadResult, error) {
	if err := p.stories.ensureBuilt(); err != nil {
		return nil, fmt.Errorf("building story store: %w", err)
	}

	type tagEntry struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
	}

	sortedTags := func(m map[string]int) []tagEntry {
		entries := make([]tagEntry, 0, len(m))
		for t, c := range m {
			entries = append(entries, tagEntry{Tag: t, Count: c})
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Count != entries[j].Count {
				return entries[i].Count > entries[j].Count
			}
			return entries[i].Tag < entries[j].Tag
		})
		return entries
	}

	resp := struct {
		TotalStories int        `json:"total_stories"`
		UserTags     []tagEntry `json:"user_tags"`
		StoryTags    []tagEntry `json:"story_tags"`
	}{
		TotalStories: len(p.stories.stories),
		UserTags:     sortedTags(p.stories.userTags),
		StoryTags:    sortedTags(p.stories.storyTags),
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

func (p *feedResourceProvider) readStoryFacets(
	ctx context.Context,
	resourceURI string,
) (*protocol.ResourceReadResult, error) {
	if err := p.stories.ensureBuilt(); err != nil {
		return nil, fmt.Errorf("building story store: %w", err)
	}

	facets := p.stories.facets()
	data, err := json.MarshalIndent(facets, "", "  ")
	if err != nil {
		return nil, err
	}

	return &protocol.ResourceReadResult{
		Contents: []protocol.ResourceContent{
			{
				URI:      resourceURI,
				MimeType: "application/json",
				Text:     string(data),
			},
		},
	}, nil
}

func (p *feedResourceProvider) readFeedFacets(
	ctx context.Context,
	resourceURI string,
) (*protocol.ResourceReadResult, error) {
	if err := p.index.ensureBuilt(ctx); err != nil {
		return nil, fmt.Errorf("building feed index: %w", err)
	}

	byFolder := make(map[string]int)
	active, inactive := 0, 0

	seen := make(map[string]bool)
	for _, summaries := range p.index.words {
		for _, s := range summaries {
			id := s.ID.String()
			if seen[id] {
				continue
			}
			seen[id] = true
			if s.Folder != "" {
				byFolder[s.Folder]++
			}
			if s.Active {
				active++
			} else {
				inactive++
			}
		}
	}

	resp := struct {
		TotalFeeds int            `json:"total_feeds"`
		Active     int            `json:"active"`
		Inactive   int            `json:"inactive"`
		ByFolder   map[string]int `json:"by_folder"`
	}{
		TotalFeeds: len(seen),
		Active:     active,
		Inactive:   inactive,
		ByFolder:   byFolder,
	}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return nil, err
	}

	return &protocol.ResourceReadResult{
		Contents: []protocol.ResourceContent{
			{
				URI:      resourceURI,
				MimeType: "application/json",
				Text:     string(data),
			},
		},
	}, nil
}

func (p *feedResourceProvider) readFeedStories(
	ctx context.Context,
	resourceURI, feedID string,
) (*protocol.ResourceReadResult, error) {
	id, err := strconv.Atoi(feedID)
	if err != nil {
		return nil, fmt.Errorf("invalid feed ID: %s", feedID)
	}

	if err := p.stories.ensureBuilt(); err != nil {
		return nil, fmt.Errorf("building story store: %w", err)
	}

	type storySummary struct {
		Hash      string   `json:"hash"`
		Title     string   `json:"title"`
		Authors   string   `json:"authors,omitempty"`
		Date      string   `json:"date"`
		Permalink string   `json:"permalink"`
		Tags      []string `json:"tags,omitempty"`
		UserTags  []string `json:"user_tags,omitempty"`
	}

	var matches []storySummary
	for _, rec := range p.stories.stories {
		if rec.FeedID == id {
			matches = append(matches, storySummary{
				Hash:      rec.Hash,
				Title:     rec.Title,
				Authors:   rec.Authors,
				Date:      rec.Date.Format("2006-01-02 15:04:05"),
				Permalink: rec.Permalink,
				Tags:      rec.Tags,
				UserTags:  rec.UserTags,
			})
		}
	}

	resp := struct {
		FeedID  int            `json:"feed_id"`
		Count   int            `json:"count"`
		Stories []storySummary `json:"stories"`
	}{
		FeedID:  id,
		Count:   len(matches),
		Stories: matches,
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

// readStory returns compact metadata only — no content, no NewsBlur noise
// fields.
// Safe to read in top-level context without blowing up token budget.
func (p *feedResourceProvider) readStory(
	ctx context.Context,
	resourceURI, storyHash string,
) (*protocol.ResourceReadResult, error) {
	rec, ok := p.stories.storyByHash(storyHash)
	if !ok {
		return nil, fmt.Errorf("story not found in store: %s", storyHash)
	}

	meta := struct {
		Hash          string   `json:"hash"`
		Title         string   `json:"title"`
		Authors       string   `json:"authors,omitempty"`
		FeedID        int      `json:"feed_id"`
		Date          string   `json:"date"`
		Permalink     string   `json:"permalink"`
		Tags          []string `json:"tags,omitempty"`
		UserTags      []string `json:"user_tags,omitempty"`
		HasContent    bool     `json:"has_content"`
		ContentTokens int      `json:"content_tokens"`
	}{
		Hash:          rec.Hash,
		Title:         rec.Title,
		Authors:       rec.Authors,
		FeedID:        rec.FeedID,
		Date:          rec.Date.Format("2006-01-02 15:04:05"),
		Permalink:     rec.Permalink,
		Tags:          rec.Tags,
		UserTags:      rec.UserTags,
		HasContent:    rec.HasContent,
		ContentTokens: rec.ContentTokens,
	}

	data, err := json.Marshal(meta)
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

// readStoryContent returns the cached story_content (HTML stripped, truncated).
// Middle tier: more than metadata, less than original article.
func (p *feedResourceProvider) readStoryContent(
	ctx context.Context,
	resourceURI, storyHash string,
) (*protocol.ResourceReadResult, error) {
	raw, ok := p.stories.rawStoryByHash(storyHash)
	if !ok {
		return nil, fmt.Errorf("story not found in store: %s", storyHash)
	}

	var full struct {
		Hash      string `json:"story_hash"`
		Title     string `json:"story_title"`
		Content   string `json:"story_content"`
		Permalink string `json:"story_permalink"`
	}
	if err := json.Unmarshal(raw, &full); err != nil {
		return nil, fmt.Errorf("parsing story: %w", err)
	}

	text := stripHTMLTags(full.Content)
	truncated := false
	if len(text) > 4000 {
		text = text[:4000]
		truncated = true
	}

	hasContent := len(text) > 200

	resp := struct {
		Hash       string `json:"hash"`
		Title      string `json:"title"`
		Permalink  string `json:"permalink"`
		Content    string `json:"content"`
		HasContent bool   `json:"has_content"`
		Truncated  bool   `json:"truncated"`
	}{
		Hash:       full.Hash,
		Title:      full.Title,
		Permalink:  full.Permalink,
		Content:    text,
		HasContent: hasContent,
		Truncated:  truncated,
	}

	data, err := json.Marshal(resp)
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

func (p *feedResourceProvider) readStoryOriginal(
	ctx context.Context,
	resourceURI, storyHash string,
) (*protocol.ResourceReadResult, error) {
	raw, ok := p.client.CachedOriginalText(storyHash)
	if !ok {
		return nil, fmt.Errorf("original text not in cache for story %s (run 'nebulous fetch' to populate)", storyHash)
	}

	return &protocol.ResourceReadResult{
		Contents: []protocol.ResourceContent{{
			URI:      resourceURI,
			MimeType: "application/json",
			Text:     string(raw),
		}},
	}, nil
}

func (p *feedResourceProvider) readRiver(ctx context.Context, resourceURI string) (*protocol.ResourceReadResult, error) {
	if err := p.stories.ensureBuilt(); err != nil {
		return nil, fmt.Errorf("building story store: %w", err)
	}

	// Count cached pages
	totalPages := 0
	for page := 1; ; page++ {
		if _, ok := p.client.CachedStarredStoryPage(page); !ok {
			break
		}
		totalPages = page
	}

	type pageEntry struct {
		Page int    `json:"page"`
		URI  string `json:"uri"`
	}

	pages := make([]pageEntry, 0, totalPages)
	for i := 1; i <= totalPages; i++ {
		pages = append(pages, pageEntry{
			Page: i,
			URI:  fmt.Sprintf("nebulous://river/%d", i),
		})
	}

	resp := struct {
		TotalPages   int         `json:"total_pages"`
		TotalStories int         `json:"total_stories"`
		Pages        []pageEntry `json:"pages"`
	}{
		TotalPages:   totalPages,
		TotalStories: len(p.stories.stories),
		Pages:        pages,
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

func (p *feedResourceProvider) readRiverPage(ctx context.Context, resourceURI string, page int) (*protocol.ResourceReadResult, error) {
	raw, ok := p.client.CachedStarredStoryPage(page)
	if !ok {
		return nil, fmt.Errorf("page %d not in cache", page)
	}

	var resp struct {
		Stories []json.RawMessage `json:"stories"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parsing cached page: %w", err)
	}

	type riverSummary struct {
		Hash      string `json:"hash"`
		Title     string `json:"title"`
		FeedID    int    `json:"feed_id"`
		Date      string `json:"date"`
		Permalink string `json:"permalink"`
	}

	summaries := make([]riverSummary, 0, len(resp.Stories))
	for _, storyRaw := range resp.Stories {
		var story struct {
			Hash      string `json:"story_hash"`
			Title     string `json:"story_title"`
			FeedID    int    `json:"story_feed_id"`
			Date      string `json:"story_date"`
			Permalink string `json:"story_permalink"`
		}
		if err := json.Unmarshal(storyRaw, &story); err != nil {
			continue
		}
		summaries = append(summaries, riverSummary{
			Hash:      story.Hash,
			Title:     story.Title,
			FeedID:    story.FeedID,
			Date:      story.Date,
			Permalink: story.Permalink,
		})
	}

	result := struct {
		Page    int            `json:"page"`
		Count   int            `json:"count"`
		Stories []riverSummary `json:"stories"`
	}{
		Page:    page,
		Count:   len(summaries),
		Stories: summaries,
	}

	data, err := json.MarshalIndent(result, "", "  ")
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

func registerResources(
	registry *server.ResourceRegistry,
	index *feedIndex,
	stories *storyStore,
) {
	registry.RegisterResource(
		protocol.Resource{
			URI:         "nebulous://river",
			Name:        "Story River",
			Description: "Index of cached story pages. Returns page count, total stories, and URIs for each nebulous://river/{page} resource. Use as entry point for browsing all available stories.",
			MimeType:    "application/json",
		},
		nil, // Handled by feedResourceProvider
	)

	registry.RegisterTemplate(
		protocol.ResourceTemplate{
			URITemplate: "nebulous://river/{page}",
			Name:        "Story River Page",
			Description: "Compact story summaries (hash, title, feed_id, date, permalink) for a cached page. No API call. Use nebulous://river to discover available pages.",
			MimeType:    "application/json",
		},
		nil, // Handled by feedResourceProvider
	)

	registry.RegisterResource(
		protocol.Resource{
			URI:         "nebulous://feed_index",
			Name:        "Feed Index",
			Description: "Word index of all subscribed feeds. Returns word list for progressive discovery. Pipeline: feed_query(words) → feed/{id} → feed/{id}/stories. Prefer feed_query tool over this resource — it searches directly. Best used via subagent.",
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
			Description: "Look up feeds matching a word. Returns compact feed summaries (id, title, folder, unread counts, active). Prefer feed_query tool instead — it accepts multiple words in one call. Use feed IDs to drill into feed/{feed_id} or feed/{feed_id}/stories.",
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
			Description: "Starred stories from a feed, from local index. Returns compact summaries (hash, title, date, permalink, tags). No API call.",
			MimeType:    "application/json",
		},
		nil,
	)

	registry.RegisterTemplate(
		protocol.ResourceTemplate{
			URITemplate: "nebulous://story/{story_hash}",
			Name:        "Story Metadata",
			Description: "Compact story metadata from cache (~200 bytes, no API call). Returns hash, title, authors, date, permalink, tags, has_content, content_tokens. Safe to read in bulk without blowing context. Use has_content and content_tokens to decide whether to drill into story/{hash}/content or story/{hash}/original.",
			MimeType:    "application/json",
		},
		nil,
	)

	registry.RegisterTemplate(
		protocol.ResourceTemplate{
			URITemplate: "nebulous://story/{story_hash}/content",
			Name:        "Story Content",
			Description: "Cached story content as plain text (HTML stripped, max 4000 chars). From local cache — no API call. When has_content=false (stub), content will be minimal; use story/{hash}/original instead to fetch from source URL.",
			MimeType:    "application/json",
		},
		nil,
	)

	registry.RegisterTemplate(
		protocol.ResourceTemplate{
			URITemplate: "nebulous://story/{story_hash}/original",
			Name:        "Story Original Text",
			Description: "Full original article text from local cache. Use when story/{hash} shows has_content=false or content was truncated. No API call — requires 'nebulous fetch' to have populated the cache. Response can be large — delegate to a subagent.",
			MimeType:    "application/json",
		},
		nil,
	)

	registry.RegisterResource(
		protocol.Resource{
			URI:         "nebulous://stories/facets",
			Name:        "Story Facets",
			Description: "Aggregate counts of all indexed stories by year, tag, feed, and status. Read this first to understand the data shape before querying with story_query. Lightweight — no story content, just counts.",
			MimeType:    "application/json",
		},
		nil,
	)

	registry.RegisterResource(
		protocol.Resource{
			URI:         "nebulous://feeds/facets",
			Name:        "Feed Facets",
			Description: "Aggregate counts of subscribed feeds by folder and active/inactive status. Lightweight overview of the subscription list.",
			MimeType:    "application/json",
		},
		nil,
	)

	registry.RegisterResource(
		protocol.Resource{
			URI:         "nebulous://tags",
			Name:        "Tags",
			Description: "All tags across indexed stories, sorted by frequency. Shows user_tags (star tags you assigned) and story_tags (feed-assigned categories) separately with counts. Lightweight entry point for tag discovery.",
			MimeType:    "application/json",
		},
		nil,
	)
}

func registerResourceToolCommands(app *command.App, resProvider *feedResourceProvider) {
	readOnly := true
	notDestructive := false
	idempotent := true

	app.AddCommand(&command.Command{
		Name: "resource-templates",
		Description: command.Description{
			Short: "List available nebulous resource templates. Call this first to discover what resources are available, then use resource-read to access them.",
		},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    &readOnly,
			DestructiveHint: &notDestructive,
			IdempotentHint:  &idempotent,
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			templates, err := resProvider.ListResourceTemplates(ctx)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			resources, err := resProvider.ListResources(ctx)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			var sb strings.Builder
			sb.WriteString("Resource templates (fill in {placeholders} and pass to resource-read):\n\n")
			for _, t := range templates {
				fmt.Fprintf(&sb, "- %s: %s\n  %s\n", t.Name, t.URITemplate, t.Description)
			}

			if len(resources) > 0 {
				sb.WriteString("\nStatic resources (pass URI directly to resource-read):\n\n")
				for _, r := range resources {
					fmt.Fprintf(&sb, "- %s: %s\n  %s\n", r.Name, r.URI, r.Description)
				}
			}

			return command.TextResult(sb.String()), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "resource-read",
		Description: command.Description{
			Short: "Read a nebulous resource by URI. This tool exists because subagents cannot access MCP resources directly. Call resource-templates to discover available URIs.",
		},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    &readOnly,
			DestructiveHint: &notDestructive,
			IdempotentHint:  &idempotent,
		},
		Params: []command.Param{
			{Name: "uri", Type: command.String, Description: "Resource URI (e.g., nebulous://stories/facets, nebulous://story/6327282:5d1cf5/content)", Required: true},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var a struct {
				URI string `json:"uri"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return command.TextErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
			}

			result, err := resProvider.ReadResource(ctx, a.URI)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			var sb strings.Builder
			for i, c := range result.Contents {
				if i > 0 {
					sb.WriteString("\n---\n")
				}
				sb.WriteString(c.Text)
			}

			return command.TextResult(sb.String()), nil
		},
	})
}
