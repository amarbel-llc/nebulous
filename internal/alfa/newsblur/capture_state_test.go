package newsblur

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCaptureLastScanAtRoundTrip(t *testing.T) {
	c := NewClient("test-token")
	if err := c.WithCache(filepath.Join(t.TempDir(), "manifest.json"), time.Hour, newMemSink()); err != nil {
		t.Fatalf("WithCache: %v", err)
	}

	if _, ok := c.CaptureLastScanAt(); ok {
		t.Fatal("expected no last-scan timestamp before any Put")
	}

	when := time.Now().Round(time.Second)
	if err := c.PutCaptureLastScanAt(when); err != nil {
		t.Fatalf("PutCaptureLastScanAt: %v", err)
	}

	got, ok := c.CaptureLastScanAt()
	if !ok {
		t.Fatal("expected a last-scan timestamp after Put")
	}
	if !got.Equal(when) {
		t.Errorf("CaptureLastScanAt = %v, want %v", got, when)
	}
}

// Unlike the watermark (write-once), the last-scan timestamp is rewritten
// on every capture-phase run — confirm a second Put actually overwrites
// rather than being ignored.
func TestCaptureLastScanAtOverwritable(t *testing.T) {
	c := NewClient("test-token")
	if err := c.WithCache(filepath.Join(t.TempDir(), "manifest.json"), time.Hour, newMemSink()); err != nil {
		t.Fatalf("WithCache: %v", err)
	}

	first := time.Now().Add(-time.Hour).Round(time.Second)
	if err := c.PutCaptureLastScanAt(first); err != nil {
		t.Fatalf("PutCaptureLastScanAt (first): %v", err)
	}
	second := time.Now().Round(time.Second)
	if err := c.PutCaptureLastScanAt(second); err != nil {
		t.Fatalf("PutCaptureLastScanAt (second): %v", err)
	}

	got, ok := c.CaptureLastScanAt()
	if !ok {
		t.Fatal("expected a last-scan timestamp")
	}
	if !got.Equal(second) {
		t.Errorf("CaptureLastScanAt = %v, want most recent write %v", got, second)
	}
}

func TestCaptureLastScanAtNoCacheIsNoop(t *testing.T) {
	c := NewClient("test-token") // no WithCache

	if _, ok := c.CaptureLastScanAt(); ok {
		t.Error("expected no last-scan timestamp with no cache attached")
	}
	if err := c.PutCaptureLastScanAt(time.Now()); err != nil {
		t.Errorf("PutCaptureLastScanAt with no cache attached should be a no-op, got error: %v", err)
	}
}
