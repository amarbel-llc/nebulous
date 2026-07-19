package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
	"golang.org/x/text/unicode/norm"
)

type feedSummary struct {
	ID     json.Number `json:"id"`
	Title  string      `json:"title"`
	Folder string      `json:"folder,omitempty"`
	NT     int         `json:"nt"`
	NG     int         `json:"ng"`
	PS     int         `json:"ps"`
	Active bool        `json:"active"`
}

// feedIndexSnapshot is the immutable result of one build pass — see
// storyStoreSnapshot's doc comment for why this replaced a plain
// sync.Once build.
type feedIndexSnapshot struct {
	words     map[string][]feedSummary
	feeds     map[string]json.RawMessage
	summaries map[string]feedSummary
}

type feedIndex struct {
	client *newsblur.Client
	cache  staleCache[feedIndexSnapshot]
}

func newFeedIndex(client *newsblur.Client) *feedIndex {
	return &feedIndex{
		client: client,
		cache:  newStaleCache[feedIndexSnapshot](client.ManifestPath()),
	}
}

// current returns the up-to-date snapshot, rebuilding when the manifest
// changed since the last check — see staleCache's doc comment for why
// this replaced a plain sync.Once build.
func (idx *feedIndex) current(ctx context.Context) (*feedIndexSnapshot, error) {
	return idx.cache.current(func() (*feedIndexSnapshot, error) {
		return idx.build(ctx)
	})
}

// ensureBuilt preserves the old call-site contract (callers only check
// the error); the snapshot itself is fetched via current().
func (idx *feedIndex) ensureBuilt(ctx context.Context) error {
	_, err := idx.current(ctx)
	return err
}

func (idx *feedIndex) build(ctx context.Context) (*feedIndexSnapshot, error) {
	// idx.cache already decided the manifest file changed; force the
	// client's own manifest past its independent debounce window too, so
	// the Feeds read below can't still be gated by it and bake pre-write
	// data into this rebuild (see staleCache's doc comment).
	idx.client.ForceManifestRefresh()

	snap := &feedIndexSnapshot{
		words:     make(map[string][]feedSummary),
		feeds:     make(map[string]json.RawMessage),
		summaries: make(map[string]feedSummary),
	}

	raw, err := idx.client.Feeds(ctx, false, true, false)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Feeds   map[string]json.RawMessage `json:"feeds"`
		Folders []json.RawMessage          `json:"flat_folders_with_feeds"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}

	// Build folder lookup: feed_id -> folder name
	folderLookup := make(map[string]string)
	for _, folderRaw := range resp.Folders {
		var folderMap map[string][]json.Number
		if err := json.Unmarshal(folderRaw, &folderMap); err != nil {
			continue
		}
		for folderName, feedIDs := range folderMap {
			for _, id := range feedIDs {
				folderLookup[id.String()] = folderName
			}
		}
	}

	for idStr, feedRaw := range resp.Feeds {
		snap.feeds[idStr] = feedRaw

		var feed struct {
			ID       json.Number `json:"id"`
			Title    string      `json:"feed_title"`
			Link     string      `json:"feed_link"`
			NT       int         `json:"nt"`
			NG       int         `json:"ng"`
			PS       int         `json:"ps"`
			Active   bool        `json:"active"`
			Disabled bool        `json:"disabled"`
		}
		if err := json.Unmarshal(feedRaw, &feed); err != nil {
			continue
		}

		folder := folderLookup[idStr]

		summary := feedSummary{
			ID:     feed.ID,
			Title:  feed.Title,
			Folder: folder,
			NT:     feed.NT,
			NG:     feed.NG,
			PS:     feed.PS,
			Active: feed.Active && !feed.Disabled,
		}
		snap.summaries[idStr] = summary

		var sources []string
		sources = append(sources, feed.Title)
		if folder != "" {
			sources = append(sources, folder)
		}
		if feed.Link != "" {
			if u, err := url.Parse(feed.Link); err == nil {
				sources = append(sources, u.Hostname())
			}
		}

		seen := make(map[string]bool)
		for _, src := range sources {
			for _, word := range extractWords(src) {
				if !seen[word] {
					seen[word] = true
					snap.words[word] = append(snap.words[word], summary)
				}
			}
		}
	}

	// Persisted so a feed that later drops out of /reader/feeds
	// (unsubscribe) keeps its title resolvable from stories starred while
	// it was subscribed (nebulous#49). Derived from snap.summaries — built
	// above from the same response — rather than accumulated separately.
	// This is the only place client.Feeds's response gets persisted this
	// way; a future direct caller of client.Feeds bypassing feedIndex
	// would need its own call to PutSeenFeedTitles.
	titles := make(map[string]string, len(snap.summaries))
	for id, s := range snap.summaries {
		if s.Title != "" {
			titles[id] = s.Title
		}
	}
	if err := idx.client.PutSeenFeedTitles(titles); err != nil {
		log.Printf("feed index: recording seen feed titles: %v (continuing)", err)
	}

	return snap, nil
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true,
	"from": true, "that": true, "this": true, "are": true,
	"was": true, "but": true, "not": true, "you": true,
	"all": true, "can": true, "had": true, "has": true,
	"have": true, "its": true, "our": true, "will": true,
	"www": true, "com": true, "org": true, "net": true,
	"http": true, "https": true,
}

func extractWords(s string) []string {
	s = stripDiacritics(s)
	s = strings.ToLower(s)

	// Split on whitespace, punctuation, etc. but keep hyphens within words
	tokens := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-'
	})

	var words []string
	for _, tok := range tokens {
		tok = strings.Trim(tok, "-")
		if tok == "" {
			continue
		}
		if len(tok) < 3 {
			continue
		}
		if isNumeric(tok) {
			continue
		}
		if stopWords[tok] {
			continue
		}
		words = append(words, tok)

		// Compound nouns: "machine-learning" -> also index "machine", "learning"
		if strings.Contains(tok, "-") {
			parts := strings.Split(tok, "-")
			for _, part := range parts {
				if len(part) < 3 || isNumeric(part) || stopWords[part] {
					continue
				}
				words = append(words, part)
			}
		}
	}
	return words
}

func stripDiacritics(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFD.String(s) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isNumeric(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

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
