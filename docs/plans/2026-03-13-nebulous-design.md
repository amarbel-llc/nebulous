# Nebulous: NewsBlur MCP Server

## Summary

Go MCP server exposing the NewsBlur API via go-lib-mcp's `command.App`.
Authenticates via `NEWSBLUR_TOKEN` env var. Direct `net/http` client, no
external dependencies beyond go-lib-mcp.

## Architecture

```
nebulous/
├── cmd/nebulous/main.go
├── internal/
│   ├── newsblur/          # HTTP API client
│   │   ├── client.go      # Client struct, baseURL, token, request helpers
│   │   ├── feeds.go       # Feed/subscription API methods
│   │   ├── stories.go     # Story retrieval methods
│   │   ├── reader.go      # Read/star/mark methods
│   │   ├── folders.go     # Folder management methods
│   │   ├── import_export.go
│   │   └── types.go       # Shared response/request types
│   └── tools/             # MCP tool registration + handlers
│       ├── registry.go    # RegisterAll(*newsblur.Client) → *command.App
│       ├── feeds.go
│       ├── stories.go
│       ├── reader.go
│       ├── subscriptions.go
│       ├── folders.go
│       └── import_export.go
├── flake.nix
├── go.mod
└── go.sum
```

Data flow: MCP JSON-RPC → tools handler → newsblur.Client method → HTTP →
NewsBlur API → JSON response → command.JSONResult.

## Authentication

- `NEWSBLUR_TOKEN` env var, read at startup
- Passed as cookie header: `Cookie: newsblur_sessionid=<token>`
- Server exits with error if env var is missing

## Tool Inventory (Phase 1)

### feeds.go (ReadOnly)

| Tool | Description | Key Params |
|------|-------------|------------|
| feed_list | List all subscribed feeds | include_favicons, flat, update_counts |
| feed_search | Search for a feed by URL | address |
| feed_stats | Statistics for a feed | feed_id |
| feed_autocomplete | Autocomplete feed search | term |

### stories.go (ReadOnly)

| Tool | Description | Key Params |
|------|-------------|------------|
| story_feed | Stories for a single feed | feed_id, page, order, read_filter, query |
| story_river | Stories across multiple feeds | feed_ids, page |
| story_starred | Saved/starred stories | page, tag, query |
| story_unread_hashes | Hashes of all unread stories | — |
| story_original_text | Full original text of a story | story_hash |

### reader.go (Write)

| Tool | Description | Key Params |
|------|-------------|------------|
| mark_read | Mark stories as read | story_hashes |
| mark_unread | Mark story as unread | story_hash |
| star | Star/save a story | story_hash, user_tags |
| unstar | Unstar a story | story_hash |
| mark_feed_read | Mark entire feed as read | feed_id |
| mark_all_read | Mark everything as read | days |

### subscriptions.go (Write)

| Tool | Description | Key Params |
|------|-------------|------------|
| subscribe | Subscribe to a feed | url, folder |
| unsubscribe | Unsubscribe from a feed | feed_id, in_folder |
| rename_feed | Rename a feed | feed_id, feed_title |

### folders.go (Write)

| Tool | Description | Key Params |
|------|-------------|------------|
| folder_create | Create a folder | folder_name, parent_folder |
| folder_rename | Rename a folder | folder_name, new_folder_name |
| folder_delete | Delete a folder | folder_name |
| move_feed | Move feed to folder | feed_id, in_folder, to_folder |
| move_folder | Move folder into folder | folder_name, in_folder, to_folder |

### import_export.go

| Tool | Description | Key Params |
|------|-------------|------------|
| opml_export | Download OPML of subscriptions | — |
| opml_import | Import from OPML | opml_file (base64) |

## Context-Saving

- `story_feed`, `story_river`, `story_starred`: pass `page` through to
  NewsBlur's pagination
- `feed_list`: use `output.LimitArray` for local pagination (offset, limit)

## Build & Packaging

- Standalone Go module: `github.com/friedenberg/nebulous`
- Single dependency: `github.com/amarbel-llc/purse-first/libs/go-mcp`
- Nix flake with stable-first nixpkgs convention, `buildGoModule`
- `postInstall` runs `$out/bin/nebulous generate-plugin $out`
- Produces `plugin.json` and `mappings.json` for purse-first marketplace

## Future Phases

- Phase 2: Social tools (social_river, share_story, follow/unfollow)
- Phase 3: Classifier/intelligence training tools
