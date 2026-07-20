# Nix side of go.mod: producer + consumer halves of the
# flake-input-go_mod protocol (amarbel-llc/nixpkgs RFC 0001).
#
# Producer: publishes go-pkgs (prod: *.go excluding *_test.go + module
# files) and go-pkgs-test (test superset) via `pkgs.mkGoPkgs` so
# downstream consumers can wire nebulous's Go module through
# `goFlakeInputs` instead of pinning gomod2nix hashes.
#
# Consumer: bridges sibling Go-module flakes (tap, purse-first) so
# cross-amarbel Go bumps collapse from a three-place edit
# (go.mod + gomod2nix.toml + flake.lock) into one.
{
  pkgs,
  src,
  tap,
  purse-first,
  system,
}:
{
  goPkgs = pkgs.mkGoPkgs {
    inherit src;
  };

  goFlakeInputs = {
    "code.linenisgreat.com/tap/go" = {
      src = tap.packages.${system}.go-pkgs;
      subPath = "go";
    };
    "code.linenisgreat.com/purse-first/libs/go-mcp" = {
      src = purse-first.packages.${system}.go-pkgs;
      subPath = "libs/go-mcp";
    };
  };
}
