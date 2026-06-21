
default: build test

build: build-go build-nix

test: test-go test-bats

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
  if [ -n "$(echo $ldflags)" ]; then ldflags_arg=(-ldflags "$ldflags"); fi
  go build "${gcflags_arg[@]}" "${ldflags_arg[@]}" -o build/{{tag}}/nebulous       ./cmd/nebulous
  go build "${gcflags_arg[@]}" "${ldflags_arg[@]}" -o build/{{tag}}/migrate-cache  ./cmd/migrate-cache
  go build "${gcflags_arg[@]}" "${ldflags_arg[@]}" -o build/{{tag}}/nebulous-cg    ./cmd/nebulous-cg

build-nix:
  nix build --show-trace

# Verify the flake-pinned madder path is ldflags-injected into the debug build.
# [group: debug]
debug-inject-check:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "=== madder ==="
  strings build/debug/nebulous | grep -m1 '/nix/store/.*madder.*/bin/madder' || echo "MISSING"

# Bump a single flake input's pin in flake.lock. Example:
#   just debug-flake-update-input madder
# [group: debug]
debug-flake-update-input input:
  nix flake update --flake . {{input}}

# Regenerate pkgs/ facades from internal/ packages via dagnabit.
# No-op until a source file contains `//go:generate dagnabit export`.
# [group: build]
generate-facades:
  dagnabit export

# Run the nebulous-cg cutting-garden plugin binary against the local
# cache, e.g. `just cg list newsblur://feeds` or `just cg list newsblur://stories`.
# Read-only traversal of the newsblur:// scheme; no token needed.
# [group: explore]
cg *args: build-go
  build/debug/nebulous-cg {{args}}

test-go *args:
  go test {{args}} ./...

# [group: test]
test-bats *args: build-go
  MIGRATE_CACHE_BIN="$(pwd)/build/debug/migrate-cache" \
  NEBULOUS_BIN="$(pwd)/build/debug/nebulous" \
    nix develop -c bats {{args}} zz-tests_bats/

install-dev: build-nix
  ./result/bin/nebulous install-mcp

cache-dir := env("HOME") / ".cache/nebulous/store"

backup-cache:
  cp -r {{cache-dir}} {{cache-dir}}.bak
  @echo "Backed up to {{cache-dir}}.bak"

restore-cache:
  rm -rf {{cache-dir}}
  mv {{cache-dir}}.bak {{cache-dir}}
  @echo "Restored from backup"

# One-shot migration from the legacy ~/.cache/nebulous/responses layout
# to the new ~/.cache/nebulous/store layout. Not built into the prod binary.
# [group: migration]
migrate-cache *args:
  go run ./cmd/migrate-cache {{args}}

fetch: build
  ./build/debug/nebulous fetch

# [group: test]
test-corpus: build
  ./build/debug/nebulous corpus-list | head -5
  @echo "---"
  @echo "total keys: $(./build/debug/nebulous corpus-list | wc -l)"
  @echo "---"
  ./build/debug/nebulous corpus-read $(./build/debug/nebulous corpus-list | head -1)

# [group: explore]
test-corpus-search query="microplastics":
  maneater index
  maneater search "{{query}}"
