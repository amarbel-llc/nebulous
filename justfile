build tag="debug":
  go build {{if tag == "release" { "-ldflags='-s -w'" } else { "'-gcflags=all=-N -l'" } }} -o build/{{tag}}/nebulous ./cmd/nebulous

nix-build:
  nix build --show-trace

dev-install: nix-build
  ./result/bin/nebulous install-mcp
