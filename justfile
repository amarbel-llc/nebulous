
default: lint build test

lint: lint-fmt lint-worktree

build: build-go build-nix

test: test-go test-bats

codemod: codemod-fmt codemod-generate-facades codemod-migrate-cache

codemod-fmt: codemod-fmt-tree

# Read-only formatting + the eng preset's file-based linters, via the sandboxed
# checks.formatting derivation.
#
# check formatting and the eng file-based linters
[group('lint')]
lint-fmt:
  #!/usr/bin/env bash
  set -euo pipefail
  system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
  nix build ".#checks.${system}.formatting" --no-link --print-build-logs

# Impure eng checks (git remotes, sweatfile, agents-md) against the working
# tree; conformist comes from the devShell PATH.
#
# run the impure eng checks against the working tree
[group('lint')]
lint-worktree:
  #!/usr/bin/env bash
  set -euo pipefail
  cfg=$(nix build --no-link --print-out-paths '.#conformist-impure-config')
  conformist check --config-file "$cfg" --tree-root .

# Format the tree in place (repair mode) via `nix fmt`.
[group('codemod')]
codemod-fmt-tree:
  nix fmt

# Debug build of all nebulous binaries (nebulous, migrate-cache).
# Pass tag=release for a stripped production build.
#
# build all nebulous binaries
[group('build')]
build-go tag="debug":
  #!/usr/bin/env bash
  set -euo pipefail
  # Resolve the flake-pinned madder so its absolute path is
  # ldflags-injected into internal/0/madder.Bin — mirrors what the Nix
  # build does in flake.nix. Without this, debug builds would invoke
  # madder via PATH, where older user-profile binaries can shadow the
  # devShell's.
  madder_path=$(nix build --no-link --print-out-paths .#madder 2>/dev/null || true)
  extra_ldflags=""
  if [ -n "$madder_path" ]; then
    extra_ldflags="$extra_ldflags -X code.linenisgreat.com/nebulous/internal/0/madder.Bin=$madder_path/bin/madder"
  fi
  base_ldflags="{{if tag == "release" { "-s -w" } else { "" } }}"
  ldflags="$base_ldflags $extra_ldflags"
  gcflags="{{if tag == "release" { "" } else { "all=-N -l" } }}"
  gcflags_arg=()
  if [ -n "$gcflags" ]; then gcflags_arg=(-gcflags "$gcflags"); fi
  ldflags_arg=()
  # shellcheck disable=SC2086 -- word-split intentional: strips leading whitespace from concatenated flags
  if [ -n "$(echo $ldflags)" ]; then ldflags_arg=(-ldflags "$ldflags"); fi
  go build "${gcflags_arg[@]}" "${ldflags_arg[@]}" -o build/{{tag}}/nebulous       ./cmd/nebulous
  go build "${gcflags_arg[@]}" "${ldflags_arg[@]}" -o build/{{tag}}/migrate-cache  ./cmd/migrate-cache

# Reproducible nix build — the primary release artifact.
[group('build')]
build-nix:
  nix build --show-trace

# Run Go unit tests.
[group('test')]
test-go *args:
  go test {{args}} ./...

# Run the bats integration suite against the debug build.
[group('test')]
test-bats *args: build-go
  MIGRATE_CACHE_BIN="$(pwd)/build/debug/migrate-cache" \
  NEBULOUS_BIN="$(pwd)/build/debug/nebulous" \
    nix develop -c bats {{args}} zz-tests_bats/

# End-to-end RFC 0013 check: spawn `nebulous traversal-serve` as an
# out-of-process wire plugin from a REAL cutting-garden binary (via a
# [[plugins]] traversalPlugins stanza) and confirm newsblur:// traversal +
# read_facets work (nebulous#40), including the `feed` facet dimension's
# label coverage (nebulous#49).
# Usage: just debug-verify-traversal-serve /path/to/cutting-garden
#
# check `nebulous traversal-serve` end-to-end against a real cutting-garden
[group('debug')]
debug-verify-traversal-serve cg_bin: build-go
  #!/usr/bin/env bash
  set -euo pipefail
  cfgdir="$(mktemp -d)"
  trap 'rm -rf "$cfgdir"' EXIT
  mkdir -p "$cfgdir/cutting-garden"
  cat > "$cfgdir/cutting-garden/config.toml" <<EOF
  [[plugins]]
  name = "nebulous"
  command = ["$(pwd)/build/debug/nebulous"]
  schemes = ["newsblur"]
  protocols = ["traversal"]
  EOF
  echo "=== cutting-garden list newsblur://feeds (via wire plugin) ==="
  XDG_CONFIG_HOME="$cfgdir" {{cg_bin}} list newsblur://feeds | head -5
  echo "=== tools/list + read_facets over MCP (via wire plugin) ==="
  {
    printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}'
    printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_facets","arguments":{"uri":"newsblur://feeds"}}}'
  } | XDG_CONFIG_HOME="$cfgdir" {{cg_bin}} mcp | tail -1 | jq .
  echo "=== feed label coverage on newsblur://stories (nebulous#49) ==="
  {
    printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}'
    printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_facets","arguments":{"uri":"newsblur://stories"}}}'
  } | XDG_CONFIG_HOME="$cfgdir" {{cg_bin}} mcp | tail -1 \
    | jq -r '.result.content[0].text | fromjson | (.facets.feed | length) as $total | (.labels.feed | length) as $labelled | "\($labelled)/\($total) feed ids labelled"'

