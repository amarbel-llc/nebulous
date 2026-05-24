# Nix side of go.mod: producer half of the flake-input-go_mod
# protocol (amarbel-llc/nixpkgs RFC 0001).
#
# Publishes `go-pkgs` (prod: *.go excluding *_test.go + module files)
# and `go-pkgs-test` (test superset: also *_test.go and testdata) via
# `pkgs.mkGoPkgs`. Downstream consumers can then bridge nebulous's Go
# module through `goFlakeInputs` instead of pinning gomod2nix hashes.
#
# The consumer half (bridging sibling Go-module flakes through
# `goFlakeInputs`) will land in the same file when adopted; per RFC
# 0001 § The `gomod.nix` convention, producer and consumer halves
# share one `gomod.nix`.
{
  pkgs,
  src,
}:
{
  goPkgs = pkgs.mkGoPkgs {
    inherit src;
  };
}
