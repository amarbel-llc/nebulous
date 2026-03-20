# Index-First Architecture Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Replace the word-only saved story index with a flat story store that supports structured queries (year, month, tag, feed_id) plus word search, remove all API-coupled read tools, and add faceted discovery resources.

**Architecture:** A `storyStore` holds `[]storyRecord` with typed fields (date, tags, feed_id) parsed at build time. A single `story_query` tool replaces 10 API-coupled read tools. `nebulous://stories/facets` and `nebulous://feeds/facets` resources expose aggregate counts for query planning. The word index becomes an acceleration layer over the flat store.

**Tech Stack:** Go stdlib (`time`, `sort`, `sync`, `encoding/json`), existing `newsblur.Client` cache, go-mcp `command`/`server`/`protocol` packages.

**Rollback:** Hard cutover. `git revert` the merge commit.

---

### Task 1: Create `storyStore` with typed `storyRecord`

**Files:**
- Create: `internal/tools/story_store.go`
- Create: `internal/tools/story_store_test.go`

**Step 1: Write the failing test**

Create `internal/tools/story_store_test.go`:

```go
package tools

import (
	"encoding/json"
	"testing"
	"time"
)

func TestStoryStoreParseRecord(t *testing.T) {
	raw := json.RawMessage(`{
		"story_hash": "abc123",
		"story_title": "Test Story",
		"story_authors": "Alice",
		"story_feed_id": 42,
		"story_date": "2024-03-15 10:30:00",
		"story_permalink": "https://example.com/story",
		"story_tags": ["tech", "go"],
		"user_tags": ["interests", "zz-nyc"],
		"story_content": "<p>Hello world of Go programming</p>",
		"starred": true,
		"read_status": 1
	}`)

	rec, err := parseStoryRecord(raw, nil)
	if err != nil {
		t.Fatalf("parseStoryRecord: %v", err)
	}

	if rec.Hash != "abc123" {
		t.Errorf("Hash = %q, want %q", rec.Hash, "abc123")
	}
	if rec.Title != "Test Story" {
		t.Errorf("Title = %q, want %q", rec.Title, "Test Story")
	}
	if rec.FeedID != 42 {
		t.Errorf("FeedID = %d, want %d", rec.FeedID, 42)
	}
	if rec.Year != 2024 {
		t.Errorf("Year = %d, want %d", rec.Year, 2024)
	}
	if rec.Month != 3 {
		t.Errorf("Month = %d, want %d", rec.Month, int(time.March))
	}
	if len(rec.UserTags) != 2 || rec.UserTags[0] != "interests" {
		t.Errorf("UserTags = %v, want [interests zz-nyc]", rec.UserTags)
	}
	if !rec.Starred {
		t.Error("Starred = false, want true")
	}
	if !rec.Words["programming"] {
		t.Error("Words missing 'programming' from content")
	}
	if !rec.Words["hello"] {
		t.Error("Words missing 'hello' from content")
	}
	if !rec.Words["test"] {
		t.Error("Words missing 'test' from title")
	}
}

func TestStoryStoreParseRecordDateFormats(t *testing.T) {
	tests := []struct {
		name     string
		dateStr  string
		wantYear int
		wantMonth int
	}{
		{"standard", `"2024-03-15 10:30:00"`, 2024, 3},
		{"with timezone", `"2024-12-01 08:00:00+00:00"`, 2024, 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := json.RawMessage(`{"story_hash":"h","story_title":"t","story_feed_id":1,"story_date":` + tt.dateStr + `}`)
			rec, err := parseStoryRecord(raw, nil)
			if err != nil {
				t.Fatalf("parseStoryRecord: %v", err)
			}
			if rec.Year != tt.wantYear {
				t.Errorf("Year = %d, want %d", rec.Year, tt.wantYear)
			}
			if rec.Month != tt.wantMonth {
				t.Errorf("Month = %d, want %d", rec.Month, tt.wantMonth)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/tools/ -run TestStoryStore -v`
