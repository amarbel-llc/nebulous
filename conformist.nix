# nebulous's conformist overlay, merged with conformist.lib.presets.{eng,eng-go}
# in flake.nix (conformist.lib.evalModule). presets.eng enables the
# eng-convention linters (eng-versioning, flake-outputs/lock, the justfile-*
# roster); presets.eng-go carries the canonical goimports -> gofumpt chain.
# Here live the repo-specific formatters, the shellcheck linter, and excludes.
{ pkgs, ... }:
{
  programs.nixfmt.enable = true;

  # shfmt: a raw stanza rather than `programs.shfmt.enable`. The module cannot
  # emit `-ci` (no option for it) and its default includes lack `*.bats` — both
  # required by the eng shell style: 2-space indent, simplify, case-branch
  # indent; over *.sh / *.bash / *.bats. (Same rationale as madder's overlay.)
  settings.formatter.shfmt = {
    command = "${pkgs.shfmt}/bin/shfmt";
    options = [
      "-w"
      "-i"
      "2"
      "-s"
      "-ci"
    ];
    includes = [
      "*.sh"
      "*.bash"
      "*.bats"
    ];
  };

  # shellcheck linter (read-only in `conformist check`). The module's default
  # includes lack *.bats, which the zz-tests_bats suite uses.
  linters.shellcheck.enable = true;
  linters.shellcheck.includes = [
    "*.sh"
    "*.bash"
    "*.bats"
  ];

  # eng-versioning(7): go.mod's module path derives the key; pinned explicitly
  # to document the version.env contract.
  linters.eng-versioning.key = "NEBULOUS_VERSION";

  # Excludes layered on conformist's default-excludes (*.lock, go.mod, go.sum,
  # LICENSE). Only genuine build/scratch/prose artifacts: *.md has no enabled
  # formatter (the agents-md linter lives in the separate impure config with
  # its own excludes, so AGENTS.md is still seen there); build/ holds nix
  # out-links; .tmp/ is the session scratch space.
  settings.excludes = [
    "*.md"
    "build/**"
    ".tmp/**"
    "result"
    "result-*"
  ];
}
