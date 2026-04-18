package newsblur

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// memSink is an in-memory BlobSink for unit tests. The id scheme mirrors
// madder's blech32-like convention (`<family>-<payload>`) so tests that care
// about the format-prefix shape can inspect returned ids meaningfully.
type memSink struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func newMemSink() *memSink { return &memSink{blobs: map[string][]byte{}} }

func (s *memSink) Read(id string, dst io.Writer) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blobs[id]
	if !ok {
		return false, nil
	}
	if _, err := dst.Write(b); err != nil {
		return false, err
	}
	return true, nil
}

func (s *memSink) Write(src io.Reader) (string, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, src); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	id := fmt.Sprintf("sha256test-%s", hex.EncodeToString(sum[:]))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobs[id] = buf.Bytes()
	return id, nil
}

func newTestCache(t *testing.T, ttl time.Duration) *responseCache {
	t.Helper()
	c, err := newResponseCache(filepath.Join(t.TempDir(), "manifest.json"), ttl, newMemSink())
	if err != nil {
		t.Fatalf("newResponseCache: %v", err)
	}
	return c
}

// backdate rewrites a manifest entry's WrittenAt without touching the blob.
// Used to simulate TTL expiry without a clock injection.
func backdate(t *testing.T, c *responseCache, key string, when time.Time) {
	t.Helper()
	entry, ok := c.manifest.Lookup(key)
	if !ok {
		t.Fatalf("backdate: key %s not found", key)
	}
	entry.WrittenAt = when
	if err := c.manifest.Record(key, entry); err != nil {
		t.Fatalf("backdate: %v", err)
	}
}

func TestCachePutAndGet(t *testing.T) {
	c := newTestCache(t, time.Hour)
	key := c.cacheKey("/test", nil)
	body := json.RawMessage(`{"ok":true}`)

	if err := c.put(key, body); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, ok := c.get(key)
	if !ok {
		t.Fatal("get returned false for existing key")
	}
	if string(got) != string(body) {
		t.Errorf("got %s, want %s", got, body)
	}
}

func TestCacheGetExpired(t *testing.T) {
	c := newTestCache(t, time.Hour)
	key := c.cacheKey("/test", nil)
	body := json.RawMessage(`{"ok":true}`)

	if err := c.put(key, body); err != nil {
		t.Fatalf("put: %v", err)
	}

	backdate(t, c, key, time.Now().Add(-2*time.Hour))

	if _, ok := c.get(key); ok {
		t.Error("get returned true for expired key")
	}
}

func TestCacheHasIgnoresTTL(t *testing.T) {
	c := newTestCache(t, time.Hour)
	key := c.cacheKey("/test", nil)

	if err := c.put(key, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatalf("put: %v", err)
	}
	backdate(t, c, key, time.Now().Add(-2*time.Hour))

	if !c.has(key) {
		t.Error("has returned false for expired-but-existing key")
	}
}

func TestCachePutImmutableIgnoresTTL(t *testing.T) {
	c := newTestCache(t, time.Hour)
	key := c.cacheKey("/test", nil)
	body := json.RawMessage(`{"ok":true}`)

	if err := c.putImmutable(key, body); err != nil {
		t.Fatalf("putImmutable: %v", err)
	}
	backdate(t, c, key, time.Now().Add(-2*time.Hour))

	// Even a regular get() should succeed on an immutable entry.
	got, ok := c.get(key)
	if !ok {
		t.Fatal("get returned false for immutable expired key")
	}
	if string(got) != string(body) {
		t.Errorf("got %s, want %s", got, body)
	}
}

func TestCacheRemove(t *testing.T) {
	c := newTestCache(t, time.Hour)
	key := c.cacheKey("/test", nil)

	if err := c.put(key, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("put: %v", err)
	}
	c.remove(key)

	if c.has(key) {
		t.Error("has returned true after remove")
	}
}

func TestCacheKeyDeterministic(t *testing.T) {
	c := newTestCache(t, time.Hour)

	params := url.Values{"page": {"1"}, "tag": {"news"}}
	k1 := c.cacheKey("/reader/starred_stories", params)
	k2 := c.cacheKey("/reader/starred_stories", params)
	if k1 != k2 {
		t.Errorf("keys differ: %s vs %s", k1, k2)
	}

	k3 := c.cacheKey("/reader/starred_stories", nil)
	if k1 == k3 {
		t.Error("different params produced same key")
	}
}

func TestCacheGetNoTTLIgnoresExpiry(t *testing.T) {
	c := newTestCache(t, time.Hour)
	key := c.cacheKey("/test", nil)
	body := json.RawMessage(`{"ok":true}`)

	if err := c.put(key, body); err != nil {
		t.Fatalf("put: %v", err)
	}
	backdate(t, c, key, time.Now().Add(-2*time.Hour))

	if _, ok := c.get(key); ok {
		t.Error("get should return false for expired key")
	}
	got, ok := c.getNoTTL(key)
	if !ok {
		t.Fatal("getNoTTL returned false for expired-but-existing key")
	}
	if string(got) != string(body) {
		t.Errorf("got %s, want %s", got, body)
	}
}

func TestCacheGetNoTTLMissing(t *testing.T) {
	c := newTestCache(t, time.Hour)
	if _, ok := c.getNoTTL("nonexistent"); ok {
		t.Error("getNoTTL returned true for missing key")
	}
}

func TestCacheGetMissing(t *testing.T) {
	c := newTestCache(t, time.Hour)
	if _, ok := c.get("nonexistent"); ok {
		t.Error("get returned true for missing key")
	}
}

func TestCacheHasMissing(t *testing.T) {
	c := newTestCache(t, time.Hour)
	if c.has("nonexistent") {
		t.Error("has returned true for missing key")
	}
}

func TestCacheReloadPersistsManifest(t *testing.T) {
	// The manifest survives across restarts; the sink is ephemeral in the
	// test. A reloaded cache pointing at the same manifest file + a sink
	// holding the same blobs should surface the entry. This mirrors the
	// production reality where madder's tree persists independently of
	// nebulous's manifest.
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	sink := newMemSink()

	c1, err := newResponseCache(manifestPath, time.Hour, sink)
	if err != nil {
		t.Fatal(err)
	}
	key := c1.cacheKey("/test", nil)
	body := json.RawMessage(`{"persisted":true}`)
	if err := c1.put(key, body); err != nil {
		t.Fatal(err)
	}

	c2, err := newResponseCache(manifestPath, time.Hour, sink)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := c2.get(key)
	if !ok {
		t.Fatal("reloaded cache missing entry")
	}
	if string(got) != string(body) {
		t.Errorf("got %s, want %s", got, body)
	}
}

func TestNewResponseCacheEmptyPath(t *testing.T) {
	if _, err := newResponseCache("", time.Hour, newMemSink()); err == nil {
		t.Error("expected error for empty manifest path")
	}
}

func TestNewResponseCacheNilSink(t *testing.T) {
	if _, err := newResponseCache(filepath.Join(t.TempDir(), "m.json"), time.Hour, nil); err == nil {
		t.Error("expected error for nil sink")
	}
}