Expected: FAIL — `parseStoryRecord` undefined

**Step 3: Write minimal implementation**

Create `internal/tools/story_store.go`:

```go
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
	client  *newsblur.Client
	once    sync.Once
	stories []*storyRecord
	words   map[string][]*storyRecord
	err     error
}

func newStoryStore(client *newsblur.Client) *storyStore {
	return &storyStore{client: client}
}

func (s *storyStore) ensureBuilt() error {
	s.once.Do(func() {
		s.words = make(map[string][]*storyRecord)
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
			// All stories from starred pages are starred
			rec.Starred = true
			s.stories = append(s.stories, rec)

			for word := range rec.Words {
				s.words[word] = append(s.words[word], rec)
			}
		}

		total += len(resp.Stories)
	}

	// Sort by date descending (newest first)
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
		Hash      string   `json:"story_hash"`
		Title     string   `json:"story_title"`
		Authors   string   `json:"story_authors"`
		Content   string   `json:"story_content"`
		FeedID    int      `json:"story_feed_id"`
		Date      string   `json:"story_date"`
		Permalink string   `json:"story_permalink"`
		Tags      []string `json:"story_tags"`
		UserTags  []string `json:"user_tags"`
		Starred   bool     `json:"starred"`
		ReadStatus int     `json:"read_status"`
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

	// Opportunistically index cached original text
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

// storyByHash returns the raw cached JSON for a story hash (for resource reads).
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
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/tools/ -run TestStoryStore -v`
Expected: PASS

**Step 5: Commit**

```
git add internal/tools/story_store.go internal/tools/story_store_test.go
git commit -m "feat(store): add storyStore with typed storyRecord and word index"
```

---

### Task 2: Add `story_query` tool

**Files:**
- Create: `internal/tools/story_query.go`
- Create: `internal/tools/story_query_test.go`

**Step 1: Write the failing test**

Create `internal/tools/story_query_test.go`:

