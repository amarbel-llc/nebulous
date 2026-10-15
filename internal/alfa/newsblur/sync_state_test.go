package newsblur

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestStarredHashTimestampsParsesPairs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reader/starred_story_hashes" || r.URL.Query().Get("include_timestamps") != "true" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"starred_story_hashes":[["1:a","1700000000"],["2:b","1700000500"]]}`))
	}))
	defer server.Close()
	c := testClientAgainstServer(t, server)

	got, err := c.StarredHashTimestamps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"1:a": "1700000000", "2:b": "1700000500"}; !reflect.DeepEqual(got, want) {
		t.Errorf("timestamps = %v, want %v", got, want)
	}
}

func TestStarredHashTimestampsRejectsMalformedPair(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"starred_story_hashes":[["only-a-hash"]]}`))
	}))
	defer server.Close()
	c := testClientAgainstServer(t, server)
	if _, err := c.StarredHashTimestamps(context.Background()); err == nil {
		t.Fatal("a malformed entry was accepted")
	}
}

func TestStarredSyncStateRoundTrip(t *testing.T) {
	c := testClientAgainstServer(t, okServer(t))

	if _, ok := c.CachedStarredSyncState(); ok {
		t.Fatal("a fresh cache reported a recorded sync state")
	}
	when := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	in := StarredSyncState{Timestamps: map[string]string{"1:a": "42"}, FullSyncAt: when}
	if err := c.PutCachedStarredSyncState(in); err != nil {
		t.Fatal(err)
	}
	got, ok := c.CachedStarredSyncState()
	if !ok || got.Timestamps["1:a"] != "42" || !got.FullSyncAt.Equal(when) {
		t.Errorf("round trip = %+v, %v", got, ok)
	}

	// Replacing it keeps only the new content.
	if err := c.PutCachedStarredSyncState(StarredSyncState{Timestamps: map[string]string{"2:b": "7"}}); err != nil {
		t.Fatal(err)
	}
	got, _ = c.CachedStarredSyncState()
	if _, stale := got.Timestamps["1:a"]; stale || got.Timestamps["2:b"] != "7" {
		t.Errorf("after replace = %+v", got)
	}
}
