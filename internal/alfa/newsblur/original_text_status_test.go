package newsblur

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOriginalTextStatusClassifiesCachedAnswers(t *testing.T) {
	server := okServer(t)
	defer server.Close()
	c := testClientAgainstServer(t, server)

	cases := []struct {
		name string
		raw  string // "" means nothing cached
		want OriginalTextState
	}{
		{"nothing cached", "", OriginalTextMissing},
		{"extracted text", `{"original_text":"<p>the article</p>","failed":false}`, OriginalTextFinal},
		{"extraction failed", `{"original_text":null,"failed":true,"result":"ok"}`, OriginalTextRetryable},
		{"failed with empty text", `{"original_text":"","failed":true}`, OriginalTextRetryable},
		// A body cached before doGet refused non-JSON: it can never yield text.
		{"legacy non-JSON body", `<!DOCTYPE html><html></html>`, OriginalTextRetryable},
		// An answer that says it succeeded is final even with no text: asking
		// again would only get the same answer.
		{"succeeded with no text", `{"original_text":"","failed":false}`, OriginalTextFinal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hash := "0:" + tc.name
			if tc.raw != "" {
				if err := c.PutCachedOriginalText(hash, json.RawMessage(tc.raw)); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			got, writtenAt := c.OriginalTextStatus(hash)
			if got != tc.want {
				t.Errorf("state = %v, want %v", got, tc.want)
			}
			if tc.raw != "" && time.Since(writtenAt) > time.Minute {
				t.Errorf("writtenAt = %v, want the time of the write", writtenAt)
			}
		})
	}
}

func TestOriginalTextStatusOfACacheOnlyClientWithoutACache(t *testing.T) {
	c := &Client{}
	if got, _ := c.OriginalTextStatus("0:abc"); got != OriginalTextMissing {
		t.Errorf("state = %v, want Missing for a client with no cache", got)
	}
}

// RefetchOriginalText must reach NewsBlur even when an answer is already
// cached: get() honors an immutable entry forever, which would make a retry
// of a failed answer return the same failed answer without a request.
func TestRefetchOriginalTextBypassesAnImmutableCachedAnswer(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"original_text":"<p>now extracted</p>","failed":false}`))
	}))
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if err := c.PutCachedOriginalText("0:abc", json.RawMessage(`{"original_text":null,"failed":true}`)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if state, _ := c.OriginalTextStatus("0:abc"); state != OriginalTextRetryable {
		t.Fatalf("seeded state = %v, want Retryable", state)
	}

	if _, err := c.RefetchOriginalText(context.Background(), "0:abc"); err != nil {
		t.Fatalf("RefetchOriginalText: %v", err)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1: the refetch must not be served from the cache", requests)
	}
	if state, _ := c.OriginalTextStatus("0:abc"); state != OriginalTextFinal {
		t.Errorf("state after a successful refetch = %v, want Final", state)
	}
}

// A refetch that fails again re-stamps the entry, so the retry interval
// counts from the LAST attempt: that is the backoff.
func TestRefetchOriginalTextRestampsAStillFailedAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"original_text":null,"failed":true}`))
	}))
	defer server.Close()
	c := testClientAgainstServer(t, server)

	if err := c.PutCachedOriginalText("0:abc", json.RawMessage(`{"original_text":null,"failed":true}`)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, before := c.OriginalTextStatus("0:abc")
	time.Sleep(10 * time.Millisecond)

	if _, err := c.RefetchOriginalText(context.Background(), "0:abc"); err != nil {
		t.Fatalf("RefetchOriginalText: %v", err)
	}
	state, after := c.OriginalTextStatus("0:abc")
	if state != OriginalTextRetryable {
		t.Errorf("state = %v, want still Retryable", state)
	}
	if !after.After(before) {
		t.Errorf("writtenAt %v is not after the seeded %v: the failed retry did not re-stamp the entry", after, before)
	}
}
