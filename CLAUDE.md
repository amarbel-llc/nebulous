# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with
code in this repository.

## Overview

Nebulous is a NewsBlur MCP server written in Go. It serves feed and story data
from a local persistent index, enabling Claude to interact with feeds, stories,
subscriptions, folders, and OPML import/export over JSON-RPC stdio.

Built on `go-mcp` from `github.com/amarbel-llc/purse-first/libs/go-mcp`.

## Build & Run

``` sh
just build              # Debug build → build/debug/nebulous
just build release      # Release build (stripped) → build/release/nebulous
just nix-build          # Nix build (reproducible, generates plugin.json)
just dev-install        # Nix build + install MCP server to ~/.claude.json
```

The Nix build uses `buildGoApplication` with `gomod2nix.toml` (not vendorHash).
After changing Go dependencies: `go mod tidy && gomod2nix`.

## Authentication

`NEWSBLUR_TOKEN` env var (NewsBlur session cookie) is required at runtime. Store
it in `.secrets.env` (gitignored, loaded by direnv via `.envrc`). The
subcommands `generate-plugin`, `hook`, and `install-mcp` do not require a token.

## Architecture

    cmd/nebulous/main.go          Entry point: parses args, creates client, starts MCP server
    internal/newsblur/             HTTP client wrapping NewsBlur REST API
      client.go                    Client struct, request helpers, cache access
      cache.go                     SHA256-keyed persistent store (~/.cache/nebulous/responses/)
      feeds.go, stories.go, ...    One file per API domain
    internal/tools/                MCP tool registration + handlers
      registry.go                  RegisterAll() → *command.App + ResourceProvider
      feeds.go                     feed_query tool (word search over feeds)
      story_store.go               Flat story store with typed records and word index
      story_query.go               Query engine with structured filters + word search
      story_query_tool.go          story_query MCP tool handler
      facets.go                    Aggregate counts by year/tag/feed/status
      reader.go                    Mutation tools (mark read/unread, star/unstar)
      subscriptions.go             subscribe/unsubscribe/rename_feed
      folders.go                   Folder management
      import_export.go             OPML import/export
      resources.go                 MCP Resource provider with template URI resolution
      feed_index.go                In-memory word index over feed metadata

### Two-Phase Architecture: Sync + Serve

The server operates in two distinct modes:

- **`nebulous fetch`** (sync phase): Sequential CLI command that populates the
  local persistent store by fetching from the NewsBlur API. Handles rate
  limiting with adaptive backoff. Fetches feeds metadata, starred story pages,
  and original article text. This is the sole ingestion pipeline --- the MCP
  server never hits the API for reads.

- **MCP server** (serve phase): Reads exclusively from the local persistent
  store. In-memory indices (`feedIndex`, `storyStore`) are built from cached
  responses on first use via `sync.Once`. All query tools and resources operate
  against these local indices.

### Data Flow

Sync: `nebulous fetch` → `newsblur.Client` → HTTP to `newsblur.com/api/*` → JSON
response → persistent store (`~/.cache/nebulous/responses/`)

Serve: MCP JSON-RPC (stdio) → `command.App` → `tools/*` handlers → in-memory
index (built from persistent store) → MCP response

### Key Patterns

- **Nil client convention**: `RegisterAll(nil)` is used for offline subcommands
  (`generate-plugin`, `hook`, `install-mcp`). Tool handlers and indices are only
  initialized when client is non-nil.
- **Story store**: `storyStore` holds all stories as typed records with a word
  acceleration index. Built once from cached starred story pages via
  `sync.Once`.
- **All newsblur client methods return `json.RawMessage`** --- parsing happens
  in tool handlers.
- **Persistent store**: SHA256-keyed files under `~/.cache/nebulous/responses/`.
  The 1h TTL applies to API-fetched responses; the `fetch` command and index
  builders read without TTL checks for immutable content (original text, starred
  story pages).
- **Rate limiting**: `RateLimitError` type parses HTTP 429 + `Retry-After`
  header. `adaptiveBackoff` learns optimal wait times from rate limit bursts.

## Nix Flake

Follows the stable-first nixpkgs convention from the parent eng repo: `nixpkgs`
= stable, `nixpkgs-master` = unstable. Devenvs are imported from `purse-first`
(go + shell).
