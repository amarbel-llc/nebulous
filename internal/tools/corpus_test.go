package tools

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/friedenberg/nebulous/internal/newsblur"
)

func testCacheOnlyClient(t *testing.T) *newsblur.Client {
	t.Helper()
	c, err := newsblur.NewCacheOnlyClient(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}

	manifest := json.RawMessage(`{"starred_story_hashes":["rich","stub","missing"]}`)
	if err := c.PutCachedStarredStoryHashes(manifest); err != nil {
		t.Fatalf("put manifest: %v", err)
	}

	// "rich" has >200 chars of content after stripping.
	richContent := "<p>" + strings.Repeat("This is a detailed article about Go programming. ", 10) + "</p>"
	rich := json.RawMessage(`{
		"story_hash": "rich",
		"story_title": "Rich Story",
		"story_authors": "Alice",
		"story_feed_id": 1,
		"story_date": "2024-06-15 10:00:00",
		"story_content": ` + jsonString(richContent) + `,
		"starred": true,
		"read_status": 1
	}`)
	if err := c.PutCachedStarredStory("rich", rich); err != nil {
		t.Fatalf("put rich story: %v", err)
	}

	// "stub" has short content (< 200 chars).
	stub := json.RawMessage(`{
		"story_hash": "stub",
		"story_title": "Stub Story",
		"story_authors": "Bob",
		"story_feed_id": 2,
		"story_date": "2024-03-10 08:00:00",
		"story_content": "<p>Short</p>",
		"starred": true,
		"read_status": 0
	}`)
	if err := c.PutCachedStarredStory("stub", stub); err != nil {
		t.Fatalf("put stub story: %v", err)
	}

	// "missing" is in the manifest but has no cached story data.
	return c
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestCorpusListOutputsOnlyExistingHashes(t *testing.T) {
	c := testCacheOnlyClient(t)
	var buf bytes.Buffer

	if err := CorpusList(c, &buf, 0); err != nil {
		t.Fatalf("CorpusList: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (rich + stub, not missing)\noutput: %q", len(lines), buf.String())
	}

	if lines[0] != "rich" {
		t.Errorf("line[0] = %q, want %q", lines[0], "rich")
	}
	if lines[1] != "stub" {
		t.Errorf("line[1] = %q, want %q", lines[1], "stub")
	}
}

func TestCorpusListWithLimit(t *testing.T) {
	c := testCacheOnlyClient(t)
	var buf bytes.Buffer

	if err := CorpusList(c, &buf, 1); err != nil {
		t.Fatalf("CorpusList: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines with limit=1, want 1\noutput: %q", len(lines), buf.String())
	}
}

func TestCorpusListNoManifest(t *testing.T) {
	c, err := newsblur.NewCacheOnlyClient(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer

	err = CorpusList(c, &buf, 0)
	if err == nil {
		t.Fatal("expected error when no manifest cached")
	}
	if !strings.Contains(err.Error(), "no cached hash manifest") {
		t.Errorf("error = %q, want mention of missing manifest", err.Error())
	}
}

func TestCorpusReadRichStory(t *testing.T) {
	c := testCacheOnlyClient(t)
	var buf bytes.Buffer

	if err := CorpusRead(c, "rich", &buf); err != nil {
		t.Fatalf("CorpusRead: %v", err)
	}

	output := buf.String()
	if !strings.HasPrefix(output, "Rich Story by Alice\n") {
		t.Errorf("output should start with header, got: %q", output[:min(len(output), 80)])
	}
	if !strings.Contains(output, "detailed article about Go programming") {
		t.Error("output missing stripped content")
	}
}

func TestCorpusReadStubSkipped(t *testing.T) {
	c := testCacheOnlyClient(t)
	var buf bytes.Buffer

	if err := CorpusRead(c, "stub", &buf); err != nil {
		t.Fatalf("CorpusRead: %v", err)
	}

	if buf.Len() != 0 {
		t.Errorf("stub story should produce empty output, got %d bytes: %q", buf.Len(), buf.String())
	}
}

func TestCorpusReadMissingKeyEmpty(t *testing.T) {
	c := testCacheOnlyClient(t)
	var buf bytes.Buffer

	if err := CorpusRead(c, "nonexistent", &buf); err != nil {
		t.Fatalf("CorpusRead: %v", err)
	}

	if buf.Len() != 0 {
		t.Errorf("missing key should produce empty output, got %d bytes", buf.Len())
	}
}

func TestCorpusReadOriginalTextFallback(t *testing.T) {
	c := testCacheOnlyClient(t)

	// Add original text for the stub story so it passes the min content threshold.
	originalContent := strings.Repeat("This is the full original article text with more detail. ", 10)
	otJSON := json.RawMessage(`{"original_text": ` + jsonString("<div>"+originalContent+"</div>") + `}`)
	if err := c.PutCachedOriginalText("stub", otJSON); err != nil {
		t.Fatalf("put original text: %v", err)
	}

	var buf bytes.Buffer
	if err := CorpusRead(c, "stub", &buf); err != nil {
		t.Fatalf("CorpusRead: %v", err)
	}

	output := buf.String()
	if buf.Len() == 0 {
		t.Fatal("expected output with original text fallback, got empty")
	}
	if !strings.HasPrefix(output, "Stub Story by Bob\n") {
		t.Errorf("output should start with header, got: %q", output[:min(len(output), 80)])
	}
	if !strings.Contains(output, "full original article text") {
		t.Error("output missing original text content")
	}
}

func TestCorpusReadNoAuthor(t *testing.T) {
	c, err := newsblur.NewCacheOnlyClient(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}

	richContent := "<p>" + strings.Repeat("Content without any author attribution in the metadata. ", 10) + "</p>"
	story := json.RawMessage(`{
		"story_hash": "noauthor",
		"story_title": "Authorless Post",
		"story_authors": "",
		"story_feed_id": 1,
		"story_date": "2024-01-01 00:00:00",
		"story_content": ` + jsonString(richContent) + `
	}`)

	manifest := json.RawMessage(`["noauthor"]`)
	if err := c.PutCachedStarredStoryHashes(manifest); err != nil {
		t.Fatalf("put manifest: %v", err)
	}
	if err := c.PutCachedStarredStory("noauthor", story); err != nil {
		t.Fatalf("put story: %v", err)
	}

	var buf bytes.Buffer
	if err := CorpusRead(c, "noauthor", &buf); err != nil {
		t.Fatalf("CorpusRead: %v", err)
	}

	lines := strings.SplitN(buf.String(), "\n", 2)
	if lines[0] != "Authorless Post" {
		t.Errorf("header = %q, want %q (no 'by' suffix)", lines[0], "Authorless Post")
	}
}
