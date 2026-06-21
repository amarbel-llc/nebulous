# nebulous

NewsBlur MCP server and cutting-garden plugin — a local-first feed/story
index that serves NewsBlur data to Claude over JSON-RPC stdio, and exposes
that same index as a structured `newsblur://` tree to the
[cutting-garden](https://github.com/amarbel-llc/cutting-garden) capture/
traversal framework.

One Go module, two surfaces over one local store:

1. **NewsBlur MCP server** (`nebulous serve mcp`) — serves feeds, stories,
   subscriptions, folders, and OPML import/export from a local persistent
   index rather than the live API.
2. **cutting-garden newsblur plugin** (`nebulous-cg`) — exposes the same
   local index as a structured tree under the `newsblur://` URI scheme
   (feeds / stories / tags, with a per-story content + original leaf),
   implementing the cutting-garden plugin SDK's `RootProvider` /
   `RootLister` / `LeafReader`. Read-only; no token required.

## NewsBlur MCP server

Built on `go-mcp` from `amarbel-llc/purse-first`. Operates in two distinct
phases:

- **Sync** — `nebulous fetch` is the sole ingestion pipeline. It
  sequentially fetches feed metadata, starred stories, and original article
  text from the NewsBlur API, with adaptive backoff that learns from
  rate-limit bursts, and persists responses into the local store: a
  SHA256-keyed manifest (`$XDG_DATA_HOME/nebulous/manifest.json`) whose
  response bodies live in a dedicated `nebulous` madder blob store.
- **Serve** — `nebulous serve mcp` reads exclusively from that local store;
  it never hits the API for reads. In-memory feed and story indices
  (word-search accelerated) are built lazily from cached responses.
  Mutation tools (star/unstar, mark read/unread, subscribe/unsubscribe,
  folders) are the exception and call the NewsBlur API directly.

Query surface: `feed_query` and `story_query` tools (structured filters by
year/tag/feed/status plus word search), a facets resource
(`nebulous://stories/facets`), and per-story resources
(`nebulous://story/{hash}`, `…/content`, `…/original`).

## cutting-garden newsblur plugin

`nebulous-cg` is a cutting-garden CLI with the `newsblur://` scheme plugin
baked in (`internal/charlie/cgplugin`, over the `tools.ReadIndex` façade).
It serves the same local index as a content-addressable traversal tree:

```
newsblur://feeds                  the subscription list (→ feeds)
newsblur://stories                the starred-story corpus (→ stories)
newsblur://tags                   the tag dictionary (→ tags)
newsblur://feed/{id}              one feed's stories
newsblur://tag/{tag}              stories carrying a tag
newsblur://story/{hash}           a story → its content + original leaves
newsblur://story/{hash}/content   cached story_content (HTML, stripped)
newsblur://story/{hash}/original  cached original article text (HTML)
```

Discover and traverse it via the cutting-garden commands, e.g.
`nebulous-cg health` or `nebulous-cg list newsblur://feeds`. Reads only the
local cache — no NewsBlur token needed. (Facet-based aggregation over the
tree lands once the cutting-garden facet SDK is tagged.)

## CLI

```
nebulous serve mcp                    Start the MCP server over stdio
nebulous fetch                        Sync feeds, starred stories, original text
nebulous corpus-list / corpus-read    Starred-story corpus access (for maneater)
nebulous generate-plugin | hook | install-mcp
                                      Plugin/install plumbing (no token needed)
nebulous-cg <command>                 Drive the newsblur:// plugin (health, list, …)
```

## Auth & configuration

- `NEWSBLUR_TOKEN` (NewsBlur session cookie) is required for `serve mcp` and
  `fetch`. Store it in `.secrets.env` (gitignored, loaded by direnv). The
  corpus, plugin, and `nebulous-cg` subcommands read only the local store
  and need no token.

## Development

The build entrypoint is the justfile:

```sh
just build          # build-go + build-nix
just build-go       # debug build → build/debug/{nebulous,migrate-cache,nebulous-cg}
just build-nix      # reproducible Nix build (buildGoApplication + gomod2nix)
just test           # go tests + bats lanes (zz-tests_bats/)
just install-dev    # nix build + install MCP server config
just cg ...         # run nebulous-cg against the local cache (e.g. just cg list newsblur://feeds)
```

After changing Go dependencies: `go mod tidy && gomod2nix` (the devShell's
go-sync-wrap hook regenerates `gomod2nix.toml` automatically).
