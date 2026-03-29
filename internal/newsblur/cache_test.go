package newsblur

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCachePutAndGet(t *testing.T) {
	c := &responseCache{dir: t.TempDir(), ttl: time.Hour}
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
	c := &responseCache{dir: t.TempDir(), ttl: time.Hour}
	key := c.cacheKey("/test", nil)
	body := json.RawMessage(`{"ok":true}`)

	if err := c.put(key, body); err != nil {
		t.Fatalf("put: %v", err)
	}

	// Backdate the file past TTL
	fp := filepath.Join(c.dir, key)
	past := time.Now().Add(-2 * time.Hour)
	os.Chtimes(fp, past, past)

	_, ok := c.get(key)
	if ok {
		t.Error("get returned true for expired key")
	}
}

func TestCacheHasIgnoresTTL(t *testing.T) {
	c := &responseCache{dir: t.TempDir(), ttl: time.Hour}
	key := c.cacheKey("/test", nil)
	body := json.RawMessage(`{"ok":true}`)

	if err := c.put(key, body); err != nil {
		t.Fatalf("put: %v", err)
	}

	// Backdate past TTL
	fp := filepath.Join(c.dir, key)
	past := time.Now().Add(-2 * time.Hour)
	os.Chtimes(fp, past, past)

	if !c.has(key) {
		t.Error("has returned false for expired-but-existing key")
	}
}

func TestCacheRemove(t *testing.T) {
	c := &responseCache{dir: t.TempDir(), ttl: time.Hour}
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
	c := &responseCache{dir: t.TempDir(), ttl: time.Hour}

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

func TestCacheGetMissing(t *testing.T) {
	c := &responseCache{dir: t.TempDir(), ttl: time.Hour}
	_, ok := c.get("nonexistent")
	if ok {
		t.Error("get returned true for missing key")
	}
}

func TestCacheHasMissing(t *testing.T) {
	c := &responseCache{dir: t.TempDir(), ttl: time.Hour}
	if c.has("nonexistent") {
		t.Error("has returned true for missing key")
	}
}
