build tag="debug":
  go build {{if tag == "release" { "-ldflags='-s -w'" } else { "'-gcflags=all=-N -l'" } }} -o build/{{tag}}/nebulous ./cmd/nebulous

test *args:
  go test {{args}} ./...

nix-build:
  nix build --show-trace

dev-install: nix-build
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
