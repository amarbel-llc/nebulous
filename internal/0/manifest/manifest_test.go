package manifest

import (
	"encoding/json"
	"fmt"
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

// nebulous#41-followup: os.CreateTemp always creates its file 0600
// (unaffected by umask), and Rename preserves that mode — so every
// prior save left manifest.json unreadable by a co-group process
// (circus's mcp-origin, added to the nebulous group specifically to
// read it). Confirms the tempfile is widened to group-readable before
// the rename that makes it live.
func TestManifestSaveIsGroupReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	m, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Record("k1", ManifestEntry{Digest: "abc", WrittenAt: time.Now()}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o040 == 0 {
		t.Errorf("manifest mode = %o, want group-readable (0o040 bit set)", perm)
	}
}

// nebulous serve mcp used to build its Manifest once at process startup
// and never reload — a concurrently-running `nebulous fetch` process's
// writes were invisible until the server itself restarted (confirmed
// live on krone: nebulous://story/{hash}/original returned "not in
// cache" for hashes a direct blob read proved were already cached).
// This simulates that scenario directly: a SEPARATE Manifest instance
// over the same file stands in for the independent fetch process.
func TestManifestLookupPicksUpConcurrentWriterAfterStaleness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	m, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}

	other, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Record("k1", ManifestEntry{Digest: "abc", WrittenAt: time.Now()}); err != nil {
		t.Fatalf("other.Record: %v", err)
	}

	// Force m's debounce window to have already elapsed (rather than
	// sleeping staleCheckDebounce in the test) so the next Lookup
	// actually rechecks the file's mtime.
	m.mu.Lock()
	m.lastCheckedAt = time.Time{}
	m.mu.Unlock()

	got, ok := m.Lookup("k1")
	if !ok {
		t.Fatal("Lookup didn't pick up the other process's write after staleness check")
	}
	if got.Digest != "abc" {
		t.Errorf("Digest = %q, want %q", got.Digest, "abc")
	}
}

// Within the debounce window, Lookup must NOT reload even if the file
// changed underneath it — the cost-bounding half of the fix (a busy
// fetch run rewrites the whole file on every single Record()).
func TestManifestLookupDoesNotReloadWithinDebounceWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	m, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}

	other, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Record("k1", ManifestEntry{Digest: "abc", WrittenAt: time.Now()}); err != nil {
		t.Fatalf("other.Record: %v", err)
	}

	// m's debounce window (started at construction, moments ago) hasn't
	// elapsed — it must not see the other process's write yet.
	if _, ok := m.Lookup("k1"); ok {
		t.Error("Lookup picked up a concurrent write within the debounce window, want debounced")
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

func TestManifestRecordBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	m, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().Round(time.Second)
	batch := map[string]ManifestEntry{
		"k1": {Digest: "d1", WrittenAt: when, Immutable: true},
		"k2": {Digest: "d2", WrittenAt: when},
		"k3": {Digest: "d3", WrittenAt: when},
	}
	if err := m.RecordBatch(batch); err != nil {
		t.Fatalf("RecordBatch: %v", err)
	}

	for k, want := range batch {
		got, ok := m.Lookup(k)
		if !ok {
			t.Errorf("Lookup(%s) missing", k)
			continue
		}
		if got.Digest != want.Digest || got.Immutable != want.Immutable {
			t.Errorf("Lookup(%s) = %+v, want %+v", k, got, want)
		}
	}

	// Empty batch is a no-op and must not error.
	if err := m.RecordBatch(nil); err != nil {
		t.Errorf("RecordBatch(nil): %v", err)
	}

	// Reload and verify.
	m2, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	for k := range batch {
		if _, ok := m2.Lookup(k); !ok {
			t.Errorf("reloaded manifest missing %s", k)
		}
	}
}

func TestManifestRecordBatchLarge(t *testing.T) {
	// Repro for observed migration bug: batch of 55k entries resulted in
	// only ~50k entries persisted. Either saveLocked silently dropped
	// data or RecordBatch did.
	path := filepath.Join(t.TempDir(), "manifest.json")
	m, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	const n = 55000
	batch := make(map[string]ManifestEntry, n)
	when := time.Now()
	for i := 0; i < n; i++ {
		// Use a 64-char hex-like key to mirror real migration input.
		key := fmt.Sprintf("%064x", i)
		batch[key] = ManifestEntry{
			Digest:    fmt.Sprintf("%064x", i),
			WrittenAt: when,
			Immutable: true,
		}
	}
	if err := m.RecordBatch(batch); err != nil {
		t.Fatalf("RecordBatch: %v", err)
	}

	// Reload from disk; count entries.
	m2, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	m2.mu.Lock()
	got := len(m2.entries)
	m2.mu.Unlock()
	if got != n {
		t.Fatalf("reloaded manifest has %d entries, want %d (lost %d)", got, n, n-got)
	}
}

func TestManifestRecordBatchRealKeys(t *testing.T) {
	// Reads the legacy (pre-migration) dir if present and tries RecordBatch
	// with the exact same keys. Skips if the dir doesn't exist (CI, etc.).
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	legacy := filepath.Join(home, ".cache", "nebulous", "responses.migrated-20260418-130055")
	if _, err := os.Stat(legacy); err != nil {
		t.Skipf("legacy dir not present: %v", err)
	}
	entries, err := os.ReadDir(legacy)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("read %d entries from legacy", len(entries))

	path := filepath.Join(t.TempDir(), "manifest.json")
	m, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	batch := make(map[string]ManifestEntry, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) != 64 {
			continue
		}
		batch[name] = ManifestEntry{
			Digest:    name, // fake digest; irrelevant for the count test
			WrittenAt: time.Now(),
			Immutable: true,
		}
	}
	t.Logf("batch has %d entries", len(batch))
	if err := m.RecordBatch(batch); err != nil {
		t.Fatalf("RecordBatch: %v", err)
	}

	// Reload and count.
	m2, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	m2.mu.Lock()
	got := len(m2.entries)
	m2.mu.Unlock()
	if got != len(batch) {
		t.Fatalf("reloaded %d entries, batch had %d (lost %d)", got, len(batch), len(batch)-got)
	}
}

func TestManifestRecordBatchRealPath(t *testing.T) {
	// Write to the same filesystem as the real cache to rule out a
	// filesystem/quota-specific issue seen during migration.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	cacheDir := filepath.Join(home, ".cache", "nebulous")
	if _, err := os.Stat(cacheDir); err != nil {
		t.Skipf("no cache dir: %v", err)
	}
	testDir, err := os.MkdirTemp(cacheDir, "manifest-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(testDir) })

	path := filepath.Join(testDir, "manifest.json")
	m, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	const n = 55000
	batch := make(map[string]ManifestEntry, n)
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("%064x", i)
		batch[key] = ManifestEntry{Digest: key, WrittenAt: time.Now(), Immutable: true}
	}
	if err := m.RecordBatch(batch); err != nil {
		t.Fatalf("RecordBatch: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("manifest file size: %d bytes", info.Size())

	m2, err := NewManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	m2.mu.Lock()
	got := len(m2.entries)
	m2.mu.Unlock()
	if got != n {
		t.Fatalf("reloaded %d entries, want %d", got, n)
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
