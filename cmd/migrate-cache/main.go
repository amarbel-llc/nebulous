// Command migrate-cache is a one-shot tool that converts the legacy flat
// response cache at ~/.cache/nebulous/responses/ into the new blobstore
// layout at ~/.cache/nebulous/store/.
//
// Legacy entries are marked immutable (see discussion on issue #6): we cannot
// reliably distinguish TTL from non-TTL entries from the filename alone, and
// accepting stale-feeds-linger is preferable to losing the ~26k starred
// stories and original-text blobs to TTL expiry.
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
	"path/filepath"
	"strings"
	"time"

	"github.com/friedenberg/nebulous/internal/blobstore"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "print what would happen without writing")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "migrate-cache — one-shot migration from the legacy ~/.cache/nebulous/responses layout\n\n")
		fmt.Fprintf(os.Stderr, "Usage: migrate-cache [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Environment:\n")
		fmt.Fprintf(os.Stderr, "  NEBULOUS_BLOB_READ_CMD   optional; paired with WRITE_CMD selects external backend\n")
		fmt.Fprintf(os.Stderr, "  NEBULOUS_BLOB_WRITE_CMD  optional; paired with READ_CMD selects external backend\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("resolving home: %v", err)
	}
	cacheRoot := filepath.Join(home, ".cache", "nebulous")
	legacyDir := filepath.Join(cacheRoot, "responses")
	newDir := filepath.Join(cacheRoot, "store")

	info, err := os.Stat(legacyDir)
	if os.IsNotExist(err) {
		log.Printf("no legacy cache at %s — nothing to migrate", legacyDir)
		return
	}
	if err != nil {
		log.Fatalf("stat legacy dir: %v", err)
	}
	if !info.IsDir() {
		log.Fatalf("%s exists but is not a directory", legacyDir)
	}

	store, err := buildStore(newDir)
	if err != nil {
		log.Fatalf("building store: %v", err)
	}

	manifestPath := filepath.Join(newDir, "manifest.json")
	m, err := blobstore.NewManifest(manifestPath)
	if err != nil {
		log.Fatalf("loading manifest: %v", err)
	}

	migrated, skipped, err := migrate(context.Background(), legacyDir, store, m, *dryRun)
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Printf("migrated %d entries, skipped %d", migrated, skipped)

	if *dryRun {
		log.Println("[dry-run] not renaming legacy dir")
		return
	}

	archived := legacyDir + ".migrated-" + time.Now().UTC().Format("20060102-150405")
	if err := os.Rename(legacyDir, archived); err != nil {
		log.Fatalf("archiving legacy dir: %v", err)
	}
	log.Printf("legacy dir archived to %s", archived)
}

// buildStore returns an external-command store if both env vars are set,
// otherwise the filesystem store under newDir.
func buildStore(newDir string) (blobstore.Store, error) {
	readCmd := strings.TrimSpace(os.Getenv("NEBULOUS_BLOB_READ_CMD"))
	writeCmd := strings.TrimSpace(os.Getenv("NEBULOUS_BLOB_WRITE_CMD"))
	if readCmd == "" && writeCmd == "" {
		return blobstore.NewFilesystemStore(newDir), nil
	}
	if readCmd == "" || writeCmd == "" {
		return nil, fmt.Errorf("NEBULOUS_BLOB_READ_CMD and NEBULOUS_BLOB_WRITE_CMD must be set together")
	}
	return blobstore.NewExternalCommandStore(strings.Fields(readCmd), strings.Fields(writeCmd))
}

// migrate walks legacyDir, writing each SHA256-named file as a blob and
// recording a manifest entry marked immutable. Manifest entries are
// accumulated in memory and written in a single batch at the end to avoid
// the O(n^2) cost of per-entry saves.
func migrate(ctx context.Context, legacyDir string, store blobstore.Store, m *blobstore.Manifest, dryRun bool) (migrated, skipped int, err error) {
	dirEntries, err := os.ReadDir(legacyDir)
	if err != nil {
		return 0, 0, fmt.Errorf("reading legacy dir: %w", err)
	}
	log.Printf("found %d entries in %s", len(dirEntries), legacyDir)

	batch := make(map[string]blobstore.ManifestEntry, len(dirEntries))
	start := time.Now()
	for _, e := range dirEntries {
		if e.IsDir() {
			skipped++
			continue
		}
		name := e.Name()
		if !looksLikeLegacyKey(name) {
			skipped++
			continue
		}

		path := filepath.Join(legacyDir, name)
		info, err := e.Info()
		if err != nil {
			log.Printf("[skip] %s: stat: %v", name, err)
			skipped++
			continue
		}

		if dryRun {
			migrated++
			continue
		}

		f, err := os.Open(path)
		if err != nil {
			log.Printf("[skip] %s: open: %v", name, err)
			skipped++
			continue
		}
		digest, err := store.Write(ctx, f)
		f.Close()
		if err != nil {
			log.Printf("[skip] %s: store write: %v", name, err)
			skipped++
			continue
		}

		batch[name] = blobstore.ManifestEntry{
			Digest:    digest,
			WrittenAt: info.ModTime(),
			Immutable: true,
		}
		migrated++
		if migrated%500 == 0 {
			elapsed := time.Since(start)
			rate := float64(migrated) / elapsed.Seconds()
			log.Printf("[progress] migrated %d entries in %s (%.0f/s)", migrated, elapsed.Round(time.Millisecond), rate)
		}
	}

	if dryRun {
		return migrated, skipped, nil
	}

	log.Printf("writing manifest with %d entries...", len(batch))
	saveStart := time.Now()
	if err := m.RecordBatch(batch); err != nil {
		return migrated, skipped, fmt.Errorf("record batch: %w", err)
	}
	log.Printf("manifest saved in %s", time.Since(saveStart).Round(time.Millisecond))
	return migrated, skipped, nil
}

// looksLikeLegacyKey reports whether name matches the old cache's filename
// shape: a 64-character hex SHA-256. Anything else (temp files, dotfiles,
// accidents) is skipped.
func looksLikeLegacyKey(name string) bool {
	if len(name) != 64 {
		return false
	}
	for _, r := range name {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
