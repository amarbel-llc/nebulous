# nebulous

NewsBlur MCP server and Web Capture Archive Protocol orchestrator —
local-first feed/story index, plus URL preservation into a
content-addressed blob store via
[chrest](https://github.com/amarbel-llc/chrest) +
[madder](https://github.com/amarbel-llc/madder).

One Go binary, two halves over one local store:

1. **NewsBlur MCP server** — serves feeds, stories, subscriptions,
   folders, and OPML import/export to Claude over JSON-RPC stdio, from
   a local persistent index rather than the live API.
2. **Archive orchestrator** — the reference orchestrator for
   [RFC 0001: Web Capture Archive Protocol](docs/rfcs/0001-web-capture-archive-protocol.md).
   Captures story URLs in multiple formats by driving chrest (the
   capturer), which streams every artifact into a madder
   content-addressed blob store (the writer).

## NewsBlur MCP server

Built on `go-mcp` from `amarbel-llc/purse-first`. Operates in two
distinct phases:

- **Sync** — `nebulous fetch` is the sole ingestion pipeline. It
  sequentially fetches feed metadata, starred stories, and original
  article text from the NewsBlur API, with adaptive backoff that
  learns from rate-limit bursts, and persists responses into the local
  store: a SHA256-keyed manifest (`$XDG_DATA_HOME/nebulous/manifest.json`)
  whose response bodies live in a dedicated `nebulous` blob store
  inside madder's XDG tree.
- **Serve** — `nebulous serve mcp` reads exclusively from that local
  store; it never hits the API for reads. In-memory feed and story
  indices (word-search accelerated) are built lazily from cached
  responses. Mutation tools (star/unstar, mark read/unread,
  subscribe/unsubscribe, folders) are the exception and call the
  NewsBlur API directly.

Query surface: `feed_query` and `story_query` tools (structured
filters by year/tag/feed/status plus word search), a facets resource
(`nebulous://stories/facets`), and per-story resources
(`nebulous://story/{hash}`, `…/content`, `…/original`).

## Archive orchestrator

What's implemented (see `cmd/nebulous/archive.go` and
`internal/alfa/{orchestrator,capturer,archivelist,policy}/`):

- **Capture pipeline** — for each (subject, policy) pair the
  orchestrator expands the policy's URL template (Go `text/template`,
  strict mode, `{{.Story.Permalink}}` / `.Hash` / `.Title`), feeds a
  batch-input JSON document to `chrest` on stdin, and `chrest` streams
  each artifact through `madder write` into the `nebulous` blob store.
  Per capture this yields a content-addressed spec, payload, and
  (when `split` is true) envelope artifact.
- **Archive records** — plain-JSON records written atomically to
  `$XDG_DATA_HOME/nebulous/archives/<subject>/<policy_id>.json`,
  cross-referencing the artifact markl IDs. Prior records are
  preserved in a madder-backed history store before being replaced.
- **Capture policy** — user-authored TOML at
  `$XDG_CONFIG_HOME/nebulous/nebulous.toml` (`[[policy]]` +
  `[[policy.capture]]`); a starter covering the Firefox-compatible
  formats (text, pdf, screenshot, html-monolith, markdown-full,
  markdown-reader) ships in `docs/templates/` via `just archive-init`.
- **Orchestration ergonomics** — worker pool (`--jobs`), TTL gate that
  skips targets with a fresh fully-successful prior record (`--ttl`),
  circuit breaker that bails after 3 consecutive failures, and TAP-14
  streaming or JSON reports (`--format`).
- **Fetch integration** — `nebulous fetch` runs an archive-capture
  pass over newly-indexed stories by default (`--no-archive` to skip),
  so starring a story preserves it.

Status honesty: RFC 0001 is `proposed` and its schemas are an
exploratory `v0` series. ADR
[0001](docs/decisions/0001-archive-normalization-in-orchestrator.md)
proposes moving payload normalization from the capturer into the
orchestrator but is not yet ratified — today normalization lives in
chrest, and nebulous does none. The earlier single-file-HTML
snapshotting exploration
([FDR 0001](docs/features/0001-periodic-story-snapshotting.md),
monolith vs SingleFile) remains `exploring`; the RFC 0001 pipeline is
what shipped.

## CLI

```
nebulous serve mcp                    Start MCP server over stdio
nebulous fetch [--archive-jobs=N] [--no-archive]
                                      Sync feeds, starred stories, original text;
                                      then archive newly-indexed stories
nebulous archive-capture [TARGET...]  Capture story IDs (<feed>:<hash>) or URLs;
                                      `-` reads targets from stdin
nebulous archive-list [PREFIX]        List archive records (--format=auto|table|jsonl)
nebulous corpus-list / corpus-read    Starred-story corpus access (for maneater)
nebulous generate-plugin | hook | install-mcp
                                      Plugin/install plumbing (no token needed)
```

## Auth & configuration

- `NEWSBLUR_TOKEN` (NewsBlur session cookie) is required for
  `serve mcp` and `fetch`. Store it in `.secrets.env` (gitignored,
  loaded by direnv). The archive, corpus, and plugin subcommands read
  only the local store and need no token.
- Capture policy: `$XDG_CONFIG_HOME/nebulous/nebulous.toml`
  (`--policy` to override).
- Archive records: `$XDG_DATA_HOME/nebulous/archives`
  (`--archive-root` to override).

## Development

The build entrypoint is the justfile:

```sh
just build          # build-go + build-nix
just build-go       # debug build → build/debug/nebulous (ldflags-injects
                    #   flake-pinned madder + chrest paths)
just build-nix      # reproducible Nix build (buildGoApplication + gomod2nix)
just test           # go tests + bats lanes (zz-tests_bats/)
just install-dev    # nix build + install MCP server config
just archive-init   # drop the starter nebulous.toml
just archive ...    # one-shot archive-capture via prod paths
just archive-recent # archive the N most recent starred stories
```

After changing Go dependencies: `go mod tidy && gomod2nix`. RFC 0001
conformance tests live in `zz-tests_bats/` and inject the capturer and
writer binaries via `bats-emo`, so a different conforming
capturer/writer can run the same suite.
