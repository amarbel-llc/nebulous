package tools

import (
	"testing"
	"time"
)

func intPtr(n int) *int       { return &n }
func strPtr(s string) *string { return &s }

func hashesOf(recs []*storyRecord) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Hash
	}
	return out
}

func makeTestRecords() []*storyRecord {
	return []*storyRecord{
		{Hash: "a", Title: "Go and Nix", FeedID: 1, Date: time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC), Year: 2024, Month: 3, UserTags: []string{"interests"}, Starred: true, Words: map[string]bool{"nix": true, "golang": true}},
		{Hash: "b", Title: "NYC Bikes", FeedID: 2, Date: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), Year: 2024, Month: 6, UserTags: []string{"zz-nyc", "news"}, Starred: true, Words: map[string]bool{"bikes": true, "nyc": true}},
		{Hash: "c", Title: "Old Nix Article", FeedID: 1, Date: time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC), Year: 2023, Month: 1, UserTags: []string{"interests"}, Starred: true, Words: map[string]bool{"nix": true, "flake": true}},
		{Hash: "d", Title: "Security Post", FeedID: 3, Date: time.Date(2024, 11, 20, 0, 0, 0, 0, time.UTC), Year: 2024, Month: 11, UserTags: []string{"news"}, Starred: true, Words: map[string]bool{"security": true, "xz": true}},
	}
}

func makeTestStore() *storyStore {
	records := makeTestRecords()

	// Pre-sort by date descending (same order as build())
	sorted := make([]*storyRecord, len(records))
	copy(sorted, records)
	// d (2024-11), b (2024-06), a (2024-03), c (2023-01)
	sorted[0] = records[3] // d
	sorted[1] = records[1] // b
	sorted[2] = records[0] // a
	sorted[3] = records[2] // c

	words := make(map[string][]*storyRecord)
	for _, rec := range sorted {
		for word := range rec.Words {
			words[word] = append(words[word], rec)
		}
	}
	return &storyStore{stories: sorted, words: words}
}

func TestQueryByYear(t *testing.T) {
	s := makeTestStore()
	results := s.query(storyQuery{Year: intPtr(2024)})
	hashes := hashesOf(results)

	if len(hashes) != 3 {
		t.Fatalf("expected 3 results, got %d: %v", len(hashes), hashes)
	}
	if hashes[0] != "d" {
		t.Errorf("expected newest first (d), got %s", hashes[0])
	}
}

func TestQueryByYearAndMonth(t *testing.T) {
	s := makeTestStore()
	results := s.query(storyQuery{Year: intPtr(2024), Month: intPtr(3)})
	hashes := hashesOf(results)

	if len(hashes) != 1 || hashes[0] != "a" {
		t.Fatalf("expected [a], got %v", hashes)
	}
}

func TestQueryByTag(t *testing.T) {
	s := makeTestStore()
	results := s.query(storyQuery{Tag: strPtr("zz-nyc")})
	hashes := hashesOf(results)

	if len(hashes) != 1 || hashes[0] != "b" {
		t.Fatalf("expected [b], got %v", hashes)
	}
}

func TestQueryByWords(t *testing.T) {
	s := makeTestStore()
	results := s.query(storyQuery{Words: []string{"nix", "security"}})
	hashes := hashesOf(results)

	if len(hashes) != 3 {
		t.Fatalf("expected 3 results (OR-union), got %d: %v", len(hashes), hashes)
	}
	// Should be sorted by date descending: d (2024-11), a (2024-03), c (2023-01)
	expected := []string{"d", "a", "c"}
	for i, h := range expected {
		if hashes[i] != h {
			t.Errorf("position %d: expected %s, got %s", i, h, hashes[i])
		}
	}
}

func TestQueryWordsANDYear(t *testing.T) {
	s := makeTestStore()
	results := s.query(storyQuery{Words: []string{"nix"}, Year: intPtr(2024)})
	hashes := hashesOf(results)

	if len(hashes) != 1 || hashes[0] != "a" {
		t.Fatalf("expected [a], got %v", hashes)
	}
}

func TestQueryByFeedID(t *testing.T) {
	s := makeTestStore()
	results := s.query(storyQuery{FeedID: intPtr(1)})
	hashes := hashesOf(results)

	if len(hashes) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(hashes), hashes)
	}
}

func TestQueryOffsetLimit(t *testing.T) {
	s := makeTestStore()
	results := s.query(storyQuery{Year: intPtr(2024), Offset: 1, Limit: 1})
	hashes := hashesOf(results)

	if len(hashes) != 1 || hashes[0] != "b" {
		t.Fatalf("expected [b] (second of 3 sorted results), got %v", hashes)
	}
}

func TestQueryNoFilters(t *testing.T) {
	s := makeTestStore()
	results := s.query(storyQuery{Limit: 100})

	if len(results) != 4 {
		t.Fatalf("expected all 4 results, got %d", len(results))
	}
}
