package tools

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
	"code.linenisgreat.com/nebulous/internal/alfa/wordindex"
)

// ErrWordIndexUnavailable means no usable on-disk word index exists (not
// built yet, unreadable, or from another tokenizer). The cutting-garden
// plugin turns it into "this plugin cannot answer a words search right now".
var ErrWordIndexUnavailable = errors.New("nebulous: word index unavailable (run 'nebulous fetch' or 'nebulous index')")

// ErrNotIndexedWord marks a words-search query that is not exactly one
// indexed word: a caller mistake, which the cutting-garden plugin reports as
// a bad request rather than an internal error.
var ErrNotIndexedWord = errors.New("not a single indexed word")

// IndexedWord canonicalizes a words-search query to the single indexed word
// it denotes: the same case and accent folding the index applies, with the
// same rules about what is indexed. A query that is not exactly one indexed
// word (several words, a phrase, text under three characters, a number, a
// stop word) is an error rather than a silent empty result, because such a
// word can never match anything in the index.
func IndexedWord(query string) (string, error) {
	canonical := strings.Trim(strings.ToLower(stripDiacritics(strings.TrimSpace(query))), "-")
	words := extractWords(canonical)
	if len(words) == 0 || words[0] != canonical || len(canonical) > maxIndexedWordBytes {
		return "", fmt.Errorf(
			"%q is %w: nebulous indexes lowercase, accent-folded words of 3 to %d letters or digits (hyphens allowed inside a word), and skips numbers and common stop words",
			query, ErrNotIndexedWord, maxIndexedWordBytes)
	}
	return canonical, nil
}

// wordIndexHolder keeps the on-disk word index open for a long-lived
// process and reopens it when fetch swaps a new file in by rename.
type wordIndexHolder struct {
	path string

	mu    sync.RWMutex
	ix    *wordindex.Index
	stamp string
}

func newWordIndexHolder(manifestPath string) *wordIndexHolder {
	return &wordIndexHolder{path: newsblur.WordIndexPath(manifestPath)}
}

// refresh makes h.ix match the file currently at h.path.
func (h *wordIndexHolder) refresh() error {
	if h.path == "" {
		return ErrWordIndexUnavailable
	}
	fi, err := os.Stat(h.path)
	if err != nil {
		h.mu.Lock()
		h.closeLocked()
		h.mu.Unlock()
		return ErrWordIndexUnavailable
	}
	stamp := strconv.FormatInt(fi.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(fi.Size(), 10)

	h.mu.RLock()
	current := h.ix != nil && h.stamp == stamp
	h.mu.RUnlock()
	if current {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ix != nil && h.stamp == stamp {
		return nil
	}
	h.closeLocked()
	ix, err := wordindex.Open(h.path, wordTokenizerVersion)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWordIndexUnavailable, err)
	}
	h.ix, h.stamp = ix, stamp
	return nil
}

func (h *wordIndexHolder) closeLocked() {
	if h.ix != nil {
		_ = h.ix.Close()
		h.ix, h.stamp = nil, ""
	}
}

func (h *wordIndexHolder) lookup(word string) ([]string, wordindex.Meta, error) {
	if err := h.refresh(); err != nil {
		return nil, wordindex.Meta{}, err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.ix == nil { // closed by a concurrent refresh after the check above
		return nil, wordindex.Meta{}, ErrWordIndexUnavailable
	}
	hashes, err := h.ix.Lookup(word)
	return hashes, h.ix.Meta(), err
}

// SearchWords returns the indexed stories containing word (a whole word,
// canonicalized with IndexedWord), read from the on-disk index. complete is
// false when the index no longer covers exactly the stories now in the
// corpus (fetch added stories after the last index build), in which case the
// hits are correct but the list may be missing newer stories. Stories the
// index knows but the corpus no longer has are dropped.
func (r *ReadIndex) SearchWords(word string) (hits []StoryRef, complete bool, err error) {
	canonical, err := IndexedWord(word)
	if err != nil {
		return nil, false, err
	}
	hashes, meta, err := r.wordIndex.lookup(canonical)
	if err != nil {
		return nil, false, err
	}
	snap, err := r.stories.current()
	if err != nil {
		return nil, false, err
	}
	for _, h := range hashes {
		if rec, ok := snap.byHash[h]; ok {
			hits = append(hits, storyRefOf(rec))
		}
	}
	return hits, meta.CorpusDigest == snap.digest, nil
}
