package newsblur

import (
	"encoding/json"
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

// CaptureWatermark/CaptureLastScanAt used to each persist their own
// wrapper struct ({"since": "..."} / {"at": "..."}) before being
// consolidated onto a bare time.Time. krone already has a real
// CaptureWatermark entry on disk in the old shape (from an already-run
// `nebulous capture --backfill`) — getPersistedTime must still read it,
// or the next run silently establishes a brand-new watermark and
// discards the true original eligibility anchor. Simulates that by
// writing the legacy shape directly, bypassing PutCaptureWatermark
// (which only ever writes the new bare-time shape going forward).
func TestCaptureWatermarkReadsLegacyWrapperStructShape(t *testing.T) {
	c := NewClient("test-token")
	if err := c.WithCache(filepath.Join(t.TempDir(), "manifest.json"), time.Hour, newMemSink()); err != nil {
		t.Fatalf("WithCache: %v", err)
	}

	legacy := time.Now().Add(-30 * 24 * time.Hour).Round(time.Second)
	raw, err := json.Marshal(struct {
		Since time.Time `json:"since"`
	}{Since: legacy})
	if err != nil {
		t.Fatalf("marshal legacy shape: %v", err)
	}
	if err := c.cache.putImmutable(c.captureWatermarkCacheKey(), raw); err != nil {
		t.Fatalf("putImmutable legacy shape: %v", err)
	}

	got, ok := c.CaptureWatermark()
	if !ok {
		t.Fatal("expected CaptureWatermark to read the legacy wrapper-struct shape")
	}
	if !got.Equal(legacy) {
		t.Errorf("CaptureWatermark = %v, want %v", got, legacy)
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
