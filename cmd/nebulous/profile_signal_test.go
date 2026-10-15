package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteProfilesWritesHeapAndGoroutine(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	dir, err := writeProfiles()
	if err != nil {
		t.Fatalf("writeProfiles: %v", err)
	}
	if want := filepath.Join(cache, "nebulous", "pprof"); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var heap, goroutine bool
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.Size() == 0 {
			t.Errorf("profile %s empty or unreadable (err=%v)", e.Name(), err)
		}
		heap = heap || strings.HasPrefix(e.Name(), "heap-")
		goroutine = goroutine || strings.HasPrefix(e.Name(), "goroutine-")
	}
	if !heap || !goroutine {
		t.Errorf("want heap and goroutine profiles, got %v", entries)
	}
}