# Verify the flake-pinned madder path is ldflags-injected into the debug build.
[group('debug')]
debug-inject-check:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "=== madder ==="
  strings build/debug/nebulous | grep -m1 '/nix/store/.*madder.*/bin/madder' || echo "MISSING"

# Bump a single flake input's pin in flake.lock. Example:
#   just debug-flake-update-input madder
#
# bump a single flake input's pin in flake.lock
[group('debug')]
debug-flake-update-input input:
  nix flake update --flake . {{input}}

# Regenerate pkgs/ facades from internal/ packages via dagnabit.
# No-op until a source file contains `//go:generate dagnabit export`.
# DAGNABIT_CEILING_DIRECTORIES bounds dagnabit's upward formatter-config
# search at the repo root — nebulous has no on-disk conformist/treefmt
# config, and an unbounded walk escalates to a stray ancestor
# conformist.toml (an eng-root checkout).
#
# regenerate pkgs/ facades from internal/ packages via dagnabit
[group('codemod')]
codemod-generate-facades:
  DAGNABIT_CEILING_DIRECTORIES="{{justfile_directory()}}" dagnabit export

# Populate the local persistent store from the NewsBlur API.
# Requires NEWSBLUR_TOKEN in the environment (set via .secrets.env / direnv).
#
# populate the local persistent store from the NewsBlur API
[group('explore')]
explore-fetch: build-go
  ./build/debug/nebulous fetch

# Build and install the MCP server to ~/.claude.json.
install-dev: build-nix
  ./result/bin/nebulous install-mcp

cache-dir := env("HOME") / ".cache/nebulous/store"

# Back up the local nebulous blob store before a risky migration.
[group('debug')]
debug-backup-cache:
  cp -r {{cache-dir}} {{cache-dir}}.bak
  @echo "Backed up to {{cache-dir}}.bak"

# Restore the nebulous blob store from the most recent backup.
[group('debug')]
debug-restore-cache:
  rm -rf {{cache-dir}}
  mv {{cache-dir}}.bak {{cache-dir}}
  @echo "Restored from backup"

# One-shot migration from the legacy ~/.cache/nebulous/responses layout
# to the new ~/.cache/nebulous/store layout. Not built into the prod binary.
#
# migrate the legacy response cache to the new store layout
[group('codemod')]
codemod-migrate-cache *args:
  go run ./cmd/migrate-cache {{args}}

# MUTATES the live NewsBlur account: (re-)stars story_hash and REPLACES its
# user_tags with exactly what's passed (matching SetStoryUserTags's own
# semantics -- an empty/absent tags arg CLEARS all existing tags on an
# already-starred story, it does not leave them alone). No default for tags:
# a silent-empty default here would be the exact live-account footgun this
# recipe exists to help debug in the first place. Bypasses nebulous entirely
# (cutting-garden#180 / nebulous#53 investigation): client.go's post() only
# checks the HTTP status code, never the response BODY, so a body-level
# failure (HTTP 200 with an error/code field inside) would currently be
# invisible to nebulous's own SetStoryUserTags/StarStory -- this prints
# exactly what NewsBlur returns, letting you check that layer directly.
# Requires NEWSBLUR_TOKEN in the environment.
#
# star a story and replace its user_tags, printing the raw response (MUTATES the live account)
[group('debug')]
debug-probe-star-response story_hash tags:
  curl -sS -X POST https://www.newsblur.com/reader/mark_story_hash_as_starred \
    -H "Cookie: newsblur_sessionid=$NEWSBLUR_TOKEN" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    --data-urlencode "story_hash={{story_hash}}" \
    --data-urlencode "user_tags={{tags}}" \
  | jq .

# Probe the RAW response body of /reader/starred_stories for one hash,
# bypassing nebulous entirely (cutting-garden#180 / nebulous#53
# investigation, H2): does the endpoint nebulous's fetch pipeline actually
# reads from even include user_tags per story at all? Requires
# NEWSBLUR_TOKEN in the environment.
#
# print the raw /reader/starred_stories response body for one hash
[group('debug')]
debug-probe-starred-story story_hash:
  curl -sS -G https://www.newsblur.com/reader/starred_stories \
    -H "Cookie: newsblur_sessionid=$NEWSBLUR_TOKEN" \
    --data-urlencode "h={{story_hash}}" \
  | jq .

# Sample the local corpus: list the first 5 keys, total count, and first entry body.
[group('explore')]
explore-corpus: build-go
  ./build/debug/nebulous corpus-list | head -5
  @echo "---"
  @echo "total keys: $(./build/debug/nebulous corpus-list | wc -l)"
  @echo "---"
  ./build/debug/nebulous corpus-read "$(./build/debug/nebulous corpus-list | head -1)"
