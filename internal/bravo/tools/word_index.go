package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"sort"
	"time"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
	"code.linenisgreat.com/nebulous/internal/alfa/wordindex"
)

// wordTokenizerVersion names the tokenization (extractWords, stripHTMLTags,
// the stop-word list, which text is indexed) that produced an on-disk index.
// Bump it whenever any of those change: Open then rejects older files and
// readers fall back to the in-memory build instead of silently disagreeing
// with it.
const wordTokenizerVersion = "extractwords-v1"

// corpusDigest identifies the set of starred stories an index covers.
func corpusDigest(hashes []string) string {
	sorted := append([]string(nil), hashes...)
	sort.Strings(sorted)
	h := sha256.New()
	for _, s := range sorted {
		h.Write([]byte(s))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// BuildWordIndex reads every cached starred story (and its original text),
// tokenizes it exactly as the in-memory word index does, and writes the
// result to path through wordindex's temp-file-and-rename swap. This is the
// fetch-side half of nebulous#59; only the finished posting lists stay in
// memory, one story's text at a time otherwise.
func BuildWordIndex(ctx context.Context, client *newsblur.Client, path string) (wordindex.Meta, error) {
	client.ForceManifestRefresh()
	raw, ok := client.CachedStarredStoryHashes()
	if !ok {
		return wordindex.Meta{}, errors.New("no cached starred story hash manifest, run 'nebulous fetch' first")
	}
	hashes, err := newsblur.ParseStarredHashes(raw)
	if err != nil {
		return wordindex.Meta{}, err
	}

	start := time.Now()
	b := wordindex.NewBuilder()
	for i, hash := range hashes {
		if i%1000 == 0 {
			if err := ctx.Err(); err != nil {
				return wordindex.Meta{}, err
			}
		}
		storyRaw, ok := client.CachedStarredStory(hash)
		if !ok {
			continue
		}
		rec, err := parseStoryRecordOpts(storyRaw, client, true)
		if err != nil {
			continue
		}
		words := make([]string, 0, len(rec.Words))
		for w := range rec.Words {
			words = append(words, w)
		}
		b.Add(hash, words)
	}

	meta := wordindex.Meta{Tokenizer: wordTokenizerVersion, CorpusDigest: corpusDigest(hashes)}
	if err := b.WriteFile(path, meta); err != nil {
		return wordindex.Meta{}, err
	}
	meta.Stories, meta.Words = b.Stories(), b.Words()
	log.Printf("word index: wrote %s: %d stories, %d words in %s",
		path, meta.Stories, meta.Words, time.Since(start).Round(time.Millisecond))
	return meta, nil
}

// WordIndexCurrent reports whether the index file at path exists, is
// readable by this build, and covers exactly the corpus described by
// hashes. It says nothing about whether stories' original text arrived
// after the build; callers that just fetched text rebuild regardless.
func WordIndexCurrent(path string, hashes []string) bool {
	ix, err := wordindex.Open(path, wordTokenizerVersion)
	if err != nil {
		return false
	}
	defer ix.Close()
	return ix.Meta().CorpusDigest == corpusDigest(hashes)
}

// openWordIndex opens the on-disk index for the corpus described by hashes.
// A nil index means "use the in-memory build"; note is a human-readable
// reason or caveat (empty when the index is current) that story_query
// surfaces to its caller.
func openWordIndex(path string, hashes []string) (ix *wordindex.Index, note string) {
	if path == "" {
		return nil, ""
	}
	ix, err := wordindex.Open(path, wordTokenizerVersion)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("word index: %s not present, building in memory (run 'nebulous fetch' or 'nebulous index')", path)
		} else {
			log.Printf("word index: %s unusable, building in memory: %v", path, err)
		}
		return nil, ""
	}
	meta := ix.Meta()
	if meta.CorpusDigest != corpusDigest(hashes) {
		note = fmt.Sprintf(
			"word index is stale: built %s covering %d stories, corpus now has %d; stories added since will not match word searches until the next fetch rebuilds it",
			meta.BuiltAt.Format(time.RFC3339), meta.Stories, len(hashes))
		log.Printf("word index: %s", note)
	}
	return ix, note
}

// closeIndexWhenUnreachable closes ix once snap is garbage collected, so a
// query still holding a replaced snapshot never sees a closed index.
func closeIndexWhenUnreachable(snap *storyStoreSnapshot) {
	if snap.index == nil {
		return
	}
	runtime.SetFinalizer(snap, func(s *storyStoreSnapshot) {
		if err := s.index.Close(); err != nil {
			log.Printf("word index: closing replaced index: %v", err)
		}
	})
}

// diskCandidates resolves word hits against the on-disk index, de-duplicating
// stories across words and dropping hashes no longer in the corpus.
func (snap *storyStoreSnapshot) diskCandidates(words []string) ([]*storyRecord, error) {
	var out []*storyRecord
	seen := make(map[string]bool)
	for _, word := range words {
		hashes, err := snap.index.Lookup(word)
		if err != nil {
			return nil, fmt.Errorf("word index lookup %q: %w", word, err)
		}
		for _, h := range hashes {
			rec, ok := snap.byHash[h]
			if !ok || seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, rec)
		}
	}
	return out, nil
}
