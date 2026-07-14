// Command nebulous-cg is a cutting-garden CLI with the newsblur scheme
// plugin baked in. It exposes nebulous's local NewsBlur index as a
// structured tree under newsblur:// (cg list / mcp / health), reading
// only the local cache — no NewsBlur token required. Reads never need a
// token; if NEWSBLUR_TOKEN is set, mutations (create_node/patch_node/
// delete_node) also become available, backed by the same live+cache
// client `nebulous serve mcp`/`nebulous fetch` use.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	cgapp "code.linenisgreat.com/cutting-garden/pkgs/cgapp"
	"github.com/friedenberg/nebulous/internal/0/madder"
	"github.com/friedenberg/nebulous/internal/alfa/newsblur"
	"github.com/friedenberg/nebulous/internal/bravo/tools"
	"github.com/friedenberg/nebulous/internal/charlie/cgplugin"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	token := os.Getenv("NEWSBLUR_TOKEN")
	client, err := buildClient(ctx, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nebulous-cg: %v\n", err)
		os.Exit(1)
	}

	// Inject the local NewsBlur read index before the plugin's
	// commands resolve any newsblur:// node.
	cgplugin.SetIndex(tools.NewReadIndex(client))

	// Mutations need a live client, not just a cache-backed one — only
	// wire NodeMutator's write path when a token was actually supplied.
	// Read traversal keeps working unchanged either way.
	if token != "" {
		cgplugin.SetClient(client)
	}

	os.Exit(cgapp.Build().Run(os.Args))
}

// buildClient constructs the client nebulous-cg's plugin reads (and,
// when token is non-empty, writes) through: a cache-only client if token
// is empty (today's default, unchanged), or a live+cache client
// otherwise — the same attachCache pattern cmd/nebulous's own
// `fetch`/`serve mcp` use.
func buildClient(ctx context.Context, token string) (*newsblur.Client, error) {
	manifestPath, store, err := openStore(ctx)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return newsblur.NewCacheOnlyClient(manifestPath, store)
	}
	client := newsblur.NewClient(token)
	if err := client.WithCache(manifestPath, 1*time.Hour, store); err != nil {
		return nil, fmt.Errorf("attaching cache: %w", err)
	}
	return client, nil
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

// openStore resolves the manifest path and initializes the madder blob
// store — the bootstrap buildClient's cache-only and live+cache branches
// both need before diverging on which kind of newsblur.Client to build.
func openStore(ctx context.Context) (manifestPath string, store *madder.Store, err error) {
	manifestPath = defaultManifestPath()
	if manifestPath == "" {
		return "", nil, fmt.Errorf("cannot resolve nebulous manifest path (set HOME or XDG_DATA_HOME)")
	}
	store, err = madder.NewStore(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("madder new store: %w", err)
	}
	if err := store.Init(); err != nil {
		return "", nil, fmt.Errorf("madder init: %w", err)
	}
	return manifestPath, store, nil
}
