package newsblur

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/friedenberg/nebulous/internal/blobstore"
)

func newTestCache(t *testing.T, ttl time.Duration) *responseCache {
	t.Helper()
	c, err := newResponseCache(t.TempDir(), ttl, nil)
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

func TestCacheReloadPersists(t *testing.T) {
	dir := t.TempDir()
	c1, err := newResponseCache(dir, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := c1.cacheKey("/test", nil)
	body := json.RawMessage(`{"persisted":true}`)
	if err := c1.put(key, body); err != nil {
		t.Fatal(err)
	}

	c2, err := newResponseCache(dir, time.Hour, nil)
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

func TestNewResponseCacheEmptyDir(t *testing.T) {
	if _, err := newResponseCache("", time.Hour, nil); err == nil {
		t.Error("expected error for empty dir")
	}
}

func TestCacheWithCustomStore(t *testing.T) {
	// Ensure the caller can inject a Store; the FilesystemStore default
	// should behave identically to explicit injection.
	dir := t.TempDir()
	store := blobstore.NewFilesystemStore(dir)
	c, err := newResponseCache(dir, time.Hour, store)
	if err != nil {
		t.Fatal(err)
	}
	key := c.cacheKey("/test", nil)
	body := json.RawMessage(`{"injected":true}`)
	if err := c.put(key, body); err != nil {
		t.Fatal(err)
	}
	got, ok := c.get(key)
	if !ok {
		t.Fatal("get returned false")
	}
	if string(got) != string(body) {
		t.Errorf("got %s, want %s", got, body)
	}
}
