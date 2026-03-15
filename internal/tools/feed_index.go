package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/friedenberg/nebulous/internal/newsblur"
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

type feedIndex struct {
	client *newsblur.Client
	once   sync.Once
	words  map[string][]feedSummary
	feeds  map[string]json.RawMessage
	err    error
}

func newFeedIndex(client *newsblur.Client) *feedIndex {
	return &feedIndex{
		client: client,
	}
}

func (idx *feedIndex) ensureBuilt(ctx context.Context) error {
	idx.once.Do(func() {
		idx.words = make(map[string][]feedSummary)
		idx.feeds = make(map[string]json.RawMessage)
		idx.err = idx.build(ctx)
	})
	return idx.err
}

func (idx *feedIndex) build(ctx context.Context) error {
	raw, err := idx.client.Feeds(ctx, false, true, false)
	if err != nil {
		return err
	}

	var resp struct {
		Feeds   map[string]json.RawMessage `json:"feeds"`
		Folders []json.RawMessage          `json:"flat_folders_with_feeds"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
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
		idx.feeds[idStr] = feedRaw

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
					idx.words[word] = append(idx.words[word], summary)
				}
			}
		}
	}

	return nil
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
