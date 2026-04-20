package archivelist

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/friedenberg/nebulous/internal/0/archive"
)

func writeRecord(t *testing.T, root, shelf, key, policyID string, rec *archive.Record) string {
	t.Helper()
	path := filepath.Join(root, shelf, key, policyID+".json")
	if err := archive.Write(path, rec); err != nil {
		t.Fatalf("archive.Write(%s): %v", path, err)
	}
	return path
}

func ptr(b bool) *bool { return &b }

func successCapture(name string) archive.Capture {
	return archive.Capture{
		Name: name,
		Spec: &archive.ArtifactRef{
			ID: "blake2b256-spec", Size: 10, MediaType: "application/json",
		},
		Payload: &archive.ArtifactRef{
			ID: "blake2b256-payload", Size: 100, MediaType: "text/plain",
			Normalized: ptr(true),
		},
	}
}

func failedCapture(name, kind, msg string) archive.Capture {
	return archive.Capture{
		Name:  name,
		Error: &archive.Error{Kind: kind, Message: msg},
	}
}

func newRecord(subject, url, policyID, capturedAt string, captures []archive.Capture) *archive.Record {
	return &archive.Record{
		Schema:     archive.Schema,
		Subject:    subject,
		URL:        url,
		PolicyID:   policyID,
		CapturedAt: capturedAt,
		Captures:   captures,
		Errors:     []archive.Error{},
	}
}

func TestWalk_empty(t *testing.T) {
	root := t.TempDir()
	got, err := Walk(Options{Root: root, Warn: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty root: got %d summaries, want 0", len(got))
	}
}

func TestWalk_missingRoot(t *testing.T) {
	got, err := Walk(Options{
		Root: "/definitely/does/not/exist/nebulous-test-" + t.Name(),
		Warn: func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("missing root should not error, got: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing root: got %d summaries, want 0", len(got))
	}
}

func TestWalk_decodesAndProjects(t *testing.T) {
	root := t.TempDir()
	writeRecord(t, root, "by-story", "6327282:5d1cf5", "default",
		newRecord("story:6327282:5d1cf5", "https://example.com/a", "default",
			"2026-04-20T12:00:00.000Z",
			[]archive.Capture{successCapture("text"), successCapture("pdf")},
		))
	writeRecord(t, root, "by-url", "sha256-abcd1234", "default",
		newRecord("url:sha256-abcd1234", "https://example.com/b", "default",
			"2026-04-20T13:00:00.000Z",
			[]archive.Capture{successCapture("text"), failedCapture("pdf", "fetch-failed", "boom")},
		))

	got, err := Walk(Options{Root: root, Warn: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d summaries, want 2: %+v", len(got), got)
	}

	// Sorted newest-first.
	if got[0].CapturedAt != "2026-04-20T13:00:00.000Z" {
		t.Errorf("sort order: first summary CapturedAt = %q, want newest", got[0].CapturedAt)
	}

	// URL summary: 1 ok / 2 total.
	var urlSummary *Summary
	for i := range got {
		if strings.HasPrefix(got[i].Subject, "url:") {
			urlSummary = &got[i]
		}
	}
	if urlSummary == nil {
		t.Fatal("url: summary not found")
	}
	if urlSummary.CapturesOK != 1 || urlSummary.CapturesTotal != 2 {
		t.Errorf("url summary captures: got %d/%d, want 1/2",
			urlSummary.CapturesOK, urlSummary.CapturesTotal)
	}

	// Story summary: 2 ok / 2 total.
	var storySummary *Summary
	for i := range got {
		if strings.HasPrefix(got[i].Subject, "story:") {
			storySummary = &got[i]
		}
	}
	if storySummary == nil {
		t.Fatal("story: summary not found")
	}
	if storySummary.CapturesOK != 2 || storySummary.CapturesTotal != 2 {
		t.Errorf("story summary captures: got %d/%d, want 2/2",
			storySummary.CapturesOK, storySummary.CapturesTotal)
	}
}

func TestWalk_subjectPrefixFilter(t *testing.T) {
	root := t.TempDir()
	writeRecord(t, root, "by-story", "6327282:5d1cf5", "default",
		newRecord("story:6327282:5d1cf5", "https://example.com/a", "default",
			"2026-04-20T12:00:00.000Z",
			[]archive.Capture{successCapture("text")},
		))
	writeRecord(t, root, "by-url", "sha256-aa", "default",
		newRecord("url:sha256-aa", "https://example.com/b", "default",
			"2026-04-20T12:00:00.000Z",
			[]archive.Capture{successCapture("text")},
		))

	got, err := Walk(Options{
		Root:          root,
		SubjectPrefix: "story:",
		Warn:          func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("prefix filter: got %d, want 1: %+v", len(got), got)
	}
	if got[0].Subject != "story:6327282:5d1cf5" {
		t.Errorf("filtered subject: got %q", got[0].Subject)
	}
}

func TestWalk_malformedFileWarnsAndContinues(t *testing.T) {
	root := t.TempDir()
	writeRecord(t, root, "by-story", "good", "default",
		newRecord("story:good", "https://example.com/a", "default",
			"2026-04-20T12:00:00.000Z",
			[]archive.Capture{successCapture("text")},
		))

	// Sibling malformed .json in the same shelf.
	badPath := filepath.Join(root, "by-story", "bad", "default.json")
	if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(badPath, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}

	var warned []string
	got, err := Walk(Options{
		Root: root,
		Warn: func(format string, args ...any) {
			warned = append(warned, format)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d summaries; malformed should be skipped not counted", len(got))
	}
	if len(warned) == 0 {
		t.Error("expected at least one warning for malformed file")
	}
}

func TestWriteJSONL(t *testing.T) {
	var buf bytes.Buffer
	in := []Summary{
		{Subject: "story:a", PolicyID: "p1", URL: "https://x/", CapturedAt: "2026-04-20T12:00:00.000Z", CapturesOK: 1, CapturesTotal: 1, Path: "/tmp/a.json"},
		{Subject: "url:b", PolicyID: "p1", URL: "https://y/", CapturedAt: "2026-04-20T11:00:00.000Z", CapturesOK: 0, CapturesTotal: 1, Path: "/tmp/b.json"},
	}
	if err := WriteJSONL(&buf, in); err != nil {
		t.Fatalf("WriteJSONL: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2; output=%q", len(lines), buf.String())
	}
	for i, want := range []string{`"subject":"story:a"`, `"subject":"url:b"`} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("line %d missing %s: %s", i, want, lines[i])
		}
	}
}

func TestWriteTable(t *testing.T) {
	var buf bytes.Buffer
	in := []Summary{
		{Subject: "story:a", PolicyID: "p1", CapturedAt: "2026-04-20T12:00:00.000Z", CapturesOK: 1, CapturesTotal: 1},
	}
	if err := WriteTable(&buf, in); err != nil {
		t.Fatalf("WriteTable: %v", err)
	}
	s := buf.String()
	if !strings.Contains(s, "SUBJECT") || !strings.Contains(s, "POLICY_ID") || !strings.Contains(s, "CAPTURES") {
		t.Errorf("table header missing columns: %q", s)
	}
	if !strings.Contains(s, "story:a") || !strings.Contains(s, "1/1") {
		t.Errorf("table body missing data: %q", s)
	}
	// Path should NOT appear in table output.
	if strings.Contains(s, ".json") {
		t.Errorf("table should not include path column, got: %q", s)
	}
}
