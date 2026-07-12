package cgplugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/friedenberg/nebulous/internal/bravo/tools"
)

func TestFacetVersionStoriesTokensOnManifestMtime(t *testing.T) {
	fi := newFakeIndex()
	fi.manifestPath = filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(fi.manifestPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	index = fi
	t.Cleanup(func() { index = nil })

	ctx := context.Background()
	tok1, ok, err := Plugin{}.FacetVersion(ctx, mustURL(t, "newsblur://stories"))
	if err != nil || !ok {
		t.Fatalf("FacetVersion: ok=%v err=%v", ok, err)
	}

	tok1Again, ok, err := Plugin{}.FacetVersion(ctx, mustURL(t, "newsblur://stories"))
	if err != nil || !ok {
		t.Fatalf("FacetVersion repeat: ok=%v err=%v", ok, err)
	}
	if tok1 != tok1Again {
		t.Errorf("token unstable across an unchanged manifest: %q vs %q", tok1, tok1Again)
	}

	// Advance the manifest's mtime (Chtimes rather than a rewrite, so the
	// content stays trivial) and confirm the token moves.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(fi.manifestPath, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	tok2, ok, err := Plugin{}.FacetVersion(ctx, mustURL(t, "newsblur://stories"))
	if err != nil || !ok {
		t.Fatalf("FacetVersion after mtime bump: ok=%v err=%v", ok, err)
	}
	if tok2 == tok1 {
		t.Errorf("token did not move with the manifest mtime: %q", tok2)
	}
}

func TestFacetVersionStoriesNoManifestYet(t *testing.T) {
	fi := newFakeIndex()
	fi.manifestPath = filepath.Join(t.TempDir(), "manifest.json") // never written
	index = fi
	t.Cleanup(func() { index = nil })

	_, ok, err := Plugin{}.FacetVersion(context.Background(), mustURL(t, "newsblur://stories"))
	if err != nil {
		t.Fatalf("FacetVersion without a manifest: %v", err)
	}
	if ok {
		t.Error("ok = true against a nonexistent manifest, want false")
	}
}

func TestFacetVersionFeedTokensOnNT(t *testing.T) {
	fi := newFakeIndex()
	fi.feeds = []tools.FeedRef{{ID: "123", Title: "Example Feed", NT: 3}}
	index = fi
	t.Cleanup(func() { index = nil })

	tok, ok, err := Plugin{}.FacetVersion(context.Background(), mustURL(t, "newsblur://feed/123"))
	if err != nil || !ok {
		t.Fatalf("FacetVersion(feed): ok=%v err=%v", ok, err)
	}
	if tok != "3" {
		t.Errorf("token = %q, want \"3\" (the feed's NT)", tok)
	}

	fi.feeds[0].NT = 4
	tok2, ok, err := Plugin{}.FacetVersion(context.Background(), mustURL(t, "newsblur://feed/123"))
	if err != nil || !ok {
		t.Fatalf("FacetVersion(feed) after NT bump: ok=%v err=%v", ok, err)
	}
	if tok2 == tok {
		t.Errorf("token did not move with NT: %q", tok2)
	}
}

func TestFacetVersionUnknownFeedIsOkFalse(t *testing.T) {
	fi := newFakeIndex()
	index = fi
	t.Cleanup(func() { index = nil })

	_, ok, err := Plugin{}.FacetVersion(context.Background(), mustURL(t, "newsblur://feed/999"))
	if err != nil {
		t.Fatalf("FacetVersion(unknown feed): %v", err)
	}
	if ok {
		t.Error("ok = true for an unknown feed id, want false")
	}
}
