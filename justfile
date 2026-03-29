
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
