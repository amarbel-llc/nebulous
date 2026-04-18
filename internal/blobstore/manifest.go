package blobstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ManifestEntry maps a logical cache key to a blob digest plus metadata
// needed for TTL enforcement.
type ManifestEntry struct {
	Digest    string    `json:"digest"`
	WrittenAt time.Time `json:"written_at"`
	// Immutable entries bypass TTL checks (used for starred story content,
	// original article text, and the starred-hash manifest).
	Immutable bool `json:"immutable,omitempty"`
}

// Manifest is a persistent map from logical keys to ManifestEntries. It is
// serialized as a single JSON file and written atomically via tempfile +
// rename. Safe for concurrent use within a single process.
type Manifest struct {
	path    string
	mu      sync.Mutex
	entries map[string]ManifestEntry
}

func NewManifest(path string) (*Manifest, error) {
	m := &Manifest{
		path:    path,
		entries: map[string]ManifestEntry{},
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manifest) load() error {
	data, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("manifest: read %s: %w", m.path, err)
	}
	var payload struct {
		Entries map[string]ManifestEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("manifest: parse %s: %w", m.path, err)
	}
	if payload.Entries != nil {
		m.entries = payload.Entries
	}
	return nil
}

func (m *Manifest) Lookup(key string) (ManifestEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	return e, ok
}

func (m *Manifest) Record(key string, entry ManifestEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[key] = entry
	return m.saveLocked()
}

func (m *Manifest) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, key)
	return m.saveLocked()
}

func (m *Manifest) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return fmt.Errorf("manifest: mkdir: %w", err)
	}
	payload := struct {
		Entries map[string]ManifestEntry `json:"entries"`
	}{Entries: m.entries}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("manifest: marshal: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.path), ".manifest-*.json")
	if err != nil {
		return fmt.Errorf("manifest: tempfile: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("manifest: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("manifest: close: %w", err)
	}
	if err := os.Rename(tmpName, m.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("manifest: rename: %w", err)
	}
	return nil
}
