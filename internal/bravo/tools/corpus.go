package tools

import (
	"encoding/json"
	"fmt"
	"io"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
)

const minContentLen = 200

// CorpusList writes one starred story hash per line to w, skipping hashes
// whose story data is not cached locally. If limit > 0, stops after emitting
// that many keys. Designed for maneater's command corpus list-cmd contract.
func CorpusList(client *newsblur.Client, w io.Writer, limit int) error {
	raw, ok := client.CachedStarredStoryHashes()
	if !ok {
		return fmt.Errorf("no cached hash manifest, run 'nebulous fetch' first")
	}

	hashes, err := newsblur.ParseStarredHashes(raw)
	if err != nil {
		return err
	}

	n := 0
	for _, h := range hashes {
		if limit > 0 && n >= limit {
			break
		}
		if !client.HasCachedStarredStory(h) {
			continue
		}
		fmt.Fprintln(w, h)
		n++
	}

	return nil
}

// CorpusRead writes the plain-text content of a single starred story to w.
// Returns nil with no output for missing or stub stories (< 200 chars),
// which causes maneater to skip the document. Designed for maneater's
// command corpus read-cmd contract.
func CorpusRead(client *newsblur.Client, key string, w io.Writer) error {
	storyRaw, ok := client.CachedStarredStory(key)
	if !ok {
		return nil
	}

	var story struct {
		Title   string `json:"story_title"`
		Authors string `json:"story_authors"`
		Content string `json:"story_content"`
	}
	if err := json.Unmarshal(storyRaw, &story); err != nil {
		return nil
	}

	stripped := stripHTMLTags(story.Content)

	// Try original text if story content is a stub.
	var originalStripped string
	if otRaw, ok := client.CachedOriginalText(key); ok {
		var ot struct {
			OriginalText string `json:"original_text"`
		}
		if json.Unmarshal(otRaw, &ot) == nil && ot.OriginalText != "" {
			originalStripped = stripHTMLTags(ot.OriginalText)
		}
	}

	// Use whichever source has more content.
	body := stripped
	if len(originalStripped) > len(stripped) {
		body = originalStripped
	}

	if len(body) < minContentLen {
		return nil
	}

	header := story.Title
	if story.Authors != "" {
		header += " by " + story.Authors
	}

	fmt.Fprintln(w, header)
	fmt.Fprintln(w, body)

	return nil
}
