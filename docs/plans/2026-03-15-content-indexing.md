# Saved Story Content Indexing Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Index starred story body content (not just titles) and support progressive original_text fetching, so word searches find stories by what they're about.

**Architecture:** Remove the 100-page cap from `savedStoryIndex`, strip HTML from `story_content` during build, opportunistically include cached `original_text` responses, and add a `fetch-original-text` CLI command that progressively populates the original_text cache with rate-limit-aware backoff. The cache key scheme already supports this — we just need to expose cache lookup and add the HTML stripping utility.

**Tech Stack:** Go stdlib (`strings`, `regexp`), existing `newsblur.Client` cache

**Rollback:** Purely additive — revert the commit. Index rebuild produces more words but same structure.

---

### Task 1: Add `stripHTMLTags` utility

**Files:**
- Modify: `internal/tools/feed_index.go` (add function after `isNumeric`)
- Create: `internal/tools/feed_index_test.go`

**Step 1: Write the failing test**

Create `internal/tools/feed_index_test.go`:

```go
package tools

import "testing"

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "hello world", "hello world"},
		{"simple tags", "<p>hello</p>", "hello"},
		{"nested tags", "<div><p>hello</p></div>", "hello"},
		{"attributes", `<a href="http://example.com">link</a>`, "link"},
		{"br and hr", "one<br>two<hr>three", "one two three"},
		{"amp entity", "one &amp; two", "one & two"},
		{"lt gt entities", "&lt;tag&gt;", "<tag>"},
		{"nbsp", "one&nbsp;two", "one two"},
		{"numeric entity", "&#39;quoted&#39;", "'quoted'"},
		{"hex entity", "&#x27;hex&#x27;", "'hex'"},
		{"collapsed whitespace", "one  \n\t  two", "one two"},
		{"empty", "", ""},
		{"script tag", "<script>alert('xss')</script>visible", "alert('xss') visible"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTMLTags(tt.input)
			if got != tt.want {
				t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/tools/ -run TestStripHTMLTags -v`
Expected: FAIL — `stripHTMLTags` undefined

**Step 3: Write minimal implementation**

Add to `internal/tools/feed_index.go` after `isNumeric`:

```go
import "regexp"

var (
	htmlTagRe    = regexp.MustCompile(`<[^>]*>`)
	htmlEntityRe = regexp.MustCompile(`&(?:#x?)?[a-zA-Z0-9]+;`)
	whitespaceRe = regexp.MustCompile(`\s+`)
)

var htmlEntities = map[string]string{
	"&amp;":  "&",
	"&lt;":   "<",
	"&gt;":   ">",
	"&quot;": `"`,
	"&apos;": "'",
	"&#39;":  "'",
	"&nbsp;": " ",
}

