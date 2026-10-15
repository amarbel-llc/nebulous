// Package wordindex is the on-disk inverted index (word to story hashes)
// that `nebulous fetch` writes and the serve processes open read-only,
// replacing the resident in-memory word map (nebulous#59).
//
// The file is a bbolt database with three buckets: "meta" (one JSON
// record), "stories" (ordinal to story hash) and "words" (word to a
// delta-varint list of ordinals). It is built whole into a temp file and
// swapped in by rename, so a reader holding the old file keeps a consistent
// view and no cross-process locking is needed. The package knows nothing
// about tokenizing; the caller supplies words and records which tokenizer
// produced them in Meta.Tokenizer.
package wordindex

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// FormatVersion changes whenever the file layout does; Open refuses any
// other value so a reader never misparses a file from a different build.
const FormatVersion = 1

// indexFileMode is the permission of a finished index file. It is applied
// with Chmod, so the process umask does not narrow it. The file holds only
// words and story hashes.
const indexFileMode = 0o644

var (
	bucketMeta    = []byte("meta")
	bucketWords   = []byte("words")
	bucketStories = []byte("stories")
	keyMeta       = []byte("meta")
)

// ErrIncompatible is returned by Open for a file whose format version or
// tokenizer does not match what the caller expects.
var ErrIncompatible = errors.New("wordindex: incompatible index file")

// Meta describes how and from what an index file was built.
type Meta struct {
	Format    int    `json:"format"`
	Tokenizer string `json:"tokenizer"`
	// CorpusDigest identifies the set of stories the index covers, so a
	// reader can tell whether the corpus has moved on since the build.
	CorpusDigest string    `json:"corpus_digest"`
	Stories      int       `json:"stories"`
	Words        int       `json:"words"`
	BuiltAt      time.Time `json:"built_at"`
}

// Builder accumulates postings in memory (about 4 bytes per posting) and
// writes them out once with WriteFile.
type Builder struct {
	hashes   []string
	postings map[string][]uint32
	skipped  int
}

func NewBuilder() *Builder {
	return &Builder{postings: make(map[string][]uint32)}
}

// Add records one story and its distinct words. Words that repeat within
// one call are counted once. The word strings are copied on first sight, so
// a caller passing substrings of a large body does not pin that body.
func (b *Builder) Add(hash string, words []string) {
	ord := uint32(len(b.hashes))
	b.hashes = append(b.hashes, hash)
	for _, w := range words {
		// bbolt rejects keys over MaxKeySize, and one junk token (a base64
		// blob, a long run of digits) must never fail the whole index.
		if len(w) > bolt.MaxKeySize {
			b.skipped++
			continue
		}
		list, seen := b.postings[w]
		if !seen {
			w = strings.Clone(w)
		} else if list[len(list)-1] == ord {
			continue
		}
		b.postings[w] = append(list, ord)
	}
}

// Skipped reports how many words Add dropped for being too long to key.
func (b *Builder) Skipped() int { return b.skipped }

func (b *Builder) Stories() int { return len(b.hashes) }
func (b *Builder) Words() int   { return len(b.postings) }

const writeBatch = 20000

// WriteFile writes the index to a temp file beside path, syncs it and
// renames it over path. meta.Format, Stories, Words and BuiltAt are filled
// in here. On any error the temp file is removed and path is untouched.
func (b *Builder) WriteFile(path string, meta Meta) (err error) {
	meta.Format = FormatVersion
	meta.Stories = len(b.hashes)
	meta.Words = len(b.postings)
	if meta.BuiltAt.IsZero() {
		meta.BuiltAt = time.Now().UTC()
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o775); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// CreateTemp makes the file 0600 and bolt.Open's mode only applies when
	// it creates a file, so widen it explicitly: other service users (the
	// serve processes, in the fetch user's group) must be able to read the
	// finished index.
	if err := tmp.Chmod(indexFileMode); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	db, err := bolt.Open(tmpPath, indexFileMode, &bolt.Options{NoSync: true})
	if err != nil {
		return err
	}
	if err = b.fill(db, meta); err != nil {
		_ = db.Close()
		return err
	}
	if err = db.Sync(); err != nil {
		_ = db.Close()
		return err
	}
	if err = db.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return err
	}
	return syncDir(dir)
}

