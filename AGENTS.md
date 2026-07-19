# AGENTS.md

This file provides guidance to coding agents (Claude Code and others) when
working with code in this repository.

## Overview

Nebulous is a NewsBlur MCP server written in Go. It serves feed and story data
from a local persistent index, enabling Claude to interact with feeds, stories,
subscriptions, folders, and OPML import/export over JSON-RPC stdio. The same
local index is also exposed as a structured `newsblur://` tree to the
[cutting-garden](https://github.com/amarbel-llc/cutting-garden) capture/traversal
framework via `nebulous traversal-serve`, an RFC 0013 wire plugin cutting-garden's
own main binary spawns.

Built on `go-mcp` from `github.com/amarbel-llc/purse-first/libs/go-mcp`.

## Build & Run

``` sh
just build-go            # Debug build → build/debug/{nebulous,migrate-cache}
just build-go release    # Release build (stripped)
just build-nix           # Nix build (reproducible, generates plugin.json)
just install-dev         # Nix build + install MCP server to ~/.claude.json
just debug-verify-traversal-serve /path/to/cutting-garden  # RFC 0013 wire-plugin check
```

The Nix build uses `buildGoApplication` with `gomod2nix.toml` (not vendorHash).
After changing Go dependencies: `go mod tidy && gomod2nix` (the devShell's
go-sync-wrap hook regenerates `gomod2nix.toml` automatically after `go get` /
`go mod tidy`).

## Authentication

`NEWSBLUR_TOKEN` env var (NewsBlur session cookie) is required at runtime for
`serve mcp` and `fetch`. Store it in `.secrets.env` (gitignored, loaded by
direnv via `.envrc`). The subcommands `generate-plugin`, `hook`, `install-mcp`,
`corpus-*`, and `traversal-serve` read only the local store and do not
require a token. `traversal-serve` optionally becomes read-write when
`NEWSBLUR_TOKEN` is set: its plugin gains `NodeMutator` support
(`create_node`/`patch_node`/`delete_node`), mapping to
star/unstar/mark_read/mark_unread/unsubscribe/rename_feed/move_feed/folder
create/rename/delete/move, plus `ContainerCreator` (`create_node` against
the `feeds` root) for subscribe, since NewsBlur assigns the feed id
server-side (cutting-garden#143). Reads stay token-free either way.

## Architecture

    cmd/nebulous/main.go           Entry point: parses args, creates client, starts MCP server
    cmd/nebulous/capture.go        `nebulous capture` subcommand: the gap-filling capture scan
    cmd/nebulous/traversal_serve.go `nebulous traversal-serve`: RFC 0013 wire-plugin mode ---
                                    serves cgplugin.Plugin out-of-process, for a cutting-garden
                                    `[[plugins]]` stanza (`command = ["nebulous"]`,
                                    `schemes = ["newsblur"]`); the sole way newsblur:// reaches
                                    cutting-garden (cmd/nebulous-cg, the earlier linked-plugin
                                    binary, has been retired, nebulous#40)
    internal/0/madder/             In-process madder/go blob store (nebulous's named store)
    internal/0/manifest/           SHA256 manifest tracking (leaf package)
    internal/alfa/newsblur/        HTTP client wrapping NewsBlur REST API
      client.go                    Client struct, request helpers, cache access
      cache.go                     Madder-backed persistent store keyed by a SHA256 manifest
      bootstrap.go                 Shared "resolve XDG manifest path, init madder store" helpers
                                    (DefaultManifestPath/NewDefaultStore/NewDefaultCacheOnlyClient)
                                    every nebulous subcommand bootstraps through
      capture_state.go             Capture watermark + per-(hash,format) completion records
      feeds.go, stories.go, ...    One file per API domain
    internal/alfa/capture/         cutting-garden subprocess client for `nebulous capture`
      capture.go                   Shells out to `cutting-garden capture`, reads the receipt
                                    id directly off its tap-ndjson stdout
    internal/bravo/tools/          MCP tool registration + handlers
      registry.go                  RegisterAll() → *command.App + ResourceProvider
      read_index.go                ReadIndex read façade over feedIndex/storyStore (for the cg plugin)
      feeds.go                     feed_query tool (word search over feeds)
      story_store.go               Flat story store with typed records and word index
      story_query.go               Query engine with structured filters + word search
      story_query_tool.go          story_query MCP tool handler
      facets.go                    Aggregate counts by year/tag/feed/status
      reader.go                    Bulk mutation tools (batch mark_read, mark_feed_read, mark_all_read --
                                    the only bespoke mutation tools left; everything per-node retired to
                                    cgplugin's NodeMutator/ContainerCreator)
      import_export.go             OPML import/export
      resources.go                 MCP Resource provider with template URI resolution
      feed_index.go                In-memory word index over feed metadata
    internal/charlie/cgplugin/     cutting-garden newsblur:// scheme plugin
      plugin.go                    Plugin identity + Index injection (SetIndex)
      traversal.go                 Types / Roots / ListRoots (incl. per-format capture leaves)
      leaf.go                      ReadLeaf (story content/original/metadata/capture, feed metadata)
      facets.go                    FacetDescriber / FacetCounter / FacetLabeler / FacetVersioner
      mutate.go                    NodeMutator: story/feed/folder create/patch/delete (SetClient)
      create_child.go              ContainerCreator.CreateChild: subscribe (server-assigned feed id)
      schema.go                    BodyDescriber: writable-type payload schemas
      url.go                       newsblur:// URL build/parse

### cgplugin as a Wire Plugin

`internal/charlie/cgplugin.Plugin{}` (RootProvider/LeafReader/FacetDescriber/
FacetCounter/FacetVersioner/FacetLabeler/NodeMutator/ContainerCreator/
BodyDescriber) is served out-of-process via `nebulous traversal-serve` (RFC
0013): cutting-garden's own main binary spawns it over an AF_UNIX rendezvous
socket per a `[[plugins]]` config stanza and dispatches `newsblur://` through
it, so the tools appear as `cutting-garden_*` on cutting-garden's own MCP
child rather than a separate one. Capability advertisement is
type-assertion-driven on the cutting-garden SDK side (`pkgs/traversal_serve`),
so nothing here maintains its own capability list.

An earlier linked-plugin binary (`cmd/nebulous-cg`, injected in-process into
cutting-garden's `cgapp.Build()`) has been retired now that the wire-plugin
path is verified in production (nebulous#40). The bespoke `nebulous` MCP
(story_query/mark_*/opml) remains a separate, unrelated child either way.

### Three-Phase Architecture: Sync (+ Capture) + Serve

The server operates in two distinct modes; sync and capture are now both
phases of the same `fetch` command rather than separate processes:

- **`nebulous fetch`** (sync phase): Sequential CLI command that populates the
  local persistent store by fetching from the NewsBlur API. Handles rate
  limiting with adaptive backoff. Fetches feeds metadata, starred story pages,
  and original article text. This is the sole ingestion pipeline --- the MCP
  server and the cutting-garden plugin never hit the API for reads.

  `fetch` also runs a **capture phase** as its fourth step: a self-healing
  gap-filling scan over the local story corpus. For each configured format
  it shells out to `cutting-garden capture <store-id> web:<permalink>`
  (`internal/alfa/capture`), which drives chrest under the hood, and records
  the resulting receipt (parsed directly off the capture command's own
  tap-ndjson stdout) via `internal/alfa/newsblur/capture_state.go`. New stories
  only by default (a persisted watermark compared against `story.Date`).
  A failed capture simply has no receipt, so the next scan retries it ---
  no separate retry bookkeeping. Gated by its own interval
  (`NEBULOUS_CAPTURE_INTERVAL`, default `6h`, checked against a persisted
  `CaptureLastScanAt` timestamp) rather than fetch's own cadence, since the
  corpus scan behind the capture loop is real cost tied to the manifest's
  mtime; soft-skips if cutting-garden isn't on `PATH` or `-no-capture` was
  passed. `nebulous capture` also remains a standalone subcommand for manual
  invocations and `--backfill` (which overrides the watermark for one run,
  ignoring the interval gate).

- **MCP server** (serve phase): Reads exclusively from the local persistent
  store. In-memory indices (`feedIndex`, `storyStore`) rebuild themselves
  when the manifest file's mtime changes since the last check (a debounced
  staleness check, `internal/bravo/tools/stale_cache.go`), so a
  concurrently-running `nebulous fetch`'s new data becomes visible without
  restarting the server --- they no longer build once via `sync.Once` and
  never again. All query tools and resources operate against these local
  indices. `nebulous traversal-serve`'s cutting-garden plugin reads the same
  indices through `tools.ReadIndex`.

### Data Flow

Sync: `nebulous fetch` → `newsblur.Client` → HTTP to `newsblur.com/api/*` → JSON
response → persistent store (SHA256 manifest at `$XDG_DATA_HOME/nebulous/manifest.json`
+ a `nebulous` madder blob store).

Capture: `nebulous fetch`'s fourth phase (or the standalone `nebulous capture`
subcommand) → `tools.ReadIndex.Stories()` (eligible stories) →
`internal/alfa/capture.Client` → `cutting-garden capture <store> web:<url>` →
chrest → RFC 0002 receipt in the `nebulous` madder store → receipt id parsed
off stdout → completion record in the same persistent store.

Serve: MCP JSON-RPC (stdio) → `command.App` → `tools/*` handlers → in-memory
index (built from persistent store) → MCP response.

Traverse: `cutting-garden <cmd>` → RFC 0013 wire call → `nebulous traversal-serve`
→ cutting-garden SDK → `cgplugin` (RootProvider/LeafReader) → `tools.ReadIndex`
→ in-memory index → cutting-garden node/leaf (including `.../capture/{format}`
once a receipt is recorded).

### Key Patterns

- **Nil client convention**: `RegisterAll(nil)` is used for offline subcommands
  (`generate-plugin`, `hook`, `install-mcp`). Tool handlers and indices are only
  initialized when client is non-nil.
- **Story store**: `storyStore` holds all stories as typed records with a word
  acceleration index, built from cached starred story pages and rebuilt
  whenever the manifest file's mtime changes (see `stale_cache.go` above).
- **All newsblur client methods return `json.RawMessage`** --- parsing happens
  in tool handlers.
- **Persistent store**: a SHA256-keyed manifest plus a madder blob store under
  the XDG tree. The 1h TTL applies to API-fetched responses; the `fetch` command
  and index builders read without TTL checks for immutable content (original
  text, starred story pages).
- **Rate limiting**: `RateLimitError` type parses HTTP 429 + `Retry-After`
  header. `adaptiveBackoff` learns optimal wait times from rate limit bursts.

## Nix Flake

Follows the stable-first nixpkgs convention from the parent eng repo. Devenvs are
imported from `purse-first` (go + shell). `madder` is wired as a flake input
(devShell + bats fixtures only — `internal/0/madder` uses `madder/go` in-process,
no subprocess); `cutting-garden` is a vendored Go dependency (gomod2nix).

`nix/nixos-module.nix` + `nix/home-manager-module.nix` are the self-passing
producer modules (`circus-host-integration(7)`) exported as
`nixosModules.default` / `homeManagerModules.default`; `checks.modules-eval`
in `flake.nix` instantiates the NixOS module through a throwaway host to
catch option-type regressions at `nix flake check` time.
