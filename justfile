
default: build test

build: build-go build-nix

test: test-go

build-go tag="debug":
  go build {{if tag == "release" { "-ldflags='-s -w'" } else { "'-gcflags=all=-N -l'" } }} -o build/{{tag}}/nebulous ./cmd/nebulous

build-nix:
  nix build --show-trace

test-go *args:
  go test {{args}} ./...

install-dev: build-nix
  ./result/bin/nebulous install-mcp

cache-dir := env("HOME") / ".cache/nebulous/responses"

backup-cache:
  cp -r {{cache-dir}} {{cache-dir}}.bak
  @echo "Backed up to {{cache-dir}}.bak"

restore-cache:
  rm -rf {{cache-dir}}
  mv {{cache-dir}}.bak {{cache-dir}}
  @echo "Restored from backup"

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
