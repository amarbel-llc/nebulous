# AGENTS.md

This file provides guidance to coding agents (Claude Code and others) when
working with code in this repository.

## Overview

Nebulous is a NewsBlur MCP server written in Go. It serves feed and story data
from a local persistent index, enabling Claude to interact with feeds, stories,
subscriptions, folders, and OPML import/export over JSON-RPC stdio. The same
local index is also exposed as a structured `newsblur://` tree to the
[cutting-garden](https://code.linenisgreat.com/cutting-garden) capture/traversal
framework via `nebulous traversal-serve`, an RFC 0013 wire plugin cutting-garden's
own main binary spawns.

Built on `go-mcp` from `code.linenisgreat.com/purse-first/libs/go-mcp`.

## Build & Run

``` sh
just build-go            # nix-built binaries linked → build/debug/{nebulous,migrate-cache}
just build-nix           # Nix build (reproducible, generates plugin.json)
just test-go             # godyn per-package Go tests (checks.nebulous-tests)
just debug-go-test internal/bravo/tools -test.run=TestX  # one package, dirty tree
just install-dev         # Nix build + install MCP server to ~/.claude.json
just debug-verify-traversal-serve /path/to/cutting-garden  # RFC 0013 wire-plugin check
just debug-verify-words-search /path/to/cutting-garden/bin/cutting-garden  # `_words` search, seeded corpus
```

Go dependencies live in `go.nix` (igloo FDR 0008, godyn(7) § GO.NIX MANIFEST):
there is no go.mod, go.sum or gomod2nix.toml in the checkout and no ambient
`go` in the devShell. The Nix build is `buildGoAuto { manifest = ./go.nix; }`,
which renders the go.mod inside nix. Fleet modules (cutting-garden, tap/go,
purse-first's go-mcp) are `flakeInputs` bridged from flake inputs, so bumping
one is a `flake.lock`-only edit (`just debug-flake-update-input <input> ...`).
Third-party changes go through the escape hatch, which runs the go command
inside nix and rewrites go.nix: `just codemod-go -- go get <module>@<version>`.

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
      feed_titles.go               Accumulating feed-id-to-title registry
                                    (SeenFeedTitles/PutSeenFeedTitles) that survives a feed
                                    dropping out of a later /reader/feeds fetch, unlike the
                                    request cache itself
      feeds.go, stories.go, ...    One file per API domain
    internal/alfa/wordindex/       On-disk word -> story-hash index (bbolt): Builder.WriteFile
                                    (temp file + rename), Open (read-only), Lookup
    internal/alfa/capture/         cutting-garden subprocess client for `nebulous capture`
      capture.go                   Shells out to `cutting-garden capture`, reads the receipt
                                    id directly off its tap-ndjson stdout
    internal/bravo/tools/          MCP tool registration + handlers
      registry.go                  RegisterAll() → *command.App + ResourceProvider
      read_index.go                ReadIndex read façade over feedIndex/storyStore (for the cg plugin)
      feeds.go                     feed_query tool (word search over feeds)
      story_store.go               Flat story store with typed records and word index
      word_index.go                BuildWordIndex (fetch side) + opening/staleness of the on-disk index
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
      traversal.go                 Types / Roots / ListRoots (incl. per-format capture leaves) /
                                    ListEnriched (EnrichedLister: ListRoots' same Facet-enriched
                                    story/feed nodes, pre-filtered -- enables bulk_mutate.go's sweep)
      leaf.go                      ReadLeaf (story content/original/metadata/capture, feed metadata)
      facets.go                    FacetDescriber / FacetCounter / FacetLabeler / FacetVersioner
      mutate.go                    NodeMutator: story/feed/folder create/patch/delete (SetClient)
      search.go                    Searcher (RFC 0016), words mode only: cutting-garden's
                                    `_words` field, answered from the on-disk word index via
                                    ReadIndex.SearchWords; the file's doc comment is the
                                    published tokenizer contract. Declines substring/regex
                                    (`_body*=` stays a walk) and, with no usable index,
                                    words mode too.
      bulk_mutate.go               BulkMutator (RFC 0017): one delegating line to
                                    cg.BestEffortBulkMutate (cutting-garden#197's shared best-effort
                                    dispatch helper) -- rejects atomic with
                                    cg.ErrBulkAtomicUnsupported since NewsBlur's REST API has no
                                    multi-object transaction primitive
      create_child.go              ContainerCreator.CreateChild: subscribe (server-assigned feed id)
      schema.go                    BodyDescriber: writable-type payload schemas
      url.go                       newsblur:// URL build/parse

### cgplugin as a Wire Plugin

`internal/charlie/cgplugin.Plugin{}` (RootProvider/LeafReader/FacetDescriber/
FacetCounter/FacetVersioner/FacetLabeler/NodeMutator/ContainerCreator/
BodyDescriber/BulkMutator/EnrichedLister/Searcher) is served out-of-process via
`nebulous traversal-serve` (RFC 0013): cutting-garden's own main binary spawns it
over an AF_UNIX rendezvous socket per a `[[plugins]]` config stanza and
dispatches `newsblur://` through it, so the tools appear as
`cutting-garden_*` on cutting-garden's own MCP child rather than a
separate one. Capability advertisement is type-assertion-driven on the
cutting-garden SDK side (`pkgs/traversal_serve`), so nothing here
maintains its own capability list.

An earlier linked-plugin binary (`cmd/nebulous-cg`, injected in-process into
cutting-garden's `cgapp.Build()`) has been retired now that the wire-plugin
path is verified in production (nebulous#40). The bespoke `nebulous` MCP
(story_query/mark_*/opml) remains a separate, unrelated child either way.

`BulkMutator` (`bulk_mutate.go`, RFC 0017, cutting-garden#191) batches the
same NodeMutator verbs -- an explicit changeset or a Root+Filter sweep --
through cutting-garden's `bulk_mutate` tool. Best-effort only: NewsBlur's
REST API has no multi-object transaction, so an `atomic` request is
rejected with `cg.ErrBulkAtomicUnsupported`, never silently downgraded.
Since cutting-garden#197, `BulkMutate` is one line delegating to
`cg.BestEffortBulkMutate(ctx, p, p, req)` -- the shared best-effort
dispatch helper caldav and jira also use, so this plugin can't drift
from the #182 patchedNothing-vs-applied partitioning or the sweep
decline-refusal write-safety contract by carrying its own copy. The
delegation requires `EnrichedLister` (`ListEnriched`, traversal.go)
alongside `NodeMutator`: it reuses ListRoots' same Facet-enriched
story/feed nodes, pre-filtered, and DECLINES (rather than returning an
empty match) for any node that isn't one of the facet-bearing containers
FacetCounts already recognizes -- a sweep over a declined root refuses
with an error instead of silently sweeping zero nodes.

### Three-Phase Architecture: Sync (+ Capture) + Serve

The server operates in two distinct modes; sync and capture are now both
phases of the same `fetch` command rather than separate processes:

- **`nebulous fetch`** (sync phase): Sequential CLI command that populates the
  local persistent store by fetching from the NewsBlur API. Handles rate
  limiting with adaptive backoff. Fetches feeds metadata, starred story pages,
  and original article text. This is the sole ingestion pipeline --- the MCP
  server and the cutting-garden plugin never hit the API for reads.

  `fetch` then runs a **refresh phase** (`cmd/nebulous/refresh.go`), because a
  starred story is otherwise downloaded exactly once and its tags and read
  state go stale. Every run it reads NewsBlur's per-story `starred_updated`
  stamps (`StarredHashTimestamps`; recorded in the store by
  `internal/alfa/newsblur/sync_state.go`) and re-downloads stories whose stamp
  moved, plus the cached holders of any tag NewsBlur's own tag counts no longer
  list (a tag deleted in the apps does not move the stamp). Read state has no
  cheap change signal, so every cached story is re-downloaded on a long
  interval (`NEBULOUS_REFRESH_FULL_INTERVAL`, default 168h; the first run, with
  no recorded stamps, is such a run); `-refresh-all` forces it, `-no-refresh`
  skips the phase. Progress is saved every `refreshChunk` stories so an
  interrupted re-sync resumes. It never fails the fetch.

  `fetch` then runs an **index phase** (`cmd/nebulous/index.go`) that
  (re)builds the on-disk word index (`internal/alfa/wordindex`, a bbolt file at
  `index/words-v1.bbolt` beside the manifest, `newsblur.WordIndexPath`) when the
  corpus changed or the file is missing, incompatible, or covers a different
  story set; `-no-index` skips it and `nebulous index` rebuilds it from the
  local cache on demand. The file is built into a temp file and swapped in by
  rename, so readers never need a lock and a failed build keeps the previous
  file (nebulous#59). Bump `wordTokenizerVersion`
  (`internal/bravo/tools/word_index.go`) whenever `extractWords`, `stripHTMLTags`
  or the stop-word list change, so old files are rejected instead of
  disagreeing with the in-memory build.

  `fetch` also runs a **capture phase** as its last step: a self-healing
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
  indices through `tools.ReadIndex`. `serve mcp`'s `story_query` answers word
  lookups from the on-disk word index when it opens cleanly (a stale one is
  served with a `note` in the result; the stale-cache check also watches the
  index file), and otherwise falls back to building the word map in memory.
  `traversal-serve` writes heap and goroutine profiles to
  `<user cache dir>/nebulous/pprof/` on `SIGUSR1`.

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
  `tools.ReadIndex` (traversal-serve, capture) uses `newMetadataStoryStore`
  instead: no word index, no body stripping, since it only reads metadata
  (nebulous#58; 2.17 GB RSS before). Memory harness:
  `just debug-go-test internal/bravo/tools -test.run=TestStoryStoreMemory -test.v -corpus-stories=3000`.
- **All newsblur client methods return `json.RawMessage`** --- parsing happens
  in tool handlers.
- **Persistent store**: a SHA256-keyed manifest plus a madder blob store under
  the XDG tree. The 1h TTL applies to API-fetched responses; the `fetch` command
  and index builders read without TTL checks for immutable content (original
  text, starred story pages).
- **Rate limiting**: `RateLimitError` type parses HTTP 429 + `Retry-After`
  header. `adaptiveBackoff` learns optimal wait times from rate limit bursts.

## Nix Flake

Follows the stable-first nixpkgs convention from the parent eng repo. `madder`
is wired as a flake input (devShell + bats fixtures only — `internal/0/madder`
uses `madder/go` in-process, no subprocess); `cutting-garden` is a flake input
whose Go module is bridged through `go.nix` `flakeInputs`. nebulous is itself
an RFC 0001 producer: `mkGoPkgs { manifest = ./go.nix; }` publishes `go-pkgs`
/ `go-pkgs-test`, and the binaries build from `go-pkgs-test`.

`nix/nixos-module.nix` + `nix/home-manager-module.nix` are the self-passing
producer modules (`circus-host-integration(7)`) exported as
`nixosModules.default` / `homeManagerModules.default`; `checks.modules-eval`
in `flake.nix` instantiates the NixOS module through a throwaway host to
catch option-type regressions at `nix flake check` time.
