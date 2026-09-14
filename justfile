
default: lint build test

lint: lint-fmt lint-worktree lint-go

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

# run go vet and godyn-lint (vet passes + staticcheck defaults) per package
[group('lint')]
lint-go:
  #!/usr/bin/env bash
  set -euo pipefail
  system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
  nix build ".#checks.${system}.vet" ".#checks.${system}.lint" --no-link --print-build-logs

# format the tree in place (repair mode) via `nix fmt`
[group('codemod')]
codemod-fmt-tree:
  nix fmt

# Build both nebulous binaries (nebulous, migrate-cache) through nix from
# go.nix (igloo FDR 0008; no ambient go) and link them under build/debug/,
# where the bats suite and the debug/explore recipes expect them.
#
# build all nebulous binaries into build/debug via nix
[group('build')]
build-go:
  #!/usr/bin/env bash
  set -euo pipefail
  mkdir -p build/debug
  for pkg in nebulous migrate-cache; do
    out=$(nix build --no-link --print-out-paths ".#${pkg}")
    ln -sfn "$out/bin/${pkg}" "build/debug/${pkg}"
  done

# reproducible nix build — the primary release artifact
[group('build')]
build-nix:
  nix build --show-trace

# run the Go unit tests: godyn's per-package test runs from go.nix
[group('test')]
test-go:
  #!/usr/bin/env bash
  set -euo pipefail
  system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
  nix build ".#checks.${system}.nebulous-tests" --no-link --print-build-logs

# Fast single-package Go test loop via godyn-test (igloo FDR 0008): builds one
# package's test run from a git+file: ref of the dirty tree (`git add -N` a NEW
# file first); only the edited cone rebuilds. Flags use the test binary's
# spelling:
#   just debug-go-test internal/bravo/tools -test.run=TestStoryQuery -test.v
#
# run go test for one package via godyn-test
[group('debug')]
debug-go-test dir *flags:
  nix run --inputs-from . igloo#godyn-test -- {{dir}} -- {{flags}}

# run the bats integration suite against the debug build
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

# Sweep every generated man page's NAME line through lexgrog and fail on any
# description over the fleet ceiling (72 chars, one line): spinclass renders
# NAME lines into a system-prompt index. go-mcp's GenerateManpages (>= v0.6.2)
# derives each NAME line itself and refuses to emit an over-long one
# (go-mcp-command(7) MANPAGE NAME LINES); this is the from-the-outside check
# on the nix build's share/man.
#
# check the built man pages' NAME lines stay <= 72 chars via lexgrog
[group('debug')]
debug-lexgrog-man: build-nix
  #!/usr/bin/env bash
  set -euo pipefail
  status=0
  for page in result/share/man/man*/*; do
    line=$(lexgrog "$page")
    desc=${line#*: \"}; desc=${desc%\"}; desc=${desc#* - }
    n=${#desc}
    printf '%3d  %s\n' "$n" "$line"
    if (( n > 72 )); then status=1; fi
  done
  exit "$status"

# verify the flake-pinned madder path is ldflags-injected into the debug build
[group('debug')]
debug-inject-check:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "=== madder ==="
  strings build/debug/nebulous | grep -m1 '/nix/store/.*madder.*/bin/madder' || echo "MISSING"

# Bump one or more flake inputs' pins in flake.lock in a single update, e.g.
# a cut-over go.nix producer together with the igloo it needs:
#   just debug-flake-update-input cutting-garden igloo
#
# bump one or more flake inputs' pins in flake.lock
[group('debug')]
debug-flake-update-input +inputs:
  nix flake update --flake . {{inputs}}

# go commands against the go.nix-rendered module (igloo FDR 0008): runs
# godyn-go from the flake-pinned igloo, so `go get` / `go mod tidy` edits land
# back in go.nix. There is no checkout go.mod and no ambient go. A NEW file
# must be `git add -N`'d first or the run cannot see it.
#   just codemod-go -- go get golang.org/x/text@latest
#
# run a go command through godyn's escape hatch and ingest the result into go.nix
[group('codemod')]
codemod-go *args:
  nix run --inputs-from . igloo#godyn-go -- {{args}}

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

# build and install the MCP server to ~/.claude.json
install-dev: build-nix
  ./result/bin/nebulous install-mcp

cache-dir := env("HOME") / ".cache/nebulous/store"

# back up the local nebulous blob store before a risky migration
[group('debug')]
debug-backup-cache:
  cp -r {{cache-dir}} {{cache-dir}}.bak
  @echo "Backed up to {{cache-dir}}.bak"

# restore the nebulous blob store from the most recent backup
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
  nix run .#migrate-cache -- {{args}}

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

# sample the local corpus: list the first 5 keys, total count, and first entry body
[group('explore')]
explore-corpus: build-go
  ./build/debug/nebulous corpus-list | head -5
  @echo "---"
  @echo "total keys: $(./build/debug/nebulous corpus-list | wc -l)"
  @echo "---"
  ./build/debug/nebulous corpus-read "$(./build/debug/nebulous corpus-list | head -1)"