func (b *Builder) fill(db *bolt.DB, meta Meta) error {
	if err := db.Update(func(tx *bolt.Tx) error {
		stories, err := tx.CreateBucket(bucketStories)
		if err != nil {
			return err
		}
		stories.FillPercent = 1.0
		for ord, h := range b.hashes {
			if err := stories.Put(ordKey(uint32(ord)), []byte(h)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	words := make([]string, 0, len(b.postings))
	for w := range b.postings {
		words = append(words, w)
	}
	sort.Strings(words)

	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucket(bucketWords)
		return err
	}); err != nil {
		return err
	}
	for start := 0; start < len(words); start += writeBatch {
		end := min(start+writeBatch, len(words))
		if err := db.Update(func(tx *bolt.Tx) error {
			bk := tx.Bucket(bucketWords)
			bk.FillPercent = 1.0 // keys arrive in sorted order
			for _, w := range words[start:end] {
				if err := bk.Put([]byte(w), encodePostings(b.postings[w])); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	return db.Update(func(tx *bolt.Tx) error {
		mb, err := tx.CreateBucket(bucketMeta)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		return mb.Put(keyMeta, raw)
	})
}

// Index is an open, read-only index file. Safe for concurrent use.
type Index struct {
	db   *bolt.DB
	meta Meta
}

// Open opens path read-only. It returns ErrIncompatible (wrapped) when the
// file's format version differs from FormatVersion or its tokenizer differs
// from wantTokenizer; any other error means the file is missing or unreadable.
func Open(path, wantTokenizer string) (*Index, error) {
	db, err := bolt.Open(path, 0, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	var meta Meta
	err = db.View(func(tx *bolt.Tx) error {
		mb := tx.Bucket(bucketMeta)
		if mb == nil || tx.Bucket(bucketWords) == nil || tx.Bucket(bucketStories) == nil {
			return fmt.Errorf("%w: missing bucket", ErrIncompatible)
		}
		raw := mb.Get(keyMeta)
		if raw == nil {
			return fmt.Errorf("%w: missing meta", ErrIncompatible)
		}
		return json.Unmarshal(raw, &meta)
	})
	if err == nil && meta.Format != FormatVersion {
		err = fmt.Errorf("%w: format %d, want %d", ErrIncompatible, meta.Format, FormatVersion)
	}
	if err == nil && meta.Tokenizer != wantTokenizer {
		err = fmt.Errorf("%w: tokenizer %q, want %q", ErrIncompatible, meta.Tokenizer, wantTokenizer)
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Index{db: db, meta: meta}, nil
}

func (ix *Index) Meta() Meta   { return ix.meta }
func (ix *Index) Close() error { return ix.db.Close() }

// Lookup returns the hashes of the stories containing word, in index
// (insertion) order. An unknown word yields no hashes and no error.
func (ix *Index) Lookup(word string) ([]string, error) {
	var out []string
	err := ix.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketWords).Get([]byte(word))
		if raw == nil {
			return nil
		}
		ords, err := decodePostings(raw)
		if err != nil {
			return err
		}
		stories := tx.Bucket(bucketStories)
		out = make([]string, 0, len(ords))
		for _, o := range ords {
			h := stories.Get(ordKey(o))
			if h == nil {
				return fmt.Errorf("wordindex: posting references missing story ordinal %d", o)
			}
			out = append(out, string(h))
		}
		return nil
	})
	return out, err
}

func ordKey(o uint32) []byte {
	var k [4]byte
	binary.BigEndian.PutUint32(k[:], o)
	return k[:]
}

// encodePostings delta-encodes a strictly ascending ordinal list as uvarints.
func encodePostings(ords []uint32) []byte {
	buf := make([]byte, 0, len(ords)*2)
	prev := uint32(0)
	for i, o := range ords {
		d := o
		if i > 0 {
			d = o - prev
		}
		buf = binary.AppendUvarint(buf, uint64(d))
		prev = o
	}
	return buf
}

func decodePostings(raw []byte) ([]uint32, error) {
	var out []uint32
	prev := uint32(0)
	for i := 0; len(raw) > 0; i++ {
		d, n := binary.Uvarint(raw)
		if n <= 0 || d > uint64(^uint32(0)) {
			return nil, errors.New("wordindex: corrupt posting list")
		}
		raw = raw[n:]
		o := uint32(d)
		if i > 0 {
			o += prev
		}
		out = append(out, o)
		prev = o
	}
	return out, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
