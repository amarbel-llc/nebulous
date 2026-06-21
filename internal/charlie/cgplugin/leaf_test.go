package cgplugin

import (
	"context"
	"testing"
)

func TestReadLeafContent(t *testing.T) {
	index = newFakeIndex()
	t.Cleanup(func() { index = nil })

	got, ok, err := Plugin{}.ReadLeaf(context.Background(), mustURL(t, "newsblur://story/123:abc/content"))
	if err != nil {
		t.Fatalf("ReadLeaf(content): %v", err)
	}
	if !ok {
		t.Fatal("ReadLeaf(content) ok=false, want true")
	}
	if string(got.Raw) != "<p>hello</p>" {
		t.Errorf("Raw = %q, want %q", got.Raw, "<p>hello</p>")
	}
	if got.RawMimeType != htmlMime {
		t.Errorf("RawMimeType = %q, want %q", got.RawMimeType, htmlMime)
	}
	if got.Structured == nil {
		t.Error("Structured is nil, want the content view")
	}
}

func TestReadLeafOriginal(t *testing.T) {
	index = newFakeIndex()
	t.Cleanup(func() { index = nil })

	got, ok, err := Plugin{}.ReadLeaf(context.Background(), mustURL(t, "newsblur://story/123:abc/original"))
	if err != nil {
		t.Fatalf("ReadLeaf(original): %v", err)
	}
	if !ok {
		t.Fatal("ReadLeaf(original) ok=false, want true")
	}
	if string(got.Raw) != "<html>full</html>" {
		t.Errorf("Raw = %q, want %q", got.Raw, "<html>full</html>")
	}
}

// A populated container (the story node itself) is not a fetchable leaf:
// ReadLeaf returns ok=false so the consumer falls back to the child
// listing.
func TestReadLeafContainerNotALeaf(t *testing.T) {
	index = newFakeIndex()
	t.Cleanup(func() { index = nil })

	_, ok, err := Plugin{}.ReadLeaf(context.Background(), mustURL(t, "newsblur://story/123:abc"))
	if err != nil {
		t.Fatalf("ReadLeaf(story container): %v", err)
	}
	if ok {
		t.Error("ReadLeaf(story container) ok=true, want false (container is not a leaf body)")
	}
}

// An uncached story leaf yields ok=false (no error), matching today's
// "run nebulous fetch" behaviour.
func TestReadLeafUncachedOriginal(t *testing.T) {
	index = newFakeIndex()
	t.Cleanup(func() { index = nil })

	_, ok, err := Plugin{}.ReadLeaf(context.Background(), mustURL(t, "newsblur://story/999:zzz/original"))
	if err != nil {
		t.Fatalf("ReadLeaf(uncached): %v", err)
	}
	if ok {
		t.Error("ReadLeaf(uncached original) ok=true, want false")
	}
}
