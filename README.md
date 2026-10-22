# nebulous

NewsBlur MCP server and cutting-garden plugin — a local-first feed/story
index that serves NewsBlur data to Claude over JSON-RPC stdio, and exposes
that same index as a structured `newsblur://` tree to the
[cutting-garden](https://code.linenisgreat.com/cutting-garden) capture/
traversal framework.

One Go module, two surfaces over one local store:

1. **NewsBlur MCP server** (`nebulous serve mcp`) — serves feeds, stories,
   subscriptions, folders, and OPML import/export from a local persistent
   index rather than the live API.
2. **cutting-garden newsblur plugin** (`nebulous traversal-serve`) — exposes
   the same local index as a structured tree under the `newsblur://` URI
   scheme (feeds / stories / tags, with a per-story content + original leaf),
   implementing the cutting-garden plugin SDK's `RootProvider` /
   `RootLister` / `LeafReader`. Served as an RFC 0013 wire plugin —
   cutting-garden's own main binary spawns it over an AF_UNIX rendezvous
   socket per a `[[plugins]]` config stanza. Read-only by default; no token
   required. Optionally becomes read-write when `NEWSBLUR_TOKEN` is set or its
   cutting-garden config names an account (see Auth & configuration),
   implementing `NodeMutator` (`create`/`patch`/`delete` ops of cutting-garden's `mutate_nodes` —
   star/unstar, mark_read/mark_unread, replace/clear a story's user tags,
   unsubscribe, rename_feed/move_feed, folder create/rename/delete/move)
   and `ContainerCreator` (subscribe, via
   a temp-id `create` op in the feeds root — cutting-garden#143's
   server-assigned-identity shape).

## NewsBlur MCP server

Built on `go-mcp` from `purse-first` (`code.linenisgreat.com/purse-first`). Operates in two distinct
modes; sync and capture are now both phases of the same `fetch` command
rather than separate processes:

- **Sync (+ Capture)** — `nebulous fetch` is the sole ingestion pipeline. It
  sequentially fetches feed metadata, starred stories, and original article
  text from the NewsBlur API, then refreshes already-cached stories whose
  tags changed in NewsBlur (and, weekly, all of them for read state), with adaptive backoff that learns from
  rate-limit bursts, and persists responses into the local store: a
  SHA256-keyed manifest (`$XDG_DATA_HOME/nebulous/manifest.json`) whose
  response bodies live in a dedicated `nebulous` madder blob store. It also
  runs a **capture phase** as a fourth step: a self-healing gap-filling
  scan that, for each configured format (`--formats`, default
  `markdown-reader`), drives `cutting-garden capture <store-id>
  web:<permalink>` (which drives chrest under the hood) for any starred
  story that doesn't yet have a recorded receipt for that format, writing a
  full-page archive independent of NewsBlur's own (often truncated)
  `story_content`. New stories only by default, via a persisted watermark.
  A failed capture simply has no receipt, so the next scan retries it
  automatically — no separate retry bookkeeping. Gated by its own interval
  (`NEBULOUS_CAPTURE_INTERVAL`, default `6h`) rather than fetch's own sync
  cadence, since the corpus scan behind the capture loop is real cost tied
  to the manifest's mtime; soft-skips if cutting-garden isn't on `PATH` or
  `--no-capture` was passed. `nebulous capture` also remains a standalone
  subcommand for manual invocations and `--backfill` (captures the full
  existing corpus for one run, ignoring the interval gate, while still
  skipping pairs already captured).
- **Serve** — `nebulous serve mcp` reads exclusively from that local store;
  it never hits the API for reads. In-memory feed and story indices
  (word-search accelerated) rebuild themselves whenever the manifest file's
  mtime changes since the last check, so a concurrently-running
  `nebulous fetch`'s new data becomes visible without restarting the
  server. Mutation tools that call the NewsBlur API directly are the
  exception; only the ones with no single-node equivalent remain here —
  batch `mark_read`, whole-feed/whole-corpus `mark_feed_read`/
  `mark_all_read` (bulk operations no single addressable node can express).
  Every per-node mutation (star/unstar, read/unread, feed rename/move/
  unsubscribe/subscribe, folder create/rename/delete/move) is covered by
  the `nebulous traversal-serve` plugin's `NodeMutator`/`ContainerCreator`
  (described below) instead, and has retired from here.

Query surface: `feed_query` and `story_query` tools (structured filters by
year/tag/feed/status plus word search), a facets resource
(`nebulous://stories/facets`), and per-story resources
(`nebulous://story/{hash}`, `…/content`, `…/original`).

## cutting-garden newsblur plugin

`nebulous traversal-serve` serves the `newsblur://` scheme plugin
(`internal/charlie/cgplugin`, over the `tools.ReadIndex` façade) as an RFC
0013 wire plugin — cutting-garden's own main binary spawns it per a
`[[plugins]]` config stanza (`command = ["nebulous"]`, `schemes =
["newsblur"]`) and dispatches `newsblur://` through it, so the tools appear
under cutting-garden's own MCP surface rather than a separate child. It
serves the same local index as a content-addressable traversal tree:

```
newsblur://feeds                  the subscription list (→ feeds)
newsblur://stories                the starred-story corpus (→ stories)
newsblur://tags                   the tag dictionary (→ tags)
newsblur://feed/{id}              one feed's stories, plus its metadata leaf
newsblur://feed/{id}/metadata     cached feed subscription record (JSON)
newsblur://tag/{tag}              stories carrying a tag
newsblur://story/{hash}                    a story → its leaves below
newsblur://story/{hash}/content            cached story_content (HTML, stripped)
newsblur://story/{hash}/original           cached original article text (HTML)
newsblur://story/{hash}/metadata           bibliographic fields (authors, tags, date, …)
newsblur://story/{hash}/capture/{format}   a completed capture's receipt id + timestamp
```

The `capture/{format}` leaf only appears once `nebulous capture` has
recorded a receipt for that (story, format) pair — it is not a placeholder
for uncaptured formats. It exposes the receipt's markl-id and when it was
captured; it does not walk the RFC 0002 receipt merkle tree itself — a
madder-aware consumer resolves it further via `madder://blobs/<digest>`.

Discover and traverse it via the cutting-garden commands once the plugin is
wired up, e.g. `cutting-garden health` or `cutting-garden list
newsblur://feeds`. Reads only the local cache — no NewsBlur token needed.
`just debug-verify-traversal-serve /path/to/cutting-garden` drives the same
path locally against a real `cutting-garden` binary for a quick sanity
check.

When `NEWSBLUR_TOKEN` is set or an account is configured, the plugin also becomes read-write via
`NodeMutator` and `ContainerCreator` (`internal/charlie/cgplugin/mutate.go`,
`create_child.go`): `mutate_nodes` `create`/`patch`/`delete` ops on
`story/{hash}` (star/unstar, read/unread, and tags) and `feed/{id}` (rename/move/
unsubscribe); a temp-id `create` op in the `feeds` root (subscribe, via
cutting-garden#143's server-assigned-identity `CreateChild` — NewsBlur
assigns the feed id, so the tool reports the resulting `feed/{id}` URI
back). Folder nodes (`folder/{path}`, not yet listed by `ListRoots`) are
create/rename/move/delete-able the same way; their path segment reuses
NewsBlur's own nested-folder join convention (`"Parent - Child"`, the same
string already in `FeedRef.Folder`) rather than a slash-hierarchical URI.
`describe_node_types`' schema (`schema.go`) documents every writable
type's body shape.

