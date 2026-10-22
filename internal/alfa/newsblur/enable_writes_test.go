package newsblur

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// writeEnabledClient is a cache-only client (ttl 0, as the plugin builds it)
// upgraded with EnableWrites and pointed at server instead of NewsBlur.
func writeEnabledClient(t *testing.T, server *httptest.Server, tokenFn func(context.Context) (string, error)) *Client {
	t.Helper()
	c := &Client{}
	if err := c.WithCache(filepath.Join(t.TempDir(), "manifest.json"), 0, newMemSink()); err != nil {
		t.Fatal(err)
	}
	c.EnableWrites(tokenFn)
	c.baseURL = server.URL
	c.httpClient = server.Client()
	return c
}

func TestEnableWritesFetchesTheTokenOnFirstPostOnly(t *testing.T) {
	var cookies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookies = append(cookies, r.Header.Get("Cookie"))
		w.Write([]byte(`{"result":"ok"}`))
	}))
	defer server.Close()

	var calls atomic.Int32
	c := writeEnabledClient(t, server, func(context.Context) (string, error) {
		calls.Add(1)
		return "lazy-token", nil
	})
	if calls.Load() != 0 {
		t.Fatalf("token fetched at EnableWrites time (%d calls); want none until a network call", calls.Load())
	}

	for range 2 {
		if _, err := c.post(context.Background(), "/reader/x", url.Values{}); err != nil {
			t.Fatalf("post: %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("tokenFn called %d times, want 1 (a successful token is kept)", calls.Load())
	}
	for _, got := range cookies {
		if got != "newsblur_sessionid=lazy-token" {
			t.Errorf("Cookie = %q, want newsblur_sessionid=lazy-token", got)
		}
	}
}

func TestEnableWritesKeepsReadsOffTheNetworkAndTheToken(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	var calls atomic.Int32
	c := writeEnabledClient(t, server, func(context.Context) (string, error) {
		calls.Add(1)
		return "lazy-token", nil
	})

	_, err := c.get(context.Background(), "/reader/feeds", nil)
	if err == nil || !strings.Contains(err.Error(), "not cached") {
		t.Fatalf("get of an uncached path = %v, want a not-cached error", err)
	}
	if hits.Load() != 0 || calls.Load() != 0 {
		t.Errorf("a read reached the network (%d hits) or the token (%d calls)", hits.Load(), calls.Load())
	}
}

func TestEnableWritesRetriesAfterATokenFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":"ok"}`))
	}))
	defer server.Close()

	var calls atomic.Int32
	c := writeEnabledClient(t, server, func(context.Context) (string, error) {
		if calls.Add(1) == 1 {
			return "", errors.New("agent locked")
		}
		return "lazy-token", nil
	})

	if _, err := c.post(context.Background(), "/reader/x", url.Values{}); err == nil || !strings.Contains(err.Error(), "agent locked") {
		t.Fatalf("first post = %v, want the credential error", err)
	}
	if _, err := c.post(context.Background(), "/reader/x", url.Values{}); err != nil {
		t.Fatalf("second post: %v (a failed token must not be remembered)", err)
	}
	if calls.Load() != 2 {
		t.Errorf("tokenFn called %d times, want 2", calls.Load())
	}
}
