package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	cgts "code.linenisgreat.com/cutting-garden/pkgs/traversal_serve"
	"github.com/friedenberg/nebulous/internal/alfa/newsblur"
	"github.com/friedenberg/nebulous/internal/bravo/tools"
	"github.com/friedenberg/nebulous/internal/charlie/cgplugin"
)

// runTraversalServe implements the RFC 0013 wire-plugin side for
// newsblur://: the same cgplugin.Plugin{} cmd/nebulous-cg links in-process
// (RootProvider/LeafReader/FacetDescriber/FacetCounter/FacetVersioner/
// FacetLabeler/NodeMutator/ContainerCreator/BodyDescriber), served
// out-of-process so cutting-garden's main binary can dispatch newsblur://
// through a [[plugins]] traversalPlugins stanza instead of the separate
// nebulous-cg MCP child (nebulous#40). Capability advertisement is
// type-assertion-driven on the SDK side, so every interface cgplugin.Plugin
// implements is advertised automatically — no capability list to maintain
// here.
func runTraversalServe(ctx context.Context) int {
	token := os.Getenv("NEWSBLUR_TOKEN")
	client, err := buildTraversalServeClient(ctx, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nebulous traversal-serve: %v\n", err)
		return 1
	}

	cgplugin.SetIndex(tools.NewReadIndex(client))
	if token != "" {
		cgplugin.SetClient(client)
	}

	cookie, err := cgts.CookieFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	ln, sock, cleanup, err := cgts.ListenRendezvous()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer cleanup()

	line, err := cgts.AnnounceLine(cookie, cgts.Handshake{
		Version: cgts.SchemaV1,
		Network: cgts.HandshakeNetwork,
		Address: sock,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, err := os.Stdout.WriteString(line); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// stdin EOF is the RFC 0013 shutdown signal, armed before accept so a
	// host that dies (or a manual invocation with no host at all) unblocks
	// the accept via the listener close instead of hanging forever.
	servCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		cancel()
	}()
	go func() {
		<-servCtx.Done()
		_ = ln.Close()
	}()

	conn, err := ln.AcceptUnix()
	if err != nil {
		if servCtx.Err() != nil {
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if err := cgts.Serve(servCtx, conn, cgts.ServeConfig{
		Plugin: cgplugin.Plugin{},
		Info: cgts.PluginInfo{
			Name:    "nebulous",
			Version: "0.1.0",
		},
		// ConfigApply is intentionally left nil: nebulous has no TOML config
		// section today (NEWSBLUR_TOKEN + XDG paths already cover everything
		// it needs), so there is nothing for a config_toml payload to carry.
	}); err != nil {
		if servCtx.Err() != nil {
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// buildTraversalServeClient mirrors cmd/nebulous-cg's buildClient: a
// cache-only client when no token is set (reads still work; mutation stays
// unavailable since cgplugin.SetClient is never called), or a live+cache
// client otherwise — the same attachCache pattern this file's other
// subcommands (fetch, serve mcp) already use.
func buildTraversalServeClient(ctx context.Context, token string) (*newsblur.Client, error) {
	if token == "" {
		return newsblur.NewDefaultCacheOnlyClient(ctx)
	}
	client := newsblur.NewClient(token)
	if err := attachCache(ctx, client, 1*time.Hour); err != nil {
		return nil, fmt.Errorf("attaching cache: %w", err)
	}
	return client, nil
}
