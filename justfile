
default: lint build test

lint: lint-fmt lint-worktree

build: build-go build-nix

test: test-go test-bats

codemod: codemod-fmt codemod-generate-facades codemod-migrate-cache

codemod-fmt: codemod-fmt-tree

# Read-only formatting + the eng preset's file-based linters, via the sandboxed
# checks.formatting derivation.
[group('lint')]
lint-fmt:
  #!/usr/bin/env bash
  set -euo pipefail
  system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
  nix build ".#checks.${system}.formatting" --no-link --print-build-logs

# Impure eng checks (git remotes, sweatfile, agents-md) against the working
# tree; conformist comes from the devShell PATH.
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

# Debug build of all nebulous binaries (nebulous, migrate-cache, nebulous-cg).
# Pass tag=release for a stripped production build.
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
    extra_ldflags="$extra_ldflags -X github.com/friedenberg/nebulous/internal/0/madder.Bin=$madder_path/bin/madder"
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
  go build "${gcflags_arg[@]}" "${ldflags_arg[@]}" -o build/{{tag}}/nebulous-cg    ./cmd/nebulous-cg

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

# Verify the flake-pinned madder path is ldflags-injected into the debug build.
[group('debug')]
debug-inject-check:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "=== madder ==="
  strings build/debug/nebulous | grep -m1 '/nix/store/.*madder.*/bin/madder' || echo "MISSING"

# Bump a single flake input's pin in flake.lock. Example:
#   just debug-flake-update-input madder
[group('debug')]
debug-flake-update-input input:
  nix flake update --flake . {{input}}

# Regenerate pkgs/ facades from internal/ packages via dagnabit.
# No-op until a source file contains `//go:generate dagnabit export`.
# DAGNABIT_CEILING_DIRECTORIES bounds dagnabit's upward formatter-config
# search at the repo root — nebulous has no on-disk conformist/treefmt
# config, and an unbounded walk escalates to a stray ancestor
# conformist.toml (an eng-root checkout).
[group('codemod')]
codemod-generate-facades:
  DAGNABIT_CEILING_DIRECTORIES="{{justfile_directory()}}" dagnabit export

# Run the nebulous-cg cutting-garden plugin binary against the local
# cache, e.g. `just explore-cg list newsblur://feeds`.
# Read-only traversal of the newsblur:// scheme; no token needed.
[group('explore')]
explore-cg *args: build-go
  build/debug/nebulous-cg {{args}}

# Populate the local persistent store from the NewsBlur API.
# Requires NEWSBLUR_TOKEN in the environment (set via .secrets.env / direnv).
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
[group('codemod')]
codemod-migrate-cache *args:
  go run ./cmd/migrate-cache {{args}}

# Sample the local corpus: list the first 5 keys, total count, and first entry body.
[group('explore')]
explore-corpus: build-go
  ./build/debug/nebulous corpus-list | head -5
  @echo "---"
  @echo "total keys: $(./build/debug/nebulous corpus-list | wc -l)"
  @echo "---"
  ./build/debug/nebulous corpus-read "$(./build/debug/nebulous corpus-list | head -1)"
