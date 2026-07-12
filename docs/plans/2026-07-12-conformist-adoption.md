# Conformist Adoption Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use eng:subagent-driven-development to implement this plan task-by-task.

**Goal:** Adopt conformist in nebulous (second brownfield `conform` adoption, following maneater@4373689).

**Architecture:** Wire the forge-hosted conformist (`git+https://code.linenisgreat.com/conformist.git`, master 2759686) as a flake input, add `conformist.nix` overlay, restructure the justfile per conformist-justfile(7), and update sweatfile pre-commit hook. The `conform` CLI scaffolds the overlay and version.env; hand-wire flake.nix following the maneater pattern (eachDefaultSystem shape with outer-let).

**Tech Stack:** Nix flakes, conformist, just, Go (nebulous)

**Rollback:** The branch is a worktree session; merge only after `just` gate is green. Revert by abandoning the worktree.

---

### Task 1: Run conform

- Run `nix run 'git+https://code.linenisgreat.com/conformist.git' -- conform` in worktree root
- Expect exit 3 (writes conformist.nix, version.env, prints repair command and justfile snippets)
- Observe whether flakeedit edits flake.nix in-place — record for dogfood report

### Task 2: Wire flake.nix

- Add conformist input (forge URL, igloo/nixpkgs-master/utils follows) after bob/purse-first/tap inputs
- Promote inner `let` to outer `let` — add nebulousVersion from version.env, nebulousCommit
- In inner let: conformistPkg, conformistEval (presets.eng + eng-go + ./conformist.nix), conformistImpureEval (presets.eng-impure)
- Outputs: formatter, checks.formatting, packages += conformist-{impure-config,pre-commit,repair}
- devShell.packages += conformistPkg + preCommit + repair

### Task 3: conformist.nix

Per maneater pattern: nixfmt, shfmt (-w -i 2 -s -ci; *.sh *.bash *.bats), shellcheck (*.sh *.bash *.bats), eng-versioning key = "NEBULOUS_VERSION", excludes (*.md, build/**, .tmp/**, result, result-*)

### Task 4: version.env

Create `export NEBULOUS_VERSION=0.1.0` per eng-versioning(7). Wire nebulousVersion into buildGoApplication (replace hardcoded `version = "0.1.0"` at flake.nix:83).

### Task 5: Restructure justfile

- default: lint build verify test
- lint: lint-fmt lint-worktree  
- lint-fmt: nix build checks.${system}.formatting
- lint-worktree: conformist check --config-file (impure-config) --tree-root .
- build: build-go build-nix (+ build-gomod2nix if present)
- codemod: codemod-fmt codemod-generate-facades
- codemod-fmt: nix fmt
- Rename generate-facades -> codemod-generate-facades; keep DAGNABIT_CEILING_DIRECTORIES
- Fix bare-verb leaves and leaves outside aggregates
- Fix shellcheck findings with scoped disables + reasons

### Task 6: sweatfile

Add `pre-commit = "conformist-pre-commit"` under [hooks].

### Task 7: Gate

`git add -N conformist.nix` before any nix eval. Run `just` until green.

### Task 8: Commit + merge

Commit referencing maneater adoption. merge-this-session. Message parent session.
