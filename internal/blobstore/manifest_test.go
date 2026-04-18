package blobstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManifestRecordAndLookup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	m, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}

	entry := ManifestEntry{Digest: "abc", WrittenAt: time.Now(), Immutable: true}
	if err := m.Record("k1", entry); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, ok := m.Lookup("k1")
	if !ok {
		t.Fatal("Lookup returned not-found for recorded key")
	}
	if got.Digest != "abc" || !got.Immutable {
		t.Errorf("Lookup returned %+v", got)
	}
}

func TestManifestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")

	m1, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().Round(time.Second)
	if err := m1.Record("k1", ManifestEntry{Digest: "abc", WrittenAt: when}); err != nil {
		t.Fatal(err)
	}

	// Reload
	m2, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := m2.Lookup("k1")
	if !ok {
		t.Fatal("reloaded manifest missing key")
	}
	if got.Digest != "abc" {
		t.Errorf("digest = %s, want abc", got.Digest)
	}
	if !got.WrittenAt.Equal(when) {
		t.Errorf("WrittenAt = %v, want %v", got.WrittenAt, when)
	}
}

func TestManifestDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	m, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Record("k1", ManifestEntry{Digest: "abc", WrittenAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("k1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Lookup("k1"); ok {
		t.Error("Lookup returned ok after Delete")
	}
}

func TestManifestLookupMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	m, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Lookup("nope"); ok {
		t.Error("Lookup returned ok for missing key")
	}
}

func TestManifestLoadsEmpty(t *testing.T) {
	// Nonexistent file should initialize empty, not error.
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	m, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Lookup("anything"); ok {
		t.Error("expected empty manifest")
	}
}

func TestManifestAtomicWrite(t *testing.T) {
	// The on-disk file should be valid JSON after Record returns.
	path := filepath.Join(t.TempDir(), "manifest.json")
	m, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Record("k1", ManifestEntry{Digest: "abc", WrittenAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Entries map[string]ManifestEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Errorf("on-disk manifest is invalid JSON: %v", err)
	}
	if _, ok := payload.Entries["k1"]; !ok {
		t.Error("on-disk manifest missing recorded key")
	}
}
