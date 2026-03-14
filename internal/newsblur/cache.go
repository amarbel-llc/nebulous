package newsblur

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type responseCache struct {
	dir string
	ttl time.Duration
}

func (c *responseCache) cacheKey(path string, params url.Values) string {
	full := path
	if len(params) > 0 {
		full += "?" + params.Encode()
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(full)))
}

func (c *responseCache) get(key string) (json.RawMessage, bool) {
	fp := filepath.Join(c.dir, key)
	info, err := os.Stat(fp)
	if err != nil {
		return nil, false
	}
	if time.Since(info.ModTime()) > c.ttl {
		return nil, false
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(data), true
}

func (c *responseCache) remove(key string) {
	os.Remove(filepath.Join(c.dir, key))
}

func (c *responseCache) put(key string, body json.RawMessage) error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.dir, key), body, 0o644)
}
