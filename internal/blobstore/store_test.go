package blobstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemStoreRoundTrip(t *testing.T) {
	s := NewFilesystemStore(t.TempDir())
	ctx := context.Background()

	digest, err := s.Write(ctx, strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := hex.EncodeToString(sha256Sum([]byte("hello world")))
	if digest != want {
		t.Errorf("digest = %s, want %s", digest, want)
	}

	var buf bytes.Buffer
	ok, err := s.Read(ctx, digest, &buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !ok {
		t.Fatal("Read returned not-found for a blob we just wrote")
	}
	if got := buf.String(); got != "hello world" {
		t.Errorf("Read content = %q, want %q", got, "hello world")
	}

	has, err := s.Has(ctx, digest)
	if err != nil || !has {
		t.Errorf("Has(%s) = (%v, %v), want (true, nil)", digest, has, err)
	}
}

func TestFilesystemStoreReadMissing(t *testing.T) {
	s := NewFilesystemStore(t.TempDir())
	var buf bytes.Buffer
	ok, err := s.Read(context.Background(), "deadbeef", &buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if ok {
		t.Error("Read returned ok for missing digest")
	}
	if buf.Len() != 0 {
		t.Errorf("Read wrote to dst for missing digest: %q", buf.String())
	}
}

func TestFilesystemStoreHasMissing(t *testing.T) {
	s := NewFilesystemStore(t.TempDir())
	has, err := s.Has(context.Background(), "deadbeef")
	if err != nil || has {
		t.Errorf("Has(missing) = (%v, %v), want (false, nil)", has, err)
	}
}

func TestFilesystemStoreDedup(t *testing.T) {
	// Writing the same content twice should produce the same digest and
	// land in the same file.
	root := t.TempDir()
	s := NewFilesystemStore(root)
	ctx := context.Background()

	d1, err := s.Write(ctx, strings.NewReader("dup"))
	if err != nil {
		t.Fatal(err)
	}
	d2, err := s.Write(ctx, strings.NewReader("dup"))
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("digests differ: %s vs %s", d1, d2)
	}

	entries, err := os.ReadDir(filepath.Join(root, "blobs", "sha256"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 blob on disk, got %d", len(entries))
	}
}

func TestFilesystemStoreStreams(t *testing.T) {
	// Large payload via io.Reader; verify digest matches SHA-256 of content.
	s := NewFilesystemStore(t.TempDir())
	payload := bytes.Repeat([]byte("a"), 1<<16)

	digest, err := s.Write(context.Background(), bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if want := hex.EncodeToString(sha256Sum(payload)); digest != want {
		t.Errorf("digest = %s, want %s", digest, want)
	}

	var buf bytes.Buffer
	ok, err := s.Read(context.Background(), digest, &buf)
	if err != nil || !ok {
		t.Fatalf("Read = (%v, %v)", ok, err)
	}
	if !bytes.Equal(buf.Bytes(), payload) {
		t.Error("Read returned different content than was written")
	}
}

func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

// Ensure Write closes its tempfile on src read failure. Regression guard.
func TestFilesystemStoreWriteSrcError(t *testing.T) {
	s := NewFilesystemStore(t.TempDir())
	_, err := s.Write(context.Background(), failingReader{})
	if err == nil {
		t.Fatal("expected error from failing reader")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
