package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultBaseURL = "https://www.newsblur.com"

// RateLimitError is returned when the API responds with HTTP 429.
type RateLimitError struct {
	RetryAfter time.Duration
	Body       string
}

func (e *RateLimitError) Error() string {
	parts := []string{"rate limited"}
	if e.RetryAfter > 0 {
		parts = append(parts, fmt.Sprintf("retry after %s", e.RetryAfter))
	}
	if e.Body != "" {
		parts = append(parts, e.Body)
	}
	return strings.Join(parts, ": ")
}

func parseRetryAfter(resp *http.Response) time.Duration {
	h := resp.Header.Get("Retry-After")
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	cache      *responseCache
}

func NewClient(token string) *Client {
	return &Client{
		baseURL:    DefaultBaseURL,
		token:      token,
		httpClient: &http.Client{},
	}
}

// NewCacheOnlyClient creates a client that reads exclusively from the local
// persistent cache. It has no auth token or HTTP client — any attempt to make
// API calls will fail. Used by offline subcommands (corpus-list, corpus-read).
func NewCacheOnlyClient(manifestPath string, sink BlobSink) (*Client, error) {
	c := &Client{}
	if err := c.WithCache(manifestPath, 0, sink); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) WithCache(manifestPath string, ttl time.Duration, sink BlobSink) error {
	rc, err := newResponseCache(manifestPath, ttl, sink)
	if err != nil {
		return err
	}
	c.cache = rc
	return nil
}

// ManifestPath returns the on-disk location of the persistent cache
// manifest, or "" if the client has no cache attached. Used to derive a
// cheap "has anything changed" freshness signal (e.g. a facet version
// token) without scanning the manifest's contents.
func (c *Client) ManifestPath() string {
	if c.cache == nil {
		return ""
	}
	return c.cache.manifest.Path()
}

// ForceManifestRefresh bypasses the manifest's own staleness debounce
// (internal/0/manifest's staleCheckDebounce). A caller that already
// decided a rebuild is warranted — e.g. storyStore/feedIndex's own outer
// staleness check just fired — calls this first so Cached*/Feeds reads
// made during that rebuild aren't gated by the manifest's separate,
// independently-timed debounce window, which could otherwise still be
// open and serve pre-write data into a rebuild the caller believes is
// now current.
func (c *Client) ForceManifestRefresh() {
	if c.cache == nil {
		return
	}
	c.cache.manifest.ForceRefresh()
}

func (c *Client) get(ctx context.Context, path string, params url.Values) (json.RawMessage, error) {
	if c.cache != nil {
		cacheKey := c.cache.cacheKey(path, params)
		if c.httpClient == nil {
			// Cache-only client (NewCacheOnlyClient, ttl=0): there is no
			// live source to fall back to, so a TTL-expired entry is
			// still the best available data — never fall through to
			// doGet, which would nil-panic on this client's absent
			// httpClient. Confirmed in production: nebulous-cg mcp
			// crashed here when cutting-garden's own background
			// facet-maintenance goroutine called FacetCounts, which
			// reaches feedIndex.build -> client.Feeds -> here, and a
			// ttl=0 cache-only client's non-immutable feeds cache entry
			// always reads as TTL-expired via the regular get() below.
			if cached, ok := c.cache.getNoTTL(cacheKey); ok {
				return cached, nil
			}
			return nil, fmt.Errorf("newsblur: %s not cached (cache-only client, no NewsBlur token)", path)
		}
		if cached, ok := c.cache.get(cacheKey); ok {
			return cached, nil
		}
	}
	return c.doGet(ctx, path, params)
}

func (c *Client) getSkipCache(ctx context.Context, path string, params url.Values) (json.RawMessage, error) {
	return c.doGet(ctx, path, params)
}

func (c *Client) doGet(ctx context.Context, path string, params url.Values) (json.RawMessage, error) {
	if c.httpClient == nil {
		return nil, fmt.Errorf("newsblur: cannot fetch %s live: client has no HTTP client (cache-only mode)", path)
	}
	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Cookie", "newsblur_sessionid="+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &RateLimitError{
			RetryAfter: parseRetryAfter(resp),
			Body:       string(body),
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	raw := json.RawMessage(body)

	if c.cache != nil {
		cacheKey := c.cache.cacheKey(path, params)
		_ = c.cache.put(cacheKey, raw)
	}

	return raw, nil
}

func (c *Client) starredStoryCacheKey(hash string) string {
	params := url.Values{"story_hash": {hash}}
	return c.cache.cacheKey("/starred_story", params)
}

func (c *Client) HasCachedStarredStory(hash string) bool {
	if c.cache == nil {
		return false
	}
	return c.cache.has(c.starredStoryCacheKey(hash))
}

func (c *Client) CachedStarredStory(hash string) (json.RawMessage, bool) {
	if c.cache == nil {
		return nil, false
	}
	return c.cache.getNoTTL(c.starredStoryCacheKey(hash))
}

func (c *Client) PutCachedStarredStory(hash string, raw json.RawMessage) error {
	if c.cache == nil {
		return nil
	}
	return c.cache.putImmutable(c.starredStoryCacheKey(hash), raw)
}

func (c *Client) CachedStarredStoryHashes() (json.RawMessage, bool) {
	if c.cache == nil {
		return nil, false
	}
	return c.cache.getNoTTL(c.cache.cacheKey("/reader/starred_story_hashes", nil))
}

func (c *Client) PutCachedStarredStoryHashes(raw json.RawMessage) error {
	if c.cache == nil {
		return nil
	}
	key := c.cache.cacheKey("/reader/starred_story_hashes", nil)
	return c.cache.putImmutable(key, raw)
}

func (c *Client) InvalidateStarredStoryHashManifest() {
	if c.cache == nil {
		return
	}
	c.cache.remove(c.cache.cacheKey("/reader/starred_story_hashes", nil))
}

func (c *Client) originalTextCacheKey(storyHash string) string {
	params := url.Values{"story_hash": {storyHash}}
	return c.cache.cacheKey("/rss_feeds/original_text", params)
}

func (c *Client) HasCachedOriginalText(storyHash string) bool {
	if c.cache == nil {
		return false
	}
	return c.cache.has(c.originalTextCacheKey(storyHash))
}

func (c *Client) CachedOriginalText(storyHash string) (json.RawMessage, bool) {
	if c.cache == nil {
		return nil, false
	}
	return c.cache.getNoTTL(c.originalTextCacheKey(storyHash))
}

func (c *Client) PutCachedOriginalText(storyHash string, raw json.RawMessage) error {
	if c.cache == nil {
		return nil
	}
	return c.cache.putImmutable(c.originalTextCacheKey(storyHash), raw)
}

func (c *Client) post(ctx context.Context, path string, form url.Values) (json.RawMessage, error) {
	if c.httpClient == nil {
		return nil, fmt.Errorf("newsblur: cannot post %s: client has no HTTP client (cache-only mode)", path)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Cookie", "newsblur_sessionid="+c.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return json.RawMessage(body), nil
}
