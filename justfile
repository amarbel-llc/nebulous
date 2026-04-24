
default: build test

build: build-go build-nix

test: test-go test-bats

build-go tag="debug":
  #!/usr/bin/env bash
  set -euo pipefail
  # Resolve the flake-pinned madder and chrest so their absolute
  # paths are ldflags-injected into internal/0/madder.Bin and
  # internal/alfa/capturer.Bin — mirrors what the Nix build does
  # in flake.nix. Without this, debug builds of `nebulous archive-capture`
  # would invoke madder/chrest via PATH, where older user-profile
  # binaries can shadow the devShell's.
  madder_path=$(nix build --no-link --print-out-paths .#madder 2>/dev/null || true)
  chrest_path=$(nix build --no-link --print-out-paths .#chrest 2>/dev/null || true)
  extra_ldflags=""
  if [ -n "$madder_path" ]; then
    extra_ldflags="$extra_ldflags -X github.com/friedenberg/nebulous/internal/0/madder.Bin=$madder_path/bin/madder"
  fi
  if [ -n "$chrest_path" ]; then
    extra_ldflags="$extra_ldflags -X github.com/friedenberg/nebulous/internal/alfa/capturer.Bin=$chrest_path/bin/chrest"
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

build-nix:
  nix build --show-trace

# Verify flake-pinned binary paths are ldflags-injected into the debug build.
# [group: debug]
debug-inject-check:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "=== madder ==="
  strings build/debug/nebulous | grep -m1 '/nix/store/.*madder.*/bin/madder' || echo "MISSING"
  echo "=== chrest ==="
  strings build/debug/nebulous | grep -m1 '/nix/store/.*chrest.*/bin/chrest' || echo "MISSING"

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

# Drop a starter nebulous.toml at the XDG-default policy path if one
# does not already exist. The starter captures text + PDF per story,
# split=false (no envelope), via the firefox backend.
# [group: archive]
archive-init:
  #!/usr/bin/env bash
  set -euo pipefail
  dir="${XDG_CONFIG_HOME:-$HOME/.config}/nebulous"
  path="$dir/nebulous.toml"
  if [ -f "$path" ]; then
    echo "archive-init: policy already exists at $path (not overwriting)"
    exit 0
  fi
  mkdir -p "$dir"
  cp ./docs/templates/nebulous.toml "$path"
  echo "archive-init: wrote $path"

# Archive one or more stories / URLs via the prod XDG data + config paths.
# Forwards all args to `nebulous archive-capture`, so typical usage is:
#   just archive 6327282:5d1cf5
#   just archive https://example.com/
#   echo 6327282:5d1cf5 | just archive -
# [group: archive]
archive *args: build-go
  ./build/debug/nebulous archive-capture {{args}}

# Archive the N most recent starred stories. Defaults to 5 targets, 1 job.
# Streams story IDs into `archive-capture -`, which processes them
# in one invocation — the orchestrator's circuit breaker handles
# transient failures (bails after 3 consecutive; one flaky
# chrest/madder won't poison the whole run).
#
# Emits a 30s mtime-based progress tick while running (works whether
# targets are new or being re-archived), then a per-capture-kind
# summary after. Use with `jobs=N` for parallel capture.
#
# Examples:
#   just archive-recent
#   just archive-recent 100
#   just archive-recent 100 jobs=4
# [group: archive]
archive-recent n="5" jobs="1": build-go
  #!/usr/bin/env bash
  set -euo pipefail
  policy="${XDG_CONFIG_HOME:-$HOME/.config}/nebulous/nebulous.toml"
  if [ ! -f "$policy" ]; then
    echo "archive-recent: no policy at $policy — run \`just archive-init\` first" >&2
    exit 1
  fi
  command -v jq >/dev/null 2>&1 || { echo "archive-recent: jq is required (available in the nix devshell)" >&2; exit 1; }

  archives="${XDG_DATA_HOME:-$HOME/.local/share}/nebulous/archives"
  mkdir -p .tmp
  marker=.tmp/archive-recent.marker
  log=.tmp/archive-recent.log
  rm -f "$marker" "$log"
  touch "$marker"
  # mtime granularity is a second on most FSes; pad so records written
  # inside the same second register as -newer than the marker.
  sleep 1

  echo "archive-recent: capturing up to {{n}} targets with jobs={{jobs}}"
  t0=$(date +%s)

  # Progress watcher in background. Counts records whose default.json
  # mtime is newer than the marker, which catches both new and
  # overwritten records (a bare file-count baseline breaks on re-archive).
  (
    while :; do
      sleep 30
      seen=$(find "$archives" -name default.json -newer "$marker" 2>/dev/null | wc -l)
      elapsed=$(( ( $(date +%s) - t0 ) / 60 ))
      printf '  [+%dm] records touched: %d/%s\n' "$elapsed" "$seen" "{{n}}" >&2
    done
  ) &
  watch_pid=$!
  trap 'kill "$watch_pid" 2>/dev/null || true' EXIT

  # --format=tap streams TAP-14 lines to the log as jobs complete, so
  # `tail -f "$log"` shows live per-job progress during the run.
  ./build/debug/nebulous corpus-list -limit {{n}} \
    | ./build/debug/nebulous archive-capture --jobs={{jobs}} --format=tap - \
    > "$log" 2>&1
  rc=$?

  kill "$watch_pid" 2>/dev/null || true
  elapsed=$(( $(date +%s) - t0 ))

  printf '\narchive-recent summary (%ds, orchestrator exit=%d):\n' "$elapsed" "$rc"
  find "$archives" -name default.json -newer "$marker" -print0 2>/dev/null \
    | xargs -0 -r jq -s '{
        records:      length,
        fully_ok:     [.[] | select(all(.captures[]?; .error == null))] | length,
        fully_failed: [.[] | select((.captures | length) > 0 and all(.captures[]; .error != null))] | length,
        partial:      [.[] | select(any(.captures[]?; .error == null) and any(.captures[]?; .error != null))] | length,
        captures_by_kind:
          [.[].captures[]? | .error.kind // "ok"]
          | group_by(.) | map({key: .[0], value: length}) | from_entries
      }'

  if [ "$rc" -ne 0 ]; then
    echo '--- orchestrator log (tail) ---'
    tail -n 40 "$log"
  fi
  exit "$rc"

# Archive comparison test
# Compares monolith (static fetch) vs single-file-cli (headless browser)
# on representative page types: static blog, complex layout, JS-heavy news

archive-test-dir := "/tmp/nebulous-archive-test"

archive-test-urls := "https://tonsky.me/blog/crdt-filesync/ https://gwern.net/gwtar https://arstechnica.com/tech-policy/2026/02/wikipedia-bans-archive-today-after-site-executed-ddos-and-altered-web-captures/"

archive-test: archive-test-monolith archive-test-singlefile archive-test-compare

archive-test-monolith:
  mkdir -p {{archive-test-dir}}
  for url in {{archive-test-urls}}; do \
    name=$(echo "$url" | sed 's|https://||;s|/|_|g;s|_$||'); \
    echo "=== monolith: $name ==="; \
    time nix run nixpkgs#monolith -- -e -I "$url" -o "{{archive-test-dir}}/monolith_${name}.html"; \
    ls -lh "{{archive-test-dir}}/monolith_${name}.html"; \
    echo; \
  done

archive-test-singlefile:
  mkdir -p {{archive-test-dir}}
  for url in {{archive-test-urls}}; do \
    name=$(echo "$url" | sed 's|https://||;s|/|_|g;s|_$||'); \
    echo "=== single-file: $name ==="; \
    time nix shell nixpkgs#single-file-cli nixpkgs#chromium -c \
      single-file --browser-executable-path=$(nix eval --raw nixpkgs#chromium)/bin/chromium \
      "$url" "{{archive-test-dir}}/singlefile_${name}.html"; \
    ls -lh "{{archive-test-dir}}/singlefile_${name}.html"; \
    echo; \
  done

archive-test-compare:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "=== Size Comparison ==="
  printf "%-60s %10s %10s\n" "URL" "monolith" "singlefile"
  printf "%-60s %10s %10s\n" "---" "--------" "----------"
  for url in {{archive-test-urls}}; do
    name=$(echo "$url" | sed 's|https://||;s|/|_|g;s|_$||')
    m_size=$(stat -c%s "{{archive-test-dir}}/monolith_${name}.html" 2>/dev/null || echo 0)
    s_size=$(stat -c%s "{{archive-test-dir}}/singlefile_${name}.html" 2>/dev/null || echo 0)
    m_human=$(numfmt --to=iec-i --suffix=B "$m_size" 2>/dev/null || echo "${m_size}B")
    s_human=$(numfmt --to=iec-i --suffix=B "$s_size" 2>/dev/null || echo "${s_size}B")
    short_url=$(echo "$url" | cut -c1-60)
    printf "%-60s %10s %10s\n" "$short_url" "$m_human" "$s_human"
  done
  echo
  echo "Output files in {{archive-test-dir}}/"
  echo "Open in browser to visually compare fidelity."

archive-test-clean:
  rm -rf {{archive-test-dir}}
