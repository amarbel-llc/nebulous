package newsblur

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A probe of /rss_feeds/original_text once answered with an HTML page (it
// started "<!DOCTYPE html>"; its HTTP status was not captured and it has not
// reproduced). Every endpoint nebulous reads is JSON, so a body that is not
// JSON must be an error and must never be cached: cached, it would stop
// `fetch` from ever asking for that story's original text again
// (HasCachedOriginalText), while every reader silently finds no text in it.
func TestOriginalTextRejectsAnHTMLBodyAndDoesNotCacheIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<!DOCTYPE html>\n<html><head><title>Oops</title></head></html>"))
	}))
	defer server.Close()
	c := testClientAgainstServer(t, server)

	_, err := c.OriginalText(context.Background(), "0:abc123")
	if err == nil {
		t.Fatal("OriginalText: expected an error for a 200 HTML body")
	}
	if !strings.Contains(err.Error(), "non-JSON") {
		t.Errorf("error %q does not say the body was not JSON", err)
	}
	if c.HasCachedOriginalText("0:abc123") {
		t.Error("the HTML body was cached as the story's original text")
	}
}

func TestOriginalTextStillCachesAFailedExtraction(t *testing.T) {
	// NewsBlur answers a page it could not extract with valid JSON,
	// original_text null and failed true. That is a real answer and stays
	// cached as before; only a non-JSON body is refused.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"feed_id": 0, "story_hash": "0:abc123", "original_text": null, "failed": true}`))
	}))
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if _, err := c.OriginalText(context.Background(), "0:abc123"); err != nil {
		t.Fatalf("OriginalText: %v", err)
	}
	if !c.HasCachedOriginalText("0:abc123") {
		t.Error("a valid JSON answer was not cached")
	}
}
