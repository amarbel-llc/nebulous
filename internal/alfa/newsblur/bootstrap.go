package newsblur

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/friedenberg/nebulous/internal/0/madder"
)

// DefaultManifestPath resolves the nebulous manifest location under XDG
// conventions ($XDG_DATA_HOME/nebulous/manifest.json, falling back to
// ~/.local/share/nebulous/manifest.json). Returns "" when no home directory
// can be resolved.
func DefaultManifestPath() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "nebulous", "manifest.json")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "nebulous", "manifest.json")
	}
	return ""
}

// NewDefaultStore initializes the madder blob store nebulous's persistent
// cache uses, bound to ctx — the "new store, then init it" sequence
// previously duplicated verbatim across cmd/nebulous and cmd/nebulous-cg.
func NewDefaultStore(ctx context.Context) (*madder.Store, error) {
	store, err := madder.NewStore(ctx)
	if err != nil {
		return nil, fmt.Errorf("madder new store: %w", err)
	}
	if err := store.Init(); err != nil {
		return nil, fmt.Errorf("madder init: %w", err)
	}
	return store, nil
}

// NewDefaultCacheOnlyClient builds a cache-only Client (no NewsBlur token,
// no HTTP access) against the default XDG-resolved manifest + madder store —
// the bootstrap every read-only consumer (nebulous-cg, traversal-serve,
// corpus-list/corpus-read) needs. Unlike the live-client path, a cache-only
// client has no fallback without a resolvable manifest, so an empty path is
// an error here rather than a silent no-cache degrade.
func NewDefaultCacheOnlyClient(ctx context.Context) (*Client, error) {
	manifestPath := DefaultManifestPath()
	if manifestPath == "" {
		return nil, fmt.Errorf("cannot resolve nebulous manifest path (set HOME or XDG_DATA_HOME)")
	}
	store, err := NewDefaultStore(ctx)
	if err != nil {
		return nil, err
	}
	return NewCacheOnlyClient(manifestPath, store)
}
