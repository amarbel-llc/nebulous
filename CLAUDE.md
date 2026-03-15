# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Nebulous is a NewsBlur MCP server written in Go. It wraps the NewsBlur REST API
as MCP tools, enabling Claude to interact with feeds, stories, subscriptions,
folders, and OPML import/export over JSON-RPC stdio.

Built on `go-mcp` from `github.com/amarbel-llc/purse-first/libs/go-mcp`.

## Build & Run

```sh
just build              # Debug build → build/debug/nebulous
just build release      # Release build (stripped) → build/release/nebulous
just nix-build          # Nix build (reproducible, generates plugin.json)
just dev-install        # Nix build + install MCP server to ~/.claude.json
```

The Nix build uses `buildGoApplication` with `gomod2nix.toml` (not vendorHash).
After changing Go dependencies: `go mod tidy && gomod2nix`.

## Authentication

`NEWSBLUR_TOKEN` env var (NewsBlur session cookie) is required at runtime.
Store it in `.secrets.env` (gitignored, loaded by direnv via `.envrc`).
The subcommands `generate-plugin`, `hook`, and `install-mcp` do not require a token.

## Architecture

```
cmd/nebulous/main.go          Entry point: parses args, creates client, starts MCP server
internal/newsblur/             HTTP client wrapping NewsBlur REST API
  client.go                    Client struct, request helpers
  cache.go                     SHA256-keyed response cache (~/.cache/nebulous/responses/, 1h TTL)
  feeds.go, stories.go, ...    One file per API domain
internal/tools/                MCP tool registration + handlers
  registry.go                  RegisterAll() → *command.App + ResourceProvider
  feeds.go, stories.go, ...    One file per tool domain (mirrors newsblur/ layout)
  feed_index.go                In-memory word index over feed metadata for lightweight discovery
  saved_story_index.go         In-memory word index over starred story content
  resources.go                 MCP Resource provider with template URI resolution
```

### Data Flow

MCP JSON-RPC (stdio) → `command.App` → `tools/*` handlers → `newsblur.Client`
→ HTTP to `newsblur.com/api/*` → JSON response (optionally cached) →
`output.LimitText()` (100KB / 2000 lines) → MCP response.

### Key Patterns

- **Nil client convention**: `RegisterAll(nil)` is used for offline subcommands
  (`generate-plugin`, `hook`, `install-mcp`). Tool handlers and indices are
  only initialized when client is non-nil.
- **In-memory indices**: `feedIndex` and `savedStoryIndex` use `sync.Once` for
  lazy initialization with fingerprint-based cache invalidation persisted to
  `~/.cache/nebulous/`.
- **All newsblur client methods return `json.RawMessage`** — parsing happens in
  tool handlers.
- **Rate limiting**: `RateLimitError` type parses HTTP 429 + `Retry-After` header.

## Nix Flake

Follows the stable-first nixpkgs convention from the parent eng repo:
`nixpkgs` = stable, `nixpkgs-master` = unstable. Devenvs are imported from
`purse-first` (go + shell).
