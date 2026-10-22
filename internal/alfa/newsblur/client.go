package newsblur

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
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

	// tokenFn, set by EnableWrites, yields the session cookie on first use
	// instead of at construction; tokenMu guards token while it is filled.
	tokenMu sync.Mutex
	tokenFn func(context.Context) (string, error)

	// cacheOnlyReads keeps ordinary reads (get) on the local cache even though
	// the client can now reach NewsBlur, so a read never needs the token.
	cacheOnlyReads bool
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

// EnableWrites lets a cache-only client write to NewsBlur, with the session
// cookie fetched from tokenFn the first time something goes to the network (a
// mutation, or the live reads one needs) and kept once it succeeds. Ordinary
// reads stay on the local cache and never call tokenFn, so a locked credential
// store costs a read nothing. A failed tokenFn is not remembered: the next
// network call asks again.
func (c *Client) EnableWrites(tokenFn func(context.Context) (string, error)) {
	c.baseURL = DefaultBaseURL
	c.httpClient = &http.Client{}
	c.tokenFn = tokenFn
	c.cacheOnlyReads = true
}

// cookie returns the session cookie header value for a network call.
func (c *Client) cookie(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.token == "" && c.tokenFn != nil {
		token, err := c.tokenFn(ctx)
		if err != nil {
			return "", fmt.Errorf("newsblur: no session token: %w", err)
		}
		if token == "" {
			return "", errors.New("newsblur: no session token: the credential source returned an empty value")
		}
		c.token = token
	}
	return "newsblur_sessionid=" + c.token, nil
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

// LocalWrites counts the manifest writes this process has made (cache
// patches after a mutation, fetched stories, ...). It is a cheap "something
// changed here" signal for readers that debounce their file-mtime check: a
// read right after a local write must see it (nebulous#56). Writes by other
// processes are not counted; they are caught by the mtime check.
func (c *Client) LocalWrites() uint64 {
	if c.cache == nil {
		return 0
	}
	return c.cache.writes.Load()
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
		if c.httpClient == nil || c.cacheOnlyReads {
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

	cookie, err := c.cookie(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cookie)

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

	// Every endpoint read here answers JSON. A 200 that is not (an HTML
	// error or interstitial page) is a failed fetch, not an answer: cached,
	// it would stop `fetch` from asking again (HasCachedOriginalText) while
	// every reader quietly finds nothing in it.
	if !json.Valid(body) {
		return nil, fmt.Errorf("newsblur: GET %s returned a non-JSON body (HTTP 200, content-type %q): %.80q",
			path, resp.Header.Get("Content-Type"), body)
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

// PutCachedStarredStoriesBatch writes multiple starred-story cache entries
// with a single manifest save -- see responseCache.putImmutableBatch.
func (c *Client) PutCachedStarredStoriesBatch(byHash map[string]json.RawMessage) error {
	if c.cache == nil {
		return nil
	}
	entries := make(map[string]json.RawMessage, len(byHash))
	for hash, raw := range byHash {
		entries[c.starredStoryCacheKey(hash)] = raw
	}
	return c.cache.putImmutableBatch(entries)
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

	cookie, err := c.cookie(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cookie)
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

	// NewsBlur reports many write failures as HTTP 200 with a negative
	// "code" in the body; surface them so callers never treat a rejected
	// write as done (and never patch the cache for it).
	if err := mutationFailure(body); err != nil {
		return nil, fmt.Errorf("POST %s: %w", path, err)
	}

	return json.RawMessage(body), nil
}