```go
package tools

import (
	"testing"
	"time"
)

func makeTestRecords() []*storyRecord {
	return []*storyRecord{
		{
			Hash: "a", Title: "Go and Nix", FeedID: 1,
			Date: time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC),
			Year: 2024, Month: 3, UserTags: []string{"interests"},
			Starred: true, Words: map[string]bool{"nix": true, "golang": true},
		},
		{
			Hash: "b", Title: "NYC Bikes", FeedID: 2,
			Date: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
			Year: 2024, Month: 6, UserTags: []string{"zz-nyc", "news"},
			Starred: true, Words: map[string]bool{"bikes": true, "nyc": true},
		},
		{
			Hash: "c", Title: "Old Nix Article", FeedID: 1,
			Date: time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC),
			Year: 2023, Month: 1, UserTags: []string{"interests"},
			Starred: true, Words: map[string]bool{"nix": true, "flake": true},
		},
		{
			Hash: "d", Title: "Security Post", FeedID: 3,
			Date: time.Date(2024, 11, 20, 0, 0, 0, 0, time.UTC),
			Year: 2024, Month: 11, UserTags: []string{"news"},
			Starred: true, Words: map[string]bool{"security": true, "xz": true},
		},
	}
}

func makeTestStore() *storyStore {
	stories := makeTestRecords()
	words := make(map[string][]*storyRecord)
	for _, rec := range stories {
		for word := range rec.Words {
			words[word] = append(words[word], rec)
		}
	}
	return &storyStore{stories: stories, words: words}
}

func TestQueryByYear(t *testing.T) {
	store := makeTestStore()
	results := store.query(storyQuery{Year: intPtr(2024)})
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	// Should be sorted newest first
	if results[0].Hash != "d" {
		t.Errorf("first result = %q, want %q", results[0].Hash, "d")
	}
}

func TestQueryByYearAndMonth(t *testing.T) {
	store := makeTestStore()
	results := store.query(storyQuery{Year: intPtr(2024), Month: intPtr(3)})
	if len(results) != 1 || results[0].Hash != "a" {
		t.Errorf("got %v, want [a]", hashesOf(results))
	}
}

func TestQueryByTag(t *testing.T) {
	store := makeTestStore()
	results := store.query(storyQuery{Tag: strPtr("zz-nyc")})
	if len(results) != 1 || results[0].Hash != "b" {
		t.Errorf("got %v, want [b]", hashesOf(results))
	}
}

func TestQueryByWords(t *testing.T) {
	store := makeTestStore()
	// OR-union: "nix" OR "security"
	results := store.query(storyQuery{Words: []string{"nix", "security"}})
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
}

func TestQueryWordsANDYear(t *testing.T) {
	store := makeTestStore()
	// "nix" AND year=2024
	results := store.query(storyQuery{Words: []string{"nix"}, Year: intPtr(2024)})
	if len(results) != 1 || results[0].Hash != "a" {
		t.Errorf("got %v, want [a]", hashesOf(results))
	}
}

func TestQueryByFeedID(t *testing.T) {
	store := makeTestStore()
	results := store.query(storyQuery{FeedID: intPtr(1)})
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

func TestQueryOffsetLimit(t *testing.T) {
	store := makeTestStore()
	results := store.query(storyQuery{Year: intPtr(2024), Offset: 1, Limit: 1})
	if len(results) != 1 || results[0].Hash != "b" {
		t.Errorf("got %v, want [b]", hashesOf(results))
	}
}

func TestQueryNoFilters(t *testing.T) {
	store := makeTestStore()
	results := store.query(storyQuery{Limit: 100})
	if len(results) != 4 {
		t.Fatalf("got %d results, want 4", len(results))
	}
}

func intPtr(n int) *int       { return &n }
func strPtr(s string) *string { return &s }

func hashesOf(recs []*storyRecord) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Hash
	}
	return out
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/tools/ -run TestQuery -v`
Expected: FAIL — `storyQuery` and `store.query` undefined

**Step 3: Write minimal implementation**

Create `internal/tools/story_query.go`:

```go
package tools

type storyQuery struct {
	Words   []string
	Year    *int
	Month   *int
	Tag     *string
	FeedID  *int
	Starred *bool
	Read    *bool
	Offset  int
	Limit   int
}

func (s *storyStore) query(q storyQuery) []*storyRecord {
	if q.Limit <= 0 {
		q.Limit = 100
	}

	var candidates []*storyRecord

	// If words are provided, start from the word index (OR-union)
	if len(q.Words) > 0 {
		seen := make(map[string]bool)
		for _, word := range q.Words {
			for _, rec := range s.words[word] {
				if !seen[rec.Hash] {
					seen[rec.Hash] = true
					candidates = append(candidates, rec)
				}
			}
		}
	} else {
		candidates = s.stories
	}

	// Apply structural filters
	var filtered []*storyRecord
	for _, rec := range candidates {
		if q.Year != nil && rec.Year != *q.Year {
			continue
		}
		if q.Month != nil && rec.Month != *q.Month {
			continue
		}
		if q.Tag != nil {
			found := false
			for _, tag := range rec.UserTags {
				if tag == *q.Tag {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if q.FeedID != nil && rec.FeedID != *q.FeedID {
			continue
		}
		if q.Starred != nil && rec.Starred != *q.Starred {
			continue
		}
		if q.Read != nil && rec.Read != *q.Read {
			continue
		}
		filtered = append(filtered, rec)
	}

	// Sort by date descending (word index results may be unordered)
	if len(q.Words) > 0 {
		sortStoriesDesc(filtered)
	}

	// Apply offset/limit
	if q.Offset >= len(filtered) {
		return nil
	}
	filtered = filtered[q.Offset:]
	if len(filtered) > q.Limit {
		filtered = filtered[:q.Limit]
	}

	return filtered
}

func sortStoriesDesc(stories []*storyRecord) {
	// Use sort.Slice — at ~1500 stories this is sub-millisecond
	for i := 0; i < len(stories); i++ {
		for j := i + 1; j < len(stories); j++ {
			if stories[j].Date.After(stories[i].Date) {
				stories[i], stories[j] = stories[j], stories[i]
			}
		}
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/tools/ -run TestQuery -v`
Expected: PASS

