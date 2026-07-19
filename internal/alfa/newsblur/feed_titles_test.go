package newsblur

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSeenFeedTitlesRoundTrip(t *testing.T) {
	c := NewClient("test-token")
	if err := c.WithCache(filepath.Join(t.TempDir(), "manifest.json"), time.Hour, newMemSink()); err != nil {
		t.Fatalf("WithCache: %v", err)
	}

	if titles := c.SeenFeedTitles(); len(titles) != 0 {
		t.Fatalf("expected no seen titles before any Put, got %v", titles)
	}

	if err := c.PutSeenFeedTitles(map[string]string{"1": "Feed One", "2": "Feed Two"}); err != nil {
		t.Fatalf("PutSeenFeedTitles: %v", err)
	}

	got := c.SeenFeedTitles()
	if got["1"] != "Feed One" || got["2"] != "Feed Two" {
		t.Errorf("SeenFeedTitles = %v, want {1: Feed One, 2: Feed Two}", got)
	}
}

// The registry only grows: a feed dropped from a later /reader/feeds
// fetch (unsubscribed) must keep its last-known title rather than being
// pruned — that's the whole point (nebulous#49).
func TestSeenFeedTitlesNeverDeletes(t *testing.T) {
	c := NewClient("test-token")
	if err := c.WithCache(filepath.Join(t.TempDir(), "manifest.json"), time.Hour, newMemSink()); err != nil {
		t.Fatalf("WithCache: %v", err)
	}

	if err := c.PutSeenFeedTitles(map[string]string{"1": "Feed One", "2": "Feed Two"}); err != nil {
		t.Fatalf("PutSeenFeedTitles (first): %v", err)
	}
	// A later fetch's snapshot no longer includes feed "2" (unsubscribed).
	if err := c.PutSeenFeedTitles(map[string]string{"1": "Feed One"}); err != nil {
		t.Fatalf("PutSeenFeedTitles (second): %v", err)
	}

	got := c.SeenFeedTitles()
	if got["2"] != "Feed Two" {
		t.Errorf("SeenFeedTitles[2] = %q, want it to survive dropping out of a later snapshot", got["2"])
	}
}

func TestSeenFeedTitlesUpdatesOnRename(t *testing.T) {
	c := NewClient("test-token")
	if err := c.WithCache(filepath.Join(t.TempDir(), "manifest.json"), time.Hour, newMemSink()); err != nil {
		t.Fatalf("WithCache: %v", err)
	}

	if err := c.PutSeenFeedTitles(map[string]string{"1": "Old Name"}); err != nil {
		t.Fatalf("PutSeenFeedTitles (first): %v", err)
	}
	if err := c.PutSeenFeedTitles(map[string]string{"1": "New Name"}); err != nil {
		t.Fatalf("PutSeenFeedTitles (second): %v", err)
	}

	if got := c.SeenFeedTitles()["1"]; got != "New Name" {
		t.Errorf("SeenFeedTitles[1] = %q, want the latest title %q", got, "New Name")
	}
}

func TestSeenFeedTitlesNoCacheIsNoop(t *testing.T) {
	c := NewClient("test-token") // no WithCache

	if titles := c.SeenFeedTitles(); len(titles) != 0 {
		t.Errorf("expected no seen titles with no cache attached, got %v", titles)
	}
	if err := c.PutSeenFeedTitles(map[string]string{"1": "Feed One"}); err != nil {
		t.Errorf("PutSeenFeedTitles with no cache attached should be a no-op, got error: %v", err)
	}
}
