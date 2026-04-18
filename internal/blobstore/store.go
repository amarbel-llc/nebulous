// Package blobstore defines a pluggable content-addressed blob store used by
// nebulous's response cache. Blobs are keyed by a content digest; a separate
// Manifest layer maps logical keys (e.g. URL-derived hashes) to digests.
package blobstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Store reads and writes opaque blobs by content digest.
type Store interface {
	// Read streams the blob identified by digest to dst. The returned bool
	// is false if no blob with that digest is present; a nil error with
	// false still indicates a successful lookup (not an I/O failure).
	Read(ctx context.Context, digest string, dst io.Writer) (bool, error)

	// Write persists content read from src and returns its digest.
	// Implementations choose the digest scheme; the returned value is
	// opaque to callers.
	Write(ctx context.Context, src io.Reader) (string, error)

	// Has reports whether the store already holds a blob for digest.
	Has(ctx context.Context, digest string) (bool, error)
}

// FilesystemStore persists blobs under <root>/blobs/sha256/<hex-digest>. The
// digest is a hex-encoded SHA-256 of the blob content.
type FilesystemStore struct {
	root string
}

func NewFilesystemStore(root string) *FilesystemStore {
	return &FilesystemStore{root: root}
}

func (s *FilesystemStore) blobPath(digest string) string {
	return filepath.Join(s.root, "blobs", "sha256", digest)
}

func (s *FilesystemStore) Read(_ context.Context, digest string, dst io.Writer) (bool, error) {
	f, err := os.Open(s.blobPath(digest))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("blobstore: open %s: %w", digest, err)
	}
	defer f.Close()
	if _, err := io.Copy(dst, f); err != nil {
		return false, fmt.Errorf("blobstore: read %s: %w", digest, err)
	}
	return true, nil
}

func (s *FilesystemStore) Write(_ context.Context, src io.Reader) (string, error) {
	root := filepath.Join(s.root, "blobs", "sha256")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("blobstore: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(root, ".blob-*")
	if err != nil {
		return "", fmt.Errorf("blobstore: tempfile: %w", err)
	}
	tmpName := tmp.Name()
	hasher := sha256.New()
	tee := io.MultiWriter(tmp, hasher)
	if _, err := io.Copy(tee, src); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", fmt.Errorf("blobstore: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("blobstore: close: %w", err)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	fp := s.blobPath(digest)
	if err := os.Rename(tmpName, fp); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("blobstore: rename: %w", err)
	}
	return digest, nil
}

func (s *FilesystemStore) Has(_ context.Context, digest string) (bool, error) {
	_, err := os.Stat(s.blobPath(digest))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("blobstore: stat %s: %w", digest, err)
	}
	return true, nil
}
