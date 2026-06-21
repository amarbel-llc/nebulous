// Command nebulous-cg is a cutting-garden CLI with the newsblur scheme
// plugin baked in. It exposes nebulous's local NewsBlur index as a
// structured tree under newsblur:// (cg list / mcp / health), reading
// only the local cache — no NewsBlur token required.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	cgapp "github.com/amarbel-llc/cutting-garden/pkgs/cgapp"
	"github.com/friedenberg/nebulous/internal/0/madder"
	"github.com/friedenberg/nebulous/internal/alfa/newsblur"
	"github.com/friedenberg/nebulous/internal/bravo/tools"
	"github.com/friedenberg/nebulous/internal/charlie/cgplugin"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client, err := buildCacheOnlyClient(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nebulous-cg: %v\n", err)
		os.Exit(1)
	}

	// Inject the local NewsBlur read index before the plugin's
	// commands resolve any newsblur:// node.
	cgplugin.SetIndex(tools.NewReadIndex(client))

	os.Exit(cgapp.Build().Run(os.Args))
}

// defaultManifestPath resolves the nebulous manifest location under XDG
// conventions, mirroring cmd/nebulous. Returns "" when no home resolves.
func defaultManifestPath() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "nebulous", "manifest.json")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "nebulous", "manifest.json")
	}
	return ""
}

// buildCacheOnlyClient constructs a token-free NewsBlur client that
// reads exclusively from the local madder-backed cache.
func buildCacheOnlyClient(ctx context.Context) (*newsblur.Client, error) {
	manifestPath := defaultManifestPath()
	if manifestPath == "" {
		return nil, fmt.Errorf("cannot resolve nebulous manifest path (set HOME or XDG_DATA_HOME)")
	}
	store := madder.NewStore(ctx)
	if err := store.Init(); err != nil {
		return nil, fmt.Errorf("madder init: %w", err)
	}
	return newsblur.NewCacheOnlyClient(manifestPath, store)
}
