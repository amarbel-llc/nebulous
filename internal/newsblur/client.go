package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

func (c *Client) WithCache(dir string, ttl time.Duration) {
	c.cache = &responseCache{dir: dir, ttl: ttl}
}

func (c *Client) get(ctx context.Context, path string, params url.Values) (json.RawMessage, error) {
	if c.cache != nil {
		cacheKey := c.cache.cacheKey(path, params)
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

func (c *Client) InvalidateStarredStoryPages() {
	if c.cache == nil {
		return
	}
	for page := 1; ; page++ {
		params := url.Values{"page": {fmt.Sprintf("%d", page)}}
		key := c.cache.cacheKey("/reader/starred_stories", params)
		if !c.cache.has(key) {
			break
		}
		c.cache.remove(key)
	}
	c.cache.remove(c.cache.cacheKey("/reader/starred_story_hashes", nil))
}

func (c *Client) OriginalTextCacheKey(storyHash string) string {
	params := url.Values{"story_hash": {storyHash}}
	return c.cache.cacheKey("/rss_feeds/original_text", params)
}

func (c *Client) HasCachedOriginalText(storyHash string) bool {
	if c.cache == nil {
		return false
	}
	return c.cache.has(c.OriginalTextCacheKey(storyHash))
}

func (c *Client) CachedOriginalText(storyHash string) (json.RawMessage, bool) {
	if c.cache == nil {
		return nil, false
	}
	key := c.OriginalTextCacheKey(storyHash)
	fp := filepath.Join(c.cache.dir, key)
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(data), true
}

func (c *Client) post(ctx context.Context, path string, form url.Values) (json.RawMessage, error) {
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
