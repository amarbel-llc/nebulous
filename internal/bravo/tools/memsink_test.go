package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
)

// memSink is an in-memory newsblur.BlobSink for tests. Mirrors the helper in
// internal/alfa/newsblur/cache_test.go; kept local here to avoid a shared
// testing package.
type memSink struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func newMemSink() *memSink { return &memSink{blobs: map[string][]byte{}} }

func (s *memSink) Read(id string, dst io.Writer) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blobs[id]
	if !ok {
		return false, nil
	}
	if _, err := dst.Write(b); err != nil {
		return false, err
	}
	return true, nil
}

func (s *memSink) Write(src io.Reader) (string, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, src); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	id := fmt.Sprintf("sha256test-%s", hex.EncodeToString(sum[:]))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobs[id] = buf.Bytes()
	return id, nil
}
