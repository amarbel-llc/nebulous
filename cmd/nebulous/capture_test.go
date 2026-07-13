package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/friedenberg/nebulous/internal/alfa/newsblur"
)

// memSink is an in-memory newsblur.BlobSink for tests. Kept local per this
// codebase's established convention (see internal/bravo/tools/memsink_test.go)
// rather than a shared testing package.
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

func testFetchClient(t *testing.T) *newsblur.Client {
	t.Helper()
	c := newsblur.NewClient("test-token")
	if err := c.WithCache(filepath.Join(t.TempDir(), "manifest.json"), time.Hour, newMemSink()); err != nil {
		t.Fatalf("WithCache: %v", err)
	}
	return c
}

func TestCaptureDueFirstRun(t *testing.T) {
	c := testFetchClient(t)
	due, last := captureDue(c)
	if !due {
		t.Error("captureDue = false on first run, want true")
	}
	if !last.IsZero() {
		t.Errorf("lastScan = %v, want zero value", last)
	}
}

func TestCaptureDueWithinInterval(t *testing.T) {
	c := testFetchClient(t)
	if err := c.PutCaptureLastScanAt(time.Now()); err != nil {
		t.Fatalf("PutCaptureLastScanAt: %v", err)
	}
	due, _ := captureDue(c)
	if due {
		t.Error("captureDue = true immediately after a scan, want false")
	}
}

func TestCaptureDueIntervalElapsed(t *testing.T) {
	c := testFetchClient(t)
	if err := c.PutCaptureLastScanAt(time.Now().Add(-defaultCaptureInterval - time.Minute)); err != nil {
		t.Fatalf("PutCaptureLastScanAt: %v", err)
	}
	due, _ := captureDue(c)
	if !due {
		t.Error("captureDue = false after the interval elapsed, want true")
	}
}

func TestCaptureIntervalEnvOverride(t *testing.T) {
	t.Setenv("NEBULOUS_CAPTURE_INTERVAL", "30m")
	if got := captureInterval(); got != 30*time.Minute {
		t.Errorf("captureInterval = %v, want 30m", got)
	}
}

func TestCaptureIntervalEnvUnparseableFallsBackToDefault(t *testing.T) {
	t.Setenv("NEBULOUS_CAPTURE_INTERVAL", "not-a-duration")
	if got := captureInterval(); got != defaultCaptureInterval {
		t.Errorf("captureInterval = %v, want default %v", got, defaultCaptureInterval)
	}
}

func TestCaptureIntervalUnsetIsDefault(t *testing.T) {
	if got := captureInterval(); got != defaultCaptureInterval {
		t.Errorf("captureInterval = %v, want default %v", got, defaultCaptureInterval)
	}
}

// withFakeLookPath temporarily substitutes lookPathCuttingGarden and
// restores the original on cleanup, so this test doesn't depend on
// whether cutting-garden happens to be installed on PATH.
func withFakeLookPath(t *testing.T, found bool) {
	t.Helper()
	orig := lookPathCuttingGarden
	t.Cleanup(func() { lookPathCuttingGarden = orig })
	if found {
		lookPathCuttingGarden = func() (string, error) { return "/fake/bin/cutting-garden", nil }
	} else {
		lookPathCuttingGarden = func() (string, error) {
			return "", fmt.Errorf("exec: %q: executable file not found in $PATH", "cutting-garden")
		}
	}
}

func TestRunFetchCapturePhaseSkipsOnNoCaptureFlag(t *testing.T) {
	c := testFetchClient(t)
	withFakeLookPath(t, true)

	runFetchCapturePhase(context.Background(), c, defaultCaptureStoreId, nil, true)

	if _, ok := c.CaptureLastScanAt(); ok {
		t.Error("CaptureLastScanAt set despite -no-capture; the phase should never have run")
	}
}

func TestRunFetchCapturePhaseSkipsWhenCuttingGardenMissing(t *testing.T) {
	c := testFetchClient(t)
	withFakeLookPath(t, false)

	runFetchCapturePhase(context.Background(), c, defaultCaptureStoreId, nil, false)

	if _, ok := c.CaptureLastScanAt(); ok {
		t.Error("CaptureLastScanAt set despite cutting-garden missing from PATH; the phase should have soft-skipped")
	}
}

func TestRunFetchCapturePhaseSkipsWithinInterval(t *testing.T) {
	c := testFetchClient(t)
	withFakeLookPath(t, true)

	last := time.Now().Round(time.Second)
	if err := c.PutCaptureLastScanAt(last); err != nil {
		t.Fatalf("PutCaptureLastScanAt: %v", err)
	}

	runFetchCapturePhase(context.Background(), c, defaultCaptureStoreId, nil, false)

	got, ok := c.CaptureLastScanAt()
	if !ok {
		t.Fatal("expected a last-scan timestamp to still be present")
	}
	if !got.Equal(last) {
		t.Errorf("CaptureLastScanAt = %v, want unchanged %v (interval gate should have skipped the run)", got, last)
	}
}

// On a fresh client (no watermark yet), runCaptureLoop's first-run path
// establishes the watermark and returns immediately without attempting
// any actual capture (see cmd/nebulous/capture.go's runCaptureLoop) — so
// this exercises runFetchCapturePhase's full success path, including
// recording CaptureLastScanAt, without needing a real cutting-garden
// binary to actually invoke.
func TestRunFetchCapturePhaseRunsAndRecordsLastScan(t *testing.T) {
	c := testFetchClient(t)
	withFakeLookPath(t, true)

	before := time.Now()
	runFetchCapturePhase(context.Background(), c, defaultCaptureStoreId, nil, false)

	got, ok := c.CaptureLastScanAt()
	if !ok {
		t.Fatal("expected CaptureLastScanAt to be recorded after a successful run")
	}
	if got.Before(before) {
		t.Errorf("CaptureLastScanAt = %v, want at/after %v", got, before)
	}
	if _, hasWatermark := c.CaptureWatermark(); !hasWatermark {
		t.Error("expected the capture watermark to have been established by the first run")
	}
}
