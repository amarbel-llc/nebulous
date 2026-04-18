// Command migrate-cache is a one-shot tool that migrates nebulous's cache
// from the old blobstore layout (hex-sha256 digests under
// ~/.cache/nebulous/store/blobs/sha256/, manifest at
// ~/.cache/nebulous/store/manifest.json) into the new madder-backed layout
// (markl-ids, blobs stored in madder's own XDG tree, manifest at
// $XDG_DATA_HOME/nebulous/manifest.json).
//
// Each legacy entry is shelled through `madder write` and recorded in the
// new manifest with its original WrittenAt and Immutable flags preserved.
// The old blob tree and old manifest are left on disk untouched — this
// command rewrites but does not destroy.
//
// Re-running is idempotent: existing entries in the new manifest are not
// overwritten, so a second run only fills in keys that weren't migrated on
// the previous pass.
//
// This command is intentionally NOT included in flake.nix's subPackages and
// lives in the repo only — it is not part of the published binary.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/friedenberg/nebulous/internal/manifest"
	"github.com/friedenberg/nebulous/internal/madder"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "enumerate what would be migrated without writing")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "migrate-cache — one-shot migration from blobstore to madder\n\n")
		fmt.Fprintf(os.Stderr, "Usage: migrate-cache [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Reads from:\n")
		fmt.Fprintf(os.Stderr, "  $HOME/.cache/nebulous/store/manifest.json\n")
		fmt.Fprintf(os.Stderr, "  $HOME/.cache/nebulous/store/blobs/sha256/<hex>\n\n")
		fmt.Fprintf(os.Stderr, "Writes to:\n")
		fmt.Fprintf(os.Stderr, "  $XDG_DATA_HOME/nebulous/manifest.json (or ~/.local/share/nebulous/manifest.json)\n")
		fmt.Fprintf(os.Stderr, "  madder's own XDG tree (opaque; new entries only)\n\n")
		fmt.Fprintf(os.Stderr, "Existing keys in the new manifest are preserved — re-runs only fill in gaps.\n")
		fmt.Fprintf(os.Stderr, "The legacy tree is left intact; no files are deleted.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("resolving home: %v", err)
	}

	legacyManifestPath := filepath.Join(home, ".cache", "nebulous", "store", "manifest.json")
	legacyBlobRoot := filepath.Join(home, ".cache", "nebulous", "store", "blobs", "sha256")

	if _, err := os.Stat(legacyManifestPath); os.IsNotExist(err) {
		log.Printf("no legacy manifest at %s — nothing to migrate", legacyManifestPath)
		return
	} else if err != nil {
		log.Fatalf("stat legacy manifest: %v", err)
	}

	legacyManifest, err := manifest.NewManifest(legacyManifestPath)
	if err != nil {
		log.Fatalf("loading legacy manifest: %v", err)
	}

	newManifestPath := defaultNewManifestPath()
	if newManifestPath == "" {
		log.Fatal("cannot resolve new manifest path (set HOME or XDG_DATA_HOME)")
	}
	newManifest, err := manifest.NewManifest(newManifestPath)
	if err != nil {
		log.Fatalf("loading new manifest: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	store := madder.NewStore(ctx)
	if !*dryRun {
		if err := store.Init(); err != nil {
			log.Fatalf("madder init: %v", err)
		}
	}

	migrated, skipped, err := migrate(legacyBlobRoot, store, legacyManifest, newManifest, *dryRun)
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Printf("migrated %d entries, skipped %d", migrated, skipped)
}

// defaultNewManifestPath mirrors cmd/nebulous/main.go's resolution: honors
// $XDG_DATA_HOME, falls back to ~/.local/share.
func defaultNewManifestPath() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "nebulous", "manifest.json")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "nebulous", "manifest.json")
	}
	return ""
}

// migrate walks the legacy manifest and rewrites each hex-keyed entry into
// madder. Entries already present in the new manifest are preserved, making
// the operation idempotent on re-run.
func migrate(
	legacyBlobRoot string,
	store *madder.Store,
	legacyManifest, newManifest *manifest.Manifest,
	dryRun bool,
) (migrated, skipped int, err error) {
	entries := legacyManifest.All()
	log.Printf("found %d entries in legacy manifest", len(entries))

	batch := make(map[string]manifest.ManifestEntry, len(entries))
	start := time.Now()

	for key, entry := range entries {
		if !looksLikeLegacyDigest(entry.Digest) {
			skipped++
			continue
		}
		if _, exists := newManifest.Lookup(key); exists {
			skipped++
			continue
		}

		if dryRun {
			migrated++
			continue
		}

		blobPath := filepath.Join(legacyBlobRoot, entry.Digest)
		f, err := os.Open(blobPath)
		if err != nil {
			log.Printf("[skip] %s: open %s: %v", key, blobPath, err)
			skipped++
			continue
		}
		markl, err := store.Write(f)
		f.Close()
		if err != nil {
			log.Printf("[skip] %s: madder write: %v", key, err)
			skipped++
			continue
		}

		batch[key] = manifest.ManifestEntry{
			Digest:    markl,
			WrittenAt: entry.WrittenAt,
			Immutable: entry.Immutable,
		}
		migrated++
		if migrated%500 == 0 {
			elapsed := time.Since(start)
			rate := float64(migrated) / elapsed.Seconds()
			log.Printf("[progress] migrated %d in %s (%.0f/s)", migrated, elapsed.Round(time.Millisecond), rate)
		}
	}

	if dryRun {
		return migrated, skipped, nil
	}

	log.Printf("writing %d new manifest entries...", len(batch))
	saveStart := time.Now()
	if err := newManifest.RecordBatch(batch); err != nil {
		return migrated, skipped, fmt.Errorf("record batch: %w", err)
	}
	log.Printf("manifest saved in %s", time.Since(saveStart).Round(time.Millisecond))
	return migrated, skipped, nil
}

// looksLikeLegacyDigest reports whether s matches the old cache's hex-sha256
// shape: exactly 64 lowercase hex characters. markl-ids have a format-family
// prefix (e.g. "blake2b256-") and won't match.
func looksLikeLegacyDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