func stripHTMLTags(s string) string {
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = htmlEntityRe.ReplaceAllStringFunc(s, func(entity string) string {
		if r, ok := htmlEntities[entity]; ok {
			return r
		}
		// Numeric entities: &#123; or &#x1F;
		if len(entity) > 3 && entity[1] == '#' {
			inner := entity[2 : len(entity)-1]
			var n int64
			if inner[0] == 'x' || inner[0] == 'X' {
				fmt.Sscanf(inner[1:], "%x", &n)
			} else {
				fmt.Sscanf(inner, "%d", &n)
			}
			if n > 0 && n < 0x10FFFF {
				return string(rune(n))
			}
		}
		return entity
	})
	s = whitespaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/tools/ -run TestStripHTMLTags -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/tools/feed_index.go internal/tools/feed_index_test.go
git commit -m "feat(index): add stripHTMLTags utility for content indexing"
```

---

### Task 2: Expose cache key computation for external lookup

The `fetch-original-text` command and the index builder both need to check
whether a given original_text response is cached. Expose the cache key
computation and a `Has` method.

**Files:**
- Modify: `internal/newsblur/cache.go` (export `CacheKey`, add `Has`)
- Modify: `internal/newsblur/client.go` (add `OriginalTextCacheKey`, `HasCachedOriginalText`, `CachedOriginalText`)

**Step 1: Add `Has` and export `CacheKey` on responseCache**

In `internal/newsblur/cache.go`, add:

```go
func (c *responseCache) has(key string) bool {
	fp := filepath.Join(c.dir, key)
	info, err := os.Stat(fp)
	if err != nil {
		return false
	}
	// No TTL check — original_text content is immutable
	_ = info
	return true
}
```

**Step 2: Add client methods for original_text cache access**

In `internal/newsblur/client.go`, add:

```go
func (c *Client) OriginalTextCacheKey(storyHash string) string {
	params := url.Values{"story_hash": {storyHash}}
	return c.cache.cacheKey("/rss_feeds/original_text", params)
}

func (c *Client) HasCachedOriginalText(storyHash string) bool {
	if c.cache == nil {
		return false
	}
	return c.cache.has(c.OriginalTextCacheKey(storyHash))
}

func (c *Client) CachedOriginalText(storyHash string) (json.RawMessage, bool) {
	if c.cache == nil {
		return nil, false
	}
	key := c.OriginalTextCacheKey(storyHash)
	// Use has() instead of get() to skip TTL — original_text is immutable
	fp := filepath.Join(c.cache.dir, key)
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(data), true
}
```

**Step 3: Run build to verify compilation**

Run: `go build ./...`
Expected: success

**Step 4: Commit**

```bash
git add internal/newsblur/cache.go internal/newsblur/client.go
git commit -m "feat(cache): expose original_text cache lookup methods"
```

---

### Task 3: Fix `InvalidateStarredStoryPages` to handle unbounded pages

**Files:**
- Modify: `internal/newsblur/client.go:129-138`

**Step 1: Replace hardcoded loop with walk-until-miss**

```go
func (c *Client) InvalidateStarredStoryPages() {
	if c.cache == nil {
		return
	}
	for page := 1; ; page++ {
		params := url.Values{"page": {fmt.Sprintf("%d", page)}}
		key := c.cache.cacheKey("/reader/starred_stories", params)
		if !c.cache.has(key) {
			break
		}
		c.cache.remove(key)
	}
	c.cache.remove(c.cache.cacheKey("/reader/starred_story_hashes", nil))
}
```

**Step 2: Run build**

Run: `go build ./...`
Expected: success

**Step 3: Commit**

```bash
git add internal/newsblur/client.go
git commit -m "fix(cache): invalidate all starred story pages, not just first 100"
```

---

### Task 4: Remove page cap and index `story_content` + cached `original_text`

**Files:**
- Modify: `internal/tools/saved_story_index.go`

**Step 1: Remove `savedStoryMaxPages` constant**

Delete the constant declaration on line 21. Update the `build` loop:

```go
func (idx *savedStoryIndex) build(ctx context.Context) error {
	totalStories := 0

	for page := 1; ; page++ {
		if page > 1 {
			if err := sleepCtx(ctx, savedStoryPageDelay); err != nil {
				return err
			}
		}

		raw, err := idx.fetchPageWithRetry(ctx, page)
		if err != nil {
			return fmt.Errorf("page %d (%d stories indexed so far): %w", page, totalStories, err)
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
		}

		totalStories += len(resp.Stories)
	}

	log.Printf("saved story index: indexed %d stories, %d words", totalStories, len(idx.words))
	return nil
}
```

**Step 2: Extract `indexStory` method**

```go
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
```

**Step 3: Run build**

Run: `go build ./...`
Expected: success

**Step 4: Commit**

```bash
git add internal/tools/saved_story_index.go
git commit -m "feat(index): remove page cap, index story_content and cached original_text"
```

---

### Task 5: Add `fetch-original-text` CLI command

**Files:**
- Modify: `cmd/nebulous/main.go`

**Step 1: Add the command handler**

Insert before the `token := os.Getenv(...)` line (after the `install-mcp` block):

```go
if flag.NArg() >= 1 && flag.Arg(0) == "fetch-original-text" {
	token := os.Getenv("NEWSBLUR_TOKEN")
	if token == "" {
		log.Fatal("NEWSBLUR_TOKEN environment variable is required")
	}

	client := newsblur.NewClient(token)
	if home, err := os.UserHomeDir(); err == nil {
		cacheDir := filepath.Join(home, ".cache", "nebulous", "responses")
		client.WithCache(cacheDir, 1*time.Hour)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := fetchOriginalText(ctx, client); err != nil {
		log.Fatalf("fetch-original-text: %v", err)
	}
	return
}
```

**Step 2: Implement `fetchOriginalText` function**

Add to `cmd/nebulous/main.go`:

```go
func fetchOriginalText(ctx context.Context, client *newsblur.Client) error {
	log.Println("fetching starred story hashes...")
	raw, err := client.StarredStoryHashes(ctx)
	if err != nil {
		return fmt.Errorf("fetching hashes: %w", err)
	}

	hashes, err := parseStarredHashes(raw)
	if err != nil {
		return fmt.Errorf("parsing hashes: %w", err)
	}

	var missing []string
	for _, h := range hashes {
		if !client.HasCachedOriginalText(h) {
			missing = append(missing, h)
		}
	}

	log.Printf("total: %d, cached: %d, missing: %d", len(hashes), len(hashes)-len(missing), len(missing))

	if len(missing) == 0 {
		log.Println("all original text already cached")
		return nil
	}

	backoff := 1 * time.Second
	maxBackoff := 5 * time.Minute
	fetched := 0

	for _, hash := range missing {
		select {
		case <-ctx.Done():
			log.Printf("interrupted after fetching %d/%d", fetched, len(missing))
			return ctx.Err()
		default:
		}

		_, err := client.OriginalText(ctx, hash)
		if err != nil {
			var rle *newsblur.RateLimitError
			if errors.As(err, &rle) {
				wait := backoff
				if rle.RetryAfter > 0 {
					wait = rle.RetryAfter
				}
				log.Printf("rate limited at %d/%d, backing off %s", fetched, len(missing), wait)

				t := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					t.Stop()
					return ctx.Err()
				case <-t.C:
				}

				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}
			log.Printf("error fetching %s: %v (skipping)", hash, err)
			continue
		}

		fetched++
		backoff = 1 * time.Second // reset on success

		if fetched%100 == 0 {
			log.Printf("fetched %d/%d", fetched, len(missing))
		}
	}

	log.Printf("done: fetched %d/%d", fetched, len(missing))
	return nil
}

func parseStarredHashes(raw json.RawMessage) ([]string, error) {
	// Try flat array: ["hash1", "hash2"]
	var flat []string
	if err := json.Unmarshal(raw, &flat); err == nil {
		return flat, nil
	}

	// Try feed-grouped: {"123": [["hash1", "ts1"], ...]}
	var byFeed map[string][][2]string
	if err := json.Unmarshal(raw, &byFeed); err == nil {
		var hashes []string
		for _, pairs := range byFeed {
			for _, pair := range pairs {
				hashes = append(hashes, pair[0])
			}
		}
		return hashes, nil
	}

	return nil, fmt.Errorf("unrecognized starred_story_hashes format")
}
```

**Step 3: Add missing import**

Add `"errors"` to the import block in `main.go`.

**Step 4: Update `flag.Usage` to document the new command**

```go
fmt.Fprintf(os.Stderr, "Usage:\n")
fmt.Fprintf(os.Stderr, "  nebulous [flags]             Start MCP server\n")
fmt.Fprintf(os.Stderr, "  nebulous generate-plugin      Generate plugin.json\n")
fmt.Fprintf(os.Stderr, "  nebulous hook                 Handle purse-first hooks\n")
fmt.Fprintf(os.Stderr, "  nebulous install-mcp          Install MCP server config\n")
fmt.Fprintf(os.Stderr, "  nebulous fetch-original-text  Progressively cache original article text\n\n")
```

**Step 5: Run build**

Run: `go build ./...`
Expected: success

**Step 6: Commit**

```bash
git add cmd/nebulous/main.go
git commit -m "feat: add fetch-original-text CLI command for progressive content caching"
```

---

### Task 6: Update tool description and TODO.md

**Files:**
- Modify: `internal/tools/saved_stories.go:23`
- Modify: `TODO.md`

**Step 1: Update tool description**

Change the `Short` description in `saved_stories.go`:

```go
Short: "Search saved/starred stories by word. Returns OR-union of matching story summaries from the title and content index. Lightweight entry point for saved story discovery — use this before fetching full story content.",
```

**Step 2: Update TODO.md**

Mark the content indexing item as done, add new TODOs:

```markdown
- [x] Index story_content (HTML stripped) in saved_story_index for deeper content search
```

Add new items:

```markdown
- [ ] Move nebulous cache/index from ~/.cache/nebulous to ~/.local/share/nebulous (XDG_DATA_HOME) — it's an index, not a throwaway cache
- [ ] Switch blob-storage portion of index to madder (github:amarbel-llc/dodder)
```

**Step 3: Commit**

```bash
git add internal/tools/saved_stories.go TODO.md
git commit -m "docs: update tool description for content search, add XDG and madder TODOs"
```

---

### Task 7: Smoke test — build and verify index lifecycle

**Step 1: Build**

Run: `just build`
Expected: success

**Step 2: Test fetch-original-text command**

Run: `NEWSBLUR_TOKEN=$(cat .secrets.env | grep NEWSBLUR_TOKEN | cut -d= -f2) ./build/debug/nebulous fetch-original-text`

Let it run for a few seconds, then ctrl+C. Verify:
- It reports total/cached/missing counts
- It fetches some stories before stopping
- Running again shows fewer missing stories (progress is durable)

**Step 3: Test index query via MCP**

Use the `saved_story_query` MCP tool to search for a word that appears in
story content but not titles. Compare results before and after the changes.