Story tags are edited with a `mutate_nodes` `patch` op: `{"add_tags":[...],"remove_tags":[...]}`
reads the story's CURRENT tags from NewsBlur (never the local cache, which
never learns about tags edited in the NewsBlur apps), applies the set edit
and writes the result. `{"user_tags":[...],"force":true}` replaces the whole
set (re-starring with a different set drops the old one rather than merging;
an empty array clears it) and is refused without `force`, because a replace
built from the possibly stale local copy can erase tags. The two forms
cannot be combined. Tags are treated as a set (duplicates dropped) and must
be non-empty with no comma or whitespace (`cgplugin/tags.go`, the one place
that rule lives). A write NewsBlur rejects inside an HTTP 200 (`code: -1`)
is reported as an error and never patches the cache.

Tag nodes (`newsblur://tag/<name>`) are writable for USER tags:
a `patch` op `{"name":"new"}` renames the tag on every story that carries it
and a `delete` op removes it from every story (the stories stay starred).
Both first confirm against NewsBlur's own tag counts that the tag exists; a
rename onto an existing tag is refused unless `"force": true` (it would merge
the two). Feed-assigned story tags cannot be renamed or deleted.

Feed and story nodes also carry facet dimensions (`year`, `user_tag`,
`story_tag`, `feed`, `read`, `age_band` on stories; `folder`, `active` on
feeds) via the cutting-garden `FacetDescriber`/`FacetCounter`/`FacetLabeler`
capabilities — see `internal/charlie/cgplugin/facets.go`. `age_band` is a
volatile dimension (RFC 0012 §11.3): it buckets a story's published date
against the current day (`today`/`this-week`/`this-month`/`older`) rather
than fixed data alone.

## NixOS / home-manager