**Step 5: Commit**

```
git add internal/tools/story_query.go internal/tools/story_query_test.go
git commit -m "feat(store): add storyQuery with structured filters and word search"
```

---

### Task 3: Add `stories/facets` and `feeds/facets` resources

**Files:**
- Create: `internal/tools/facets.go`
- Create: `internal/tools/facets_test.go`

**Step 1: Write the failing test**

Create `internal/tools/facets_test.go`:

```go
package tools

import (
	"encoding/json"
	"testing"
)

func TestStoryFacets(t *testing.T) {
	store := makeTestStore()
	facets := store.facets()

	if facets.TotalStories != 4 {
		t.Errorf("TotalStories = %d, want 4", facets.TotalStories)
	}
	if facets.ByYear[2024] != 3 {
		t.Errorf("ByYear[2024] = %d, want 3", facets.ByYear[2024])
	}
	if facets.ByYear[2023] != 1 {
		t.Errorf("ByYear[2023] = %d, want 1", facets.ByYear[2023])
	}
	if facets.ByTag["interests"] != 2 {
		t.Errorf("ByTag[interests] = %d, want 2", facets.ByTag["interests"])
	}
	if facets.ByTag["zz-nyc"] != 1 {
		t.Errorf("ByTag[zz-nyc] = %d, want 1", facets.ByTag["zz-nyc"])
	}
	if facets.ByFeed[1].Count != 2 {
		t.Errorf("ByFeed[1].Count = %d, want 2", facets.ByFeed[1].Count)
	}
	if facets.ByStatus["starred"] != 4 {
		t.Errorf("ByStatus[starred] = %d, want 4", facets.ByStatus["starred"])
	}
	if len(facets.Years) != 2 || facets.Years[0] != 2024 {
		t.Errorf("Years = %v, want [2024 2023]", facets.Years)
	}

	// Verify JSON serialization works
	data, err := json.Marshal(facets)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if len(data) == 0 {
		t.Error("empty JSON")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/tools/ -run TestStoryFacets -v`
Expected: FAIL — `store.facets` undefined

**Step 3: Write minimal implementation**

Create `internal/tools/facets.go`:

```go
package tools

import "sort"

type feedFacetEntry struct {
	Count int    `json:"count"`
	Title string `json:"title,omitempty"`
}

type storyFacets struct {
	TotalStories int                       `json:"total_stories"`
	ByYear       map[int]int               `json:"by_year"`
	ByTag        map[string]int            `json:"by_tag"`
	ByFeed       map[int]*feedFacetEntry   `json:"by_feed"`
	ByStatus     map[string]int            `json:"by_status"`
	Years        []int                     `json:"years"`
}

func (s *storyStore) facets() *storyFacets {
	f := &storyFacets{
		TotalStories: len(s.stories),
		ByYear:       make(map[int]int),
		ByTag:        make(map[string]int),
		ByFeed:       make(map[int]*feedFacetEntry),
		ByStatus:     make(map[string]int),
	}

	for _, rec := range s.stories {
		f.ByYear[rec.Year]++

		for _, tag := range rec.UserTags {
			f.ByTag[tag]++
		}

		entry, ok := f.ByFeed[rec.FeedID]
		if !ok {
			entry = &feedFacetEntry{}
			f.ByFeed[rec.FeedID] = entry
		}
		entry.Count++

		if rec.Starred {
			f.ByStatus["starred"]++
		}
		if rec.Read {
			f.ByStatus["read"]++
		} else {
			f.ByStatus["unread"]++
		}
	}

	// Build sorted years list (descending)
	for year := range f.ByYear {
		f.Years = append(f.Years, year)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(f.Years)))

	return f
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/tools/ -run TestStoryFacets -v`
Expected: PASS

