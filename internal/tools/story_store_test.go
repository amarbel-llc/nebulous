package tools

import (
	"encoding/json"
	"testing"
	"time"
)

func TestStoryStoreParseRecord(t *testing.T) {
	raw := json.RawMessage(`{
		"story_hash": "abc123",
		"story_title": "Test Story",
		"story_authors": "Alice",
		"story_feed_id": 42,
		"story_date": "2024-03-15 10:30:00",
		"story_permalink": "https://example.com/story",
		"story_tags": ["tech", "go"],
		"user_tags": ["interests", "zz-nyc"],
		"story_content": "<p>Hello world of Go programming</p>",
		"starred": true,
		"read_status": 1
	}`)

	rec, err := parseStoryRecord(raw, nil)
	if err != nil {
		t.Fatalf("parseStoryRecord: %v", err)
	}

	if rec.Hash != "abc123" {
		t.Errorf("Hash = %q, want %q", rec.Hash, "abc123")
	}
	if rec.Title != "Test Story" {
		t.Errorf("Title = %q, want %q", rec.Title, "Test Story")
	}
	if rec.FeedID != 42 {
		t.Errorf("FeedID = %d, want %d", rec.FeedID, 42)
	}
	if rec.Year != 2024 {
		t.Errorf("Year = %d, want %d", rec.Year, 2024)
	}
	if rec.Month != 3 {
		t.Errorf("Month = %d, want %d", rec.Month, int(time.March))
	}
	if len(rec.UserTags) != 2 || rec.UserTags[0] != "interests" {
		t.Errorf("UserTags = %v, want [interests zz-nyc]", rec.UserTags)
	}
	if !rec.Starred {
		t.Error("Starred = false, want true")
	}
	if !rec.Words["programming"] {
		t.Error("Words missing 'programming' from content")
	}
	if !rec.Words["hello"] {
		t.Error("Words missing 'hello' from content")
	}
	if !rec.Words["test"] {
		t.Error("Words missing 'test' from title")
	}
}

func TestStoryStoreParseRecordDateFormats(t *testing.T) {
	tests := []struct {
		name      string
		dateStr   string
		wantYear  int
		wantMonth int
	}{
		{"standard", `"2024-03-15 10:30:00"`, 2024, 3},
		{"with timezone", `"2024-12-01 08:00:00+00:00"`, 2024, 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := json.RawMessage(`{"story_hash":"h","story_title":"t","story_feed_id":1,"story_date":` + tt.dateStr + `}`)
			rec, err := parseStoryRecord(raw, nil)
			if err != nil {
				t.Fatalf("parseStoryRecord: %v", err)
			}
			if rec.Year != tt.wantYear {
				t.Errorf("Year = %d, want %d", rec.Year, tt.wantYear)
			}
			if rec.Month != tt.wantMonth {
				t.Errorf("Month = %d, want %d", rec.Month, tt.wantMonth)
			}
		})
	}
}