The flake exports producer modules (`circus-host-integration(7)`'s
self-passing convention, mirroring `cutting-garden`'s own module shape):

- `nixosModules.default` (`services.nebulous`) — installs `nebulous`, and
  runs `nebulous fetch` on a single periodic systemd
  timer (`fetchInterval`, default `1h`) so a host's local cache stays
  warm without a manual invocation. `environmentFile` is the secret seam
  for `NEWSBLUR_TOKEN` (a path to `VAR=value` lines — the value never
  enters the Nix store). This module does **not** define an MCP-serving
  unit: `nebulous serve mcp` is a moxy stdio child on hosts that expose
  it (Path 1, per `circus-host-integration(7)`), wired host-side by the
  consumer, not by this module.
  Supplying `chrestPackage` and `cuttingGardenPackage` (both nullable
  `types.package`, unset by default) additionally enables the gap-filling
  capture scan described above as a fourth phase of the same
  `nebulous fetch` run — no second timer. It's gated by its own interval
  (`captureInterval`, default `6h`, threaded in as `NEBULOUS_CAPTURE_INTERVAL`
  and checked against a persisted last-scan timestamp, since a no-op pass
  is cheap but the corpus scan behind it isn't) with `captureFormats`
  (default `[ "markdown-reader" ]`) and `captureStoreId` (default
  `"nebulous"`) passed through as `-formats`/`-store` flags. Both packages
  are external to nebulous's own flake, so the consumer (circus) supplies
  them as option values from its own chrest/cutting-garden flake inputs —
  a host that only wants NewsBlur sync (no captures) leaves them unset and
  `fetch` runs sync-only.
- `homeManagerModules.default` (`programs.nebulous`) — installs the
  binaries for interactive/workstation use, plus opt-in wiring for a local
  session: `moxy.enable` renders a moxyfile with the `serve mcp` child
  (`moxy.moxyfile` is its path, to `include` from your own moxyfile),
  `cuttingGarden.enable` adds the `newsblur://` plugin stanza to
  `programs.cutting-garden.plugins` (read-only; `cuttingGarden.writable` adds
  a piggy-backed account whose token is read on the first mutation), and
  `fetch.enable` runs `nebulous fetch`
  on a systemd user timer (`fetch.interval`, default `1h`). The top-level
  `tokenPassKey` (default `newsblur-api-token`) and `piggyStoreDir` (default
  unset) say which piggy entry and store all of these read the token from.

## CLI

```
nebulous serve mcp                    Start the MCP server over stdio
nebulous fetch [--formats f1,f2] [--store id] [--no-capture] [--no-index]
               [--no-refresh] [--refresh-all]
                                      Sync feeds/starred stories/original text; re-downloads cached
                                      stories whose tags changed in NewsBlur (and, weekly, all of
                                      them for read state: NEBULOUS_REFRESH_FULL_INTERVAL); also runs
                                      the gap-filling capture scan when its interval elapses
nebulous capture [--formats f1,f2] [--store id] [--backfill]
                                      Run the capture scan standalone, ignoring the interval gate
nebulous corpus-list / corpus-read    Starred-story corpus access (for maneater)
nebulous generate-plugin | hook | install-mcp
                                      Plugin/install plumbing (no token needed)
nebulous traversal-serve              RFC 0013 wire plugin: serve newsblur:// for a
                                      cutting-garden [[plugins]] stanza (not meant for
                                      direct interactive use — cutting-garden spawns it)
```

## Auth & configuration

- `NEWSBLUR_TOKEN` (NewsBlur session cookie) is required for `serve mcp` and
  `fetch`. Store it in `.secrets.env` (gitignored, loaded by direnv); when it
  is unset, nebulous reads it itself from `piggy pass show newsblur-api-token`
  (the nix package carries its own piggy); `NEBULOUS_TOKEN_PASS_KEY` names a
  different entry, and piggy's `PIGGY_STORE_DIR` picks the store. The
  corpus and `traversal-serve` subcommands read only the local store and
  need no token. `traversal-serve` optionally becomes read-write (mutations
  via `mutate_nodes`) when `NEWSBLUR_TOKEN` is set or the plugin's
  cutting-garden config has an account:

  ```toml
  [[nebulous.accounts]]
  name = "newsblur"
  password_source = "piggy"        # or "env"
  password_key = "newsblur-api-token"
  ```

  The same account shape caldav/fastmail use; the token is read the first
  time a mutation needs it. Without either, it stays read-only.

## Development

The build entrypoint is the justfile:

```sh
just build          # build-go + build-nix
just build-go       # nix-built binaries linked → build/debug/{nebulous,migrate-cache}
just build-nix      # reproducible Nix build (igloo godyn, from go.nix)
just test           # godyn Go tests + bats lanes (zz-tests_bats/)
just install-dev    # nix build + install MCP server config
just debug-verify-traversal-serve /path/to/cutting-garden
                    # RFC 0013 wire-plugin check against a real cutting-garden binary
```

Go dependencies live in `go.nix` (igloo FDR 0008); there is no go.mod in the
checkout. Change them through the escape hatch, which runs the go command inside
nix and rewrites go.nix: `just codemod-go -- go get <module>@<version>`.