**Step 5: Commit**

```
git add internal/tools/facets.go internal/tools/facets_test.go
git commit -m "feat(store): add storyFacets for aggregate counts by year/tag/feed/status"
```

---

### Task 4: Register `story_query` tool and wire up `storyStore`

**Files:**
- Modify: `internal/tools/registry.go`
- Create: `internal/tools/story_query_tool.go`

**Step 1: Create the MCP tool handler**

Create `internal/tools/story_query_tool.go`:

```go
package tools

import (
	"context"
	"encoding/json"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
)

type storyQuerySummary struct {
	Hash      string   `json:"hash"`
	Title     string   `json:"title"`
	Authors   string   `json:"authors,omitempty"`
	FeedID    int      `json:"feed_id"`
	Date      string   `json:"date"`
	UserTags  []string `json:"user_tags,omitempty"`
	Permalink string   `json:"permalink"`
}

func registerStoryQueryCommand(app *command.App, store *storyStore) {
	app.AddCommand(&command.Command{
		Name: "story_query",
		Description: command.Description{
			Short: "Query stories with structured filters and/or word search. Returns compact summaries sorted by date descending. Start with nebulous://stories/facets to see available years, tags, and feeds. Pipeline: stories/facets → story_query(filters) → story/{hash} (metadata) → story/{hash}/content (text) → story/{hash}/original (full article). Fan out story/{hash} reads to subagents in parallel.",
		},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    protocol.BoolPtr(true),
			DestructiveHint: protocol.BoolPtr(false),
			IdempotentHint:  protocol.BoolPtr(true),
			OpenWorldHint:   protocol.BoolPtr(false),
		},
		Params: []command.Param{
			{Name: "words", Type: command.Array, Description: "Words to search for (OR-union, then AND with other filters)"},
			{Name: "year", Type: command.Int, Description: "Filter by year (e.g. 2024)"},
			{Name: "month", Type: command.Int, Description: "Filter by month (1-12, requires year)"},
			{Name: "tag", Type: command.String, Description: "Filter by user tag (e.g. zz-nyc, interests, news)"},
			{Name: "feed_id", Type: command.Int, Description: "Filter by feed ID"},
			{Name: "starred", Type: command.Bool, Description: "Filter by starred status"},
			{Name: "read", Type: command.Bool, Description: "Filter by read status"},
			{Name: "offset", Type: command.Int, Description: "Skip first N results (default 0)"},
			{Name: "limit", Type: command.Int, Description: "Max results to return (default 100)"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			if store == nil {
				return command.TextErrorResult("story store not available (no client)"), nil
			}
			if err := store.ensureBuilt(); err != nil {
				return command.TextErrorResult("building story store: " + err.Error()), nil
			}

			var p struct {
				Words   []string `json:"words"`
				Year    *int     `json:"year"`
				Month   *int     `json:"month"`
				Tag     *string  `json:"tag"`
				FeedID  *int     `json:"feed_id"`
				Starred *bool    `json:"starred"`
				Read    *bool    `json:"read"`
				Offset  int      `json:"offset"`
				Limit   int      `json:"limit"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}

			q := storyQuery{
				Words:   p.Words,
				Year:    p.Year,
				Month:   p.Month,
				Tag:     p.Tag,
				FeedID:  p.FeedID,
				Starred: p.Starred,
				Read:    p.Read,
				Offset:  p.Offset,
				Limit:   p.Limit,
			}

			results := store.query(q)

			summaries := make([]storyQuerySummary, len(results))
			for i, rec := range results {
				summaries[i] = storyQuerySummary{
					Hash:      rec.Hash,
					Title:     rec.Title,
					Authors:   rec.Authors,
					FeedID:    rec.FeedID,
					Date:      rec.Date.Format("2006-01-02 15:04:05"),
					UserTags:  rec.UserTags,
					Permalink: rec.Permalink,
				}
			}

			resp := struct {
				Total   int                 `json:"total"`
				Offset  int                 `json:"offset"`
				Limit   int                 `json:"limit"`
				Results []storyQuerySummary `json:"results"`
			}{
				Total:   len(summaries),
				Offset:  q.Offset,
				Limit:   q.Limit,
				Results: summaries,
			}

			return command.JSONResult(resp), nil
		},
	})
}
```

**Step 2: Wire storyStore into registry.go**

Replace the contents of `internal/tools/registry.go`:

```go
package tools

