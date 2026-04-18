package newsblur

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/friedenberg/nebulous/internal/blobstore"
)

// responseCache maps logical keys (SHA256 of URL+params) to content blobs via
// a persistent Manifest. The actual bytes live in a pluggable blobstore.Store
// (filesystem by default, or an external command per amarbel-llc/maneater#8).
type responseCache struct {
	manifest *blobstore.Manifest
	store    blobstore.Store
	ttl      time.Duration
}

func newResponseCache(dir string, ttl time.Duration, store blobstore.Store) (*responseCache, error) {
	if dir == "" {
		return nil, fmt.Errorf("cache: dir must not be empty")
	}
	m, err := blobstore.NewManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("cache: load manifest: %w", err)
	}
	if store == nil {
		store = blobstore.NewFilesystemStore(dir)
	}
	return &responseCache{manifest: m, store: store, ttl: ttl}, nil
}

func (c *responseCache) cacheKey(path string, params url.Values) string {
	full := path
	if len(params) > 0 {
		full += "?" + params.Encode()
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(full)))
}

func (c *responseCache) get(key string) (json.RawMessage, bool) {
	entry, ok := c.manifest.Lookup(key)
	if !ok {
		return nil, false
	}
	if !entry.Immutable && time.Since(entry.WrittenAt) > c.ttl {
		return nil, false
	}
	return c.readBlob(entry.Digest)
}

// getNoTTL reads a cached value regardless of age. Used for immutable content
// (individual stories, original text) and manifests that are explicitly
// refreshed by the fetch command rather than expiring on a timer.
func (c *responseCache) getNoTTL(key string) (json.RawMessage, bool) {
	entry, ok := c.manifest.Lookup(key)
	if !ok {
		return nil, false
	}
	return c.readBlob(entry.Digest)
}

func (c *responseCache) readBlob(digest string) (json.RawMessage, bool) {
	var buf bytes.Buffer
	ok, err := c.store.Read(context.Background(), digest, &buf)
	if err != nil || !ok {
		return nil, false
	}
	return json.RawMessage(buf.Bytes()), true
}

func (c *responseCache) remove(key string) {
	_ = c.manifest.Delete(key)
}

func (c *responseCache) has(key string) bool {
	_, ok := c.manifest.Lookup(key)
	return ok
}

func (c *responseCache) put(key string, body json.RawMessage) error {
	return c.write(key, body, false)
}

// putImmutable writes an entry that bypasses TTL checks. Used for starred
// story content, original article text, and the starred-hash manifest.
func (c *responseCache) putImmutable(key string, body json.RawMessage) error {
	return c.write(key, body, true)
}

func (c *responseCache) write(key string, body json.RawMessage, immutable bool) error {
	digest, err := c.store.Write(context.Background(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cache write: %w", err)
	}
	return c.manifest.Record(key, blobstore.ManifestEntry{
		Digest:    digest,
		WrittenAt: time.Now(),
		Immutable: immutable,
	})
}
