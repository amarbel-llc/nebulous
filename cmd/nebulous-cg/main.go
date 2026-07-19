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

// buildClient constructs the client nebulous-cg's plugin reads (and, when
// token is non-empty, writes) through: a cache-only client if token is
// empty (today's default, unchanged), or a live+cache client otherwise.
func buildClient(ctx context.Context, token string) (*newsblur.Client, error) {
	if token == "" {
		return newsblur.NewDefaultCacheOnlyClient(ctx)
	}
	manifestPath, store, err := openStore(ctx)
	if err != nil {
		return nil, err
	}
	client := newsblur.NewClient(token)
	if err := client.WithCache(manifestPath, 1*time.Hour, store); err != nil {
		return nil, fmt.Errorf("attaching cache: %w", err)
	}
	return client, nil
}

// openStore resolves the manifest path and initializes the madder blob
// store for buildClient's live+cache branch (cache-only goes through
// newsblur.NewDefaultCacheOnlyClient instead).
func openStore(ctx context.Context) (manifestPath string, store *madder.Store, err error) {
	manifestPath = newsblur.DefaultManifestPath()
	if manifestPath == "" {
		return "", nil, fmt.Errorf("cannot resolve nebulous manifest path (set HOME or XDG_DATA_HOME)")
	}
	store, err = newsblur.NewDefaultStore(ctx)
	if err != nil {
		return "", nil, err
	}
	return manifestPath, store, nil
}
