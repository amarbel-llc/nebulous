// Package manifest holds the persistent cache manifest that maps nebulous's
// logical cache keys (SHA256 of URL+params) to opaque blob digests. Blob
// storage itself lives in madder; this package is only the key→digest
// index.
package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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

// staleCheckDebounce bounds how often Lookup/All stat the manifest file
// to check for another process's writes (nebulous serve mcp's Manifest
// used to never reload after its initial load, so a concurrently-running
// `nebulous fetch` was invisible until the server itself restarted).
// Debounced rather than checked on every call: story_store's build does
// tens of thousands of sequential Lookup calls, and a busy fetch run
// rewrites the whole file (bumping its mtime) on every single Record —
// without debouncing, a build racing an active fetch could reload
// mid-scan far more than once.
const staleCheckDebounce = 1 * time.Second

// Manifest is a persistent map from logical keys to ManifestEntries. It is
// serialized as a single JSON file and written atomically via tempfile +
// rename. Safe for concurrent use within a single process.
type Manifest struct {
	path    string
	mu      sync.Mutex
	entries map[string]ManifestEntry

	lastMtimeNanos int64
	lastCheckedAt  time.Time
}

// Path returns the on-disk location of the manifest's JSON file.
func (m *Manifest) Path() string { return m.path }

func NewManifest(path string) (*Manifest, error) {
	m := &Manifest{
		path:    path,
		entries: map[string]ManifestEntry{},
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	m.lastCheckedAt = time.Now()
	if fi, err := os.Stat(path); err == nil {
		m.lastMtimeNanos = fi.ModTime().UnixNano()
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

// refreshIfStale re-reads the manifest file if it changed on disk since
// it was last checked, picking up entries a concurrently-running process
// (e.g. `nebulous fetch`) wrote after this Manifest was constructed or
// last refreshed. The stat check itself is debounced (staleCheckDebounce)
// rather than performed on every call. Caller must hold m.mu. A stat or
// reload failure leaves the last-known-good in-memory entries untouched
// — staleness detection is best-effort, not a hard requirement.
func (m *Manifest) refreshIfStale() {
	if time.Since(m.lastCheckedAt) < staleCheckDebounce {
		return
	}
	m.lastCheckedAt = time.Now()

	fi, err := os.Stat(m.path)
	if err != nil {
		return
	}
	mtime := fi.ModTime().UnixNano()
	if mtime == m.lastMtimeNanos {
		return
	}
	if err := m.load(); err != nil {
		return
	}
	m.lastMtimeNanos = mtime
}

// ForceRefresh re-reads the manifest file immediately, bypassing the
// staleCheckDebounce gate. For a caller that already knows a rebuild is
// warranted (e.g. a higher-layer cache's own staleness check just fired)
// and needs Lookup/All calls made during that rebuild to observe
// manifest state at least as fresh as right now — otherwise this
// Manifest's own, independently-timed debounce window could still be
// unexpired and serve pre-write data into a rebuild the outer layer
// believes is now current, silently baking in staleness until the file's
// mtime next changes.
func (m *Manifest) ForceRefresh() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastCheckedAt = time.Time{}
	m.refreshIfStale()
}

func (m *Manifest) Lookup(key string) (ManifestEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshIfStale()
	e, ok := m.entries[key]
	return e, ok
}

// All returns a snapshot of every entry. Use for bulk operations (migration,
// audits); the returned map is a copy and safe to iterate without holding the
// manifest lock.
func (m *Manifest) All() map[string]ManifestEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshIfStale()
	return maps.Clone(m.entries)
}

func (m *Manifest) Record(key string, entry ManifestEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reloadLocked()
	m.entries[key] = entry
	return m.saveLocked()
}

// RecordBatch merges the given entries into the manifest and saves once.
// Use this for bulk operations (e.g. migration) to avoid the O(n^2) write
// pattern of calling Record in a loop.
func (m *Manifest) RecordBatch(entries map[string]ManifestEntry) error {
	if len(entries) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reloadLocked()
	for k, v := range entries {
		m.entries[k] = v
	}
	return m.saveLocked()
}

func (m *Manifest) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reloadLocked()
	delete(m.entries, key)
	return m.saveLocked()
}

// reloadLocked unconditionally re-reads the manifest file from disk into
// m.entries, bypassing staleCheckDebounce entirely (unlike
// refreshIfStale/ForceRefresh, which still only reload when the file's
// mtime moved). Record/RecordBatch/Delete each call this before merging
// their own change in and saving the whole map back -- without it, a
// process whose in-memory entries never picked up another process's
// meanwhile-committed write would overwrite that write on its own next
// save (nebulous#44; confirmed live as nebulous#54: a patch_node clearing
// a story's user_tags reported "applied" but a read-back immediately
// after intermittently still showed the pre-clear value, self-resolving
// on a repeat -- exactly this race, racing a concurrently-running
// `nebulous fetch`). This closes the specific "merge from a stale
// in-memory copy" gap; it does not eliminate the narrower TOCTOU window
// between this reload and saveLocked's write below, where a different
// process could still save in between -- #44's own doc comment already
// flags that a full fix needs a file lock or compare-and-swap, out of
// scope here. A reload failure leaves m.entries as-is, same as
// refreshIfStale's own best-effort handling. Caller must hold m.mu.
func (m *Manifest) reloadLocked() {
	if err := m.load(); err != nil {
		return
	}
	m.lastCheckedAt = time.Now()
	if fi, err := os.Stat(m.path); err == nil {
		m.lastMtimeNanos = fi.ModTime().UnixNano()
	}
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
	// os.CreateTemp always creates its file 0600 (Go stdlib, not subject
	// to umask) — widen to group-readable before the rename makes it
	// live, so a co-group reader (e.g. circus's mcp-origin, added to the
	// nebulous group specifically for this) can open it. Every save
	// replaces the file via this same tempfile+rename path, so a
	// narrower mode would silently reassert itself on the next write.
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("manifest: chmod: %w", err)
	}
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
	// Record our own write's mtime so the next same-process Lookup/All
	// doesn't see refreshIfStale notice the change and redundantly
	// reload data this process's in-memory entries already reflect.
	m.lastCheckedAt = time.Now()
	if fi, err := os.Stat(m.path); err == nil {
		m.lastMtimeNanos = fi.ModTime().UnixNano()
	}
	return nil
}
