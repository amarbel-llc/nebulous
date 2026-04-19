package newsblur

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/friedenberg/nebulous/internal/0/manifest"
)

// BlobSink is the operational surface nebulous needs from its blob backend.
// Implemented by *madder.Store in production and by test fakes in unit tests.
type BlobSink interface {
	Read(id string, dst io.Writer) (bool, error)
	Write(src io.Reader) (id string, err error)
}

// responseCache maps logical keys (SHA256 of URL+params) to content blobs
// through a persistent Manifest. Blob bytes live in a BlobSink; the manifest
// lives at a caller-provided path (nebulous-owned meta directory).
type responseCache struct {
	manifest *manifest.Manifest
	sink     BlobSink
	ttl      time.Duration
}

func newResponseCache(manifestPath string, ttl time.Duration, sink BlobSink) (*responseCache, error) {
	if manifestPath == "" {
		return nil, fmt.Errorf("cache: manifest path must not be empty")
	}
	if sink == nil {
		return nil, fmt.Errorf("cache: blob sink must not be nil")
	}
	m, err := manifest.NewManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("cache: load manifest: %w", err)
	}
	return &responseCache{manifest: m, sink: sink, ttl: ttl}, nil
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
	ok, err := c.sink.Read(digest, &buf)
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
	digest, err := c.sink.Write(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cache write: %w", err)
	}
	return c.manifest.Record(key, manifest.ManifestEntry{
		Digest:    digest,
		WrittenAt: time.Now(),
		Immutable: immutable,
	})
}