import (
	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/server"
	"github.com/friedenberg/nebulous/internal/newsblur"
)

func RegisterAll(client *newsblur.Client) (*command.App, server.ResourceProvider) {
	app := command.NewApp("nebulous", "NewsBlur MCP server")
	app.Version = "0.1.0"

	var feedIdx *feedIndex
	var storyStr *storyStore
	if client != nil {
		feedIdx = newFeedIndex(client)
		storyStr = newStoryStore(client)
	}

	registerFeedCommands(app, client, feedIdx)
	registerStoryQueryCommand(app, storyStr)
	registerReaderCommands(app, client)
	registerSubscriptionCommands(app, client)
	registerFolderCommands(app, client)
	registerImportExportCommands(app, client)

	var resources server.ResourceProvider
	if feedIdx != nil {
		registry := server.NewResourceRegistry()
		registerResources(registry, feedIdx, storyStr)
		resources = newFeedResourceProvider(registry, feedIdx, storyStr, client)
	}

	return app, resources
}
```

**Step 3: Run build to verify compilation**

Run: `go build ./...`
Expected: compilation errors — `registerResources` and `newFeedResourceProvider` signatures changed. Don't fix yet — Task 5 handles the resource rewiring.

**Step 4: Commit** (skip until Task 5 resolves compilation)

---

### Task 5: Rewire resources to use `storyStore`

**Files:**
- Modify: `internal/tools/resources.go` — replace `*savedStoryIndex` with `*storyStore`

**Step 1: Update `feedResourceProvider` struct**

Replace `savedStories *savedStoryIndex` with `stories *storyStore` in the struct and constructor. Update all methods that reference `savedStories` to use `stories`.

The `readStory` method changes from looking up raw JSON in the old index to building the response from the `storyRecord`:

```go
func (p *feedResourceProvider) readStory(ctx context.Context, resourceURI, storyHash string) (*protocol.ResourceReadResult, error) {
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
```

The `readStoryContent` method needs access to the raw cached JSON for the story content. Add a `rawStoryByHash` method to `storyStore` that reads from the cached page data:

Add to `internal/tools/story_store.go`:

```go
func (s *storyStore) rawStoryByHash(hash string) (json.RawMessage, bool) {
	if err := s.ensureBuilt(); err != nil {
		return nil, false
	}
	// Walk cached pages to find raw JSON
	for page := 1; ; page++ {
		raw, ok := s.client.CachedStarredStoryPage(page)
		if !ok {
			break
		}
		var resp struct {
			Stories []json.RawMessage `json:"stories"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
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
```

Update `readStoryContent` to use `p.stories.rawStoryByHash(storyHash)`.

**Step 2: Add `stories/facets` resource handler**

Add to the `ReadResource` prefix matching in `feedResourceProvider`:

```go
if uri == "nebulous://stories/facets" {
	return p.readStoryFacets(ctx, uri)
}
```

```go
func (p *feedResourceProvider) readStoryFacets(ctx context.Context, resourceURI string) (*protocol.ResourceReadResult, error) {
	if err := p.stories.ensureBuilt(); err != nil {
		return nil, fmt.Errorf("building story store: %w", err)
	}

	facets := p.stories.facets()

	data, err := json.MarshalIndent(facets, "", "  ")
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
```

**Step 3: Add `feeds/facets` resource handler**

Add to `ReadResource`:

```go
if uri == "nebulous://feeds/facets" {
	return p.readFeedFacets(ctx, uri)
}
```

```go
func (p *feedResourceProvider) readFeedFacets(ctx context.Context, resourceURI string) (*protocol.ResourceReadResult, error) {
	if err := p.index.ensureBuilt(ctx); err != nil {
		return nil, fmt.Errorf("building feed index: %w", err)
	}

	type folderCount struct {
		Folder string `json:"folder"`
		Count  int    `json:"count"`
	}

	byFolder := make(map[string]int)
	active := 0
	inactive := 0
	for _, summaries := range p.index.words {
		for _, s := range summaries {
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

	// Deduplicate: count unique feeds
	totalFeeds := len(p.index.feeds)

	resp := struct {
		TotalFeeds int            `json:"total_feeds"`
		ByFolder   map[string]int `json:"by_folder"`
		Active     int            `json:"active"`
		Inactive   int            `json:"inactive"`
	}{
		TotalFeeds: totalFeeds,
		ByFolder:   byFolder,
		Active:     active,
		Inactive:   inactive,
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
```

**Step 4: Register the new resources in `registerResources`**

Update `registerResources` signature to accept `*storyStore` instead of `*savedStoryIndex`. Add:

```go
registry.RegisterResource(
	protocol.Resource{
		URI:         "nebulous://stories/facets",
		Name:        "Story Facets",
		Description: "Aggregate counts of all indexed stories by year, tag, feed, and status. Read this first to understand the data shape before querying with story_query. Lightweight — no story content, just counts.",
		MimeType:    "application/json",
	},
	nil, // Handled by feedResourceProvider prefix matching
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
```

**Step 5: Run build to verify compilation**

Run: `go build ./...`
Expected: PASS

**Step 6: Commit**

```
git add internal/tools/resources.go internal/tools/story_store.go internal/tools/registry.go
git commit -m "feat(resources): wire storyStore into resources, add stories/facets and feeds/facets"
```

---

### Task 6: Remove API-coupled read tools and old index

**Files:**
- Delete: `internal/tools/stories.go`
- Delete: `internal/tools/saved_stories.go`
- Delete: `internal/tools/saved_story_index.go`
- Modify: `internal/tools/feeds.go` — remove `feed_list`, `feed_stats`, `feed_autocomplete`, `feed_search`
- Modify: `internal/tools/registry.go` — remove `registerStoryCommands`, `registerSavedStoryCommands`

**Step 1: Delete old files**

```
rm internal/tools/stories.go
rm internal/tools/saved_stories.go
rm internal/tools/saved_story_index.go
```

**Step 2: Remove API-coupled feed tools from `feeds.go`**

Remove `feed_list`, `feed_stats`, `feed_autocomplete`, `feed_search` commands from `registerFeedCommands`. Keep only `feed_query`. Remove the `output` import if no longer needed. Remove the `client *newsblur.Client` parameter from `registerFeedCommands` — it only needs `index *feedIndex` now.

**Step 3: Update `registry.go`**

Remove `registerStoryCommands(app, client)` and `registerSavedStoryCommands(app, savedIdx)` calls. Update `registerFeedCommands` call to drop the client param.

**Step 4: Remove `saved_story_index` references from `resources.go`**

Remove `readSavedStoryIndexWord`, the `saved_story_index` resource, and the `saved_story_index/{word}` template registration. Remove the `nebulous://saved_story_index/` prefix handler from `ReadResource`.

**Step 5: Run build to verify compilation**

Run: `go build ./...`
Expected: PASS (may need to chase unused import removals)

**Step 6: Run tests**

Run: `go test ./internal/tools/ -v`
Expected: PASS

**Step 7: Commit**

```
git add -u internal/tools/
git commit -m "refactor: remove API-coupled read tools and old savedStoryIndex"
```

---

### Task 7: Update server instructions and tool descriptions

**Files:**
- Modify: `cmd/nebulous/main.go:115` — update `Instructions` string
- Modify: `internal/tools/story_query_tool.go` — verify description
- Modify: `internal/tools/resources.go` — update resource descriptions

**Step 1: Update server instructions**

In `cmd/nebulous/main.go`, change the `Instructions` field to:

```go
Instructions: "NewsBlur MCP server. Read nebulous://stories/facets first to understand the data shape (years, tags, feeds, counts), then use story_query to filter by year, tag, feed, words, or any combination. Use nebulous://story/{hash} for metadata, story/{hash}/content for text, story/{hash}/original for full articles. Delegate bulk story reads to subagents. Use feed_query for feed discovery. Mutation tools (star, mark_read, subscribe, etc.) hit the NewsBlur API directly.",
```

**Step 2: Run build**

Run: `go build ./...`
Expected: PASS

**Step 3: Commit**

```
git add cmd/nebulous/main.go internal/tools/story_query_tool.go internal/tools/resources.go
git commit -m "docs: update server instructions and tool descriptions for index-first architecture"
```

---

### Task 8: Update CLAUDE.md and TODO.md

**Files:**
- Modify: `CLAUDE.md`
- Modify: `TODO.md`

**Step 1: Update CLAUDE.md**

Update the tool inventory section to reflect the new surface:
- Query tools: `story_query`, `feed_query`
- Mutation tools: (unchanged)
- Resources: add `stories/facets`, `feeds/facets`
- Remove references to `savedStoryIndex`, `starred_story_index_query`, all removed tools

Update the architecture section: `saved_story_index.go` is replaced by `story_store.go`, `story_query.go`, `story_query_tool.go`, `facets.go`.

**Step 2: Update TODO.md**

Mark completed:
- `[x] Evaluate starred story word-index + query + cache workflows`
- `[x] FDR: cache-as-persistent-index architecture`

Add new:
- `[ ] Migrate user_tags to dodder tag structure`
- `[ ] Sync non-starred stories in fetch (feed stories, unread) to enable full story_query filtering`
- `[ ] Add feed title to story facets by_feed entries (requires cross-referencing feed store)`

**Step 3: Commit**

```
git add CLAUDE.md TODO.md
git commit -m "docs: update CLAUDE.md and TODO.md for index-first architecture"
```

---

### Task 9: Smoke test — end-to-end verification

**Step 1: Build**

Run: `just build`
Expected: success

**Step 2: Verify facets resource**

Start the MCP server and read `nebulous://stories/facets`. Verify:
- `total_stories` matches expected count
- `by_year` has entries for multiple years
- `by_tag` includes "interests", "zz-nyc", "news"
- `years` is sorted descending

**Step 3: Verify story_query tool**

Test queries:
- `{year: 2024}` — returns stories, count matches `by_year["2024"]` from facets
- `{tag: "zz-nyc", year: 2024}` — returns NYC stories from 2024
- `{words: ["nix"]}` — returns nix-related stories across all years
- `{words: ["nix"], year: 2024}` — intersection
- `{offset: 0, limit: 10}` — paginated browse
- `{}` — returns first 100 stories

**Step 4: Verify story resources still work**

- `nebulous://story/{hash}` — returns compact metadata with user_tags
- `nebulous://story/{hash}/content` — returns cached text
- `nebulous://story/{hash}/original` — fetches from source

**Step 5: Verify mutation tools still work**

- `star` / `unstar` a story — should still hit API

**Step 6: Verify `feed_query` still works**

- `feed_query(words: ["hacker"])` — returns Hacker News feed
