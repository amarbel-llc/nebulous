---
status: proposed
date: 2026-07-12
promotion-criteria: |
  Promote to experimental when: (1) the madder shell-out perf fix (Stage 1)
  is merged, (2) a nixosModule + home-manager module exist and build,
  (3) a first end-to-end capture (nebulous capture -> cutting-garden capture
  <store> web:<url> -> chrest -> receipt in a dedicated madder store ->
  discoverable via a new newsblur:// leaf) has run successfully against a
  real story, even if only exercised manually rather than via a deployed
  timer.
  Promote to testing when: the systemd timer + services.nebulous module are
  deployed on krone and have completed at least one unattended cycle.
---

# nebulous as a live krone-hosted service

## Problem Statement

Nebulous today is a workstation-only tool: `nebulous fetch` is a manual,
one-shot CLI invocation, and its local cache lives under
`$XDG_DATA_HOME/nebulous` on whatever machine runs it. NewsBlur's own
`story_content` is frequently truncated or missing for starred stories, and
nebulous has no full-fidelity preservation of the underlying article --- if
the source page goes offline (link rot, paywall changes, site shutdowns),
only NewsBlur's lossy extract remains.

Separately, circus's krone tent host already runs an always-on,
tailnet-only substrate purpose-built for exactly this kind of background
service: a durable madder blob store, a co-located cutting-garden +
madder-mcp MCP origin behind moxy (`infra/hosts/krone/mcp-origin.nix`), and
an established producer-module integration contract
(`circus-host-integration(7)`).

The goal: run nebulous as a live, always-on service on krone that (1)
periodically re-syncs against the NewsBlur API without manual intervention,
(2) drives chrest for full-page ("expanded/complete") captures of story
links, durably preserving them independent of NewsBlur, and (3) makes both
the structured NewsBlur data and the captured archives discoverable through
the cutting-garden MCP already running on krone.

## Research Findings (2026-07-12)

Investigated across four repos (`nebulous`, `cutting-garden`, `chrest`,
`circus`) before any implementation. The load-bearing finding:

**The chrest capture path is already fully wired -- inside cutting-garden,
not chrest itself.** `cutting-garden` ships a built-in `web` plugin
(`plugins/web/`) that claims the opt-in `web:` URL scheme and drives
`chrest` (resolved via `PATH`) as a subprocess: it tries the persistent
`chrest capture-serve` session first (RFC 0008), falling back to one-shot
`chrest capture-batch`. `cutting-garden capture <store-id> web:<url>`
(**positional** store id, e.g. `cutting-garden capture nebulous
web:https://example.com/article` -- corrected during Stage 3
implementation; the store id itself carries **no leading dot** either,
confirmed by a real invocation: `.nebulous` fails with `blob store not
found`, bare `nebulous` succeeds) already produces a full
[RFC 0002][cg-rfc-0002]/[RFC 0003][cg-rfc-0003] receipt (merkle tree:
identity/outcome/payload) written into whatever madder store is targeted
-- **no new integration code is needed on the chrest or cutting-garden
side.** chrest needs no NixOS module either; it's a self-contained package
(headless Firefox included via its own fixed-output derivation), so
`environment.systemPackages = [ chrest ]` on krone is sufficient.

krone already runs the durable substrate this needs: `/var/lib/madder` is
the group-owned `//default` madder store (`services.madder`, a unix-socket
daemon plus nix-cache's in-process library backend), on its own dedicated
hcloud volume (`krone-cache`, circus#17). `services.cutting-garden` is
already deployed there as a moxy stdio child alongside `madder-mcp`, both
fronted by one moxy MCP origin behind the Cloudflare tunnel
(`infra/hosts/krone/mcp-origin.nix`) -- the exact "cutting-garden MCP"
surface this feature wants nebulous's data exposed through.

circus's producer-module contract (`circus-host-integration(7)`) is
explicit: a service repo exports `nixosModules.default` (options + guarded
config block, self-passing so the package defaults to the flake's own
build); circus adds it as a flake input and flips `services.<name>.enable`.
MCP never gets its own HTTP origin -- it's a moxy stdio child. Secrets are
hand-placed root-only from piggy and referenced by env-var *name* only
(never the value) in rendered config.

### Gaps this surfaces

1. Nebulous has **no NixOS module at all today** -- CLI + MCP only.
2. `nebulous fetch` is **one-shot**, not a daemon.
3. Nebulous's own madder backend (`internal/0/madder`) **shells out to the
   `madder` CLI once per blob read**. Confirmed as the root cause of a
   multi-minute-plus cold-start hang against the local ~55k-entry cache
   ([nebulous#37][nebulous-37]) during this session's cutting-garden
   plugin verification. Fine for an interactive CLI; a real problem for a
   long-running krone service re-running fetch against a corpus that only
   grows.
4. Nebulous's own store needs a **durable** home on krone. The existing
   `//default` store is shared by nix-cache/forgejo-adjacent infra; the
   established pattern (durable volume + cache volume split, circus#17 /
   circus#21) argues for nebulous getting its own dedicated store rather
   than commingling.
5. **Capture format is single-choice per `cutting-garden capture` call**,
   selected via the `CUTTING_GARDEN_WEB_FORMAT` env var (default `pdf`) --
   no per-call format *list* yet (an open cutting-garden FDR follow-up per
   its own code comments in `plugins/web/types.go`).
6. **cutting-garden's web plugin has no browsable index of its captures.**
   `RestoreProtocol` is a pure point-lookup by receipt digest
   (`plugins/web/restore.go`); there is no `RootLister`/traversal
   implementation for the `web` scheme. "Available via the cutting-garden
   MCP" in a *discoverable* sense therefore requires nebulous's own
   `newsblur://story/{hash}/...` tree to carry the capture receipt's
   markl-id -- nebulous is the discovery index; cutting-garden/madder stay
   pure blob substrate.

## Decisions

Resolved 2026-07-12 (Sasha, via a 4-question decision round in the
nebulous session that did this research):

### 1. Scheduling: systemd timer, not a nebulous watch-loop

A systemd timer invoking the existing `nebulous fetch` CLI on a cadence.
No new scheduling code in nebulous; matches the NixOS idiom already used
elsewhere in circus (e.g. GC timers). A daemon/watch-loop mode inside
nebulous itself was the alternative, rejected as unneeded complexity for a
fetch cadence that doesn't need sub-timer-interval reactivity.

### 2. Store layout: dedicated madder store + volume for nebulous

Not krone's existing `//default` store. Mirrors the durable/cache volume
split already established for forgejo vs nix-cache (circus#17, circus#21)
-- keeps nebulous's growing corpus isolated, with its own backup/GC story
separate from the store nix-cache and forgejo-adjacent infra share.

### 3. Capture formats: a list, extensible, entirely nebulous-side

Sasha expects the format set to grow over time and wants "all of them" (a
static enumeration up front, not one hard-coded pick). Since
`cutting-garden capture` only accepts one format per invocation today
(env-var-driven, no per-call list -- see Gap 5 above), the design is:
nebulous's own config holds a **list** of format strings, and its capture
loop invokes `cutting-garden capture web:<url>` once per configured format
(setting `CUTTING_GARDEN_WEB_FORMAT` per subprocess call). Adding a format
later is purely a nebulous-config change -- no upstream cutting-garden
change required. (Multiple invocations against the same URL each launch
chrest's browser separately; whether that cost matters in practice is an
open question, see below.)

### 4. Fix the madder perf issue first

`internal/0/madder`'s per-blob subprocess shell-out (Gap 3 /
[nebulous#37][nebulous-37]) must be fixed -- switch to the in-process
`madder/go` library backend -- **before** standing nebulous up as a krone
daemon that re-runs fetch on every cycle against a perpetually-growing
corpus. This is Stage 1 of the roadmap below and is starting immediately.

## Staged Roadmap

- **Stage 1** (this repo): fix `internal/0/madder`'s per-blob subprocess
  shell-out -- switch to the in-process `madder/go` library backend, the
  same pattern circus's own `nix-cache` module already uses (its
  `backend = "madder"` in-process library option,
  `infra/hosts/krone/configuration.nix`).
- **Stage 2** (this repo, done): `nix/nixos-module.nix` +
  `nix/home-manager-module.nix`, mirroring cutting-garden's self-passing
  producer-module shape. `NEWSBLUR_TOKEN` is wired as a secret-*name*-only
  `environmentFile` option per `circus-host-integration(7)`. **Correction
  from the original draft**: the periodic `nebulous fetch` systemd
  timer/service lives in nebulous's OWN NixOS module, not in circus's
  Stage 4 — re-reading `circus-host-integration(7)`'s "MCP on a host"
  section, its "producer module defines no systemd unit of its own" rule
  is scoped specifically to the MCP-stdio-child role ("under Path 1... it
  defines no systemd unit"), not a blanket rule. `nix-cache` and `madder`'s
  own producer modules already own non-MCP daemon units the same way;
  `nebulous fetch` is exactly that kind of concern (a batch job, unrelated
  to MCP serving), so it follows their precedent instead. A
  `checks.modules-eval` flake check (mirroring cutting-garden's own)
  instantiates the module through a throwaway host and asserts the
  rendered timer/service, catching option-type regressions at `nix flake
  check` time.
- **Stage 3** (this repo, done): the multi-format capture loop -- a new
  `nebulous capture` subcommand (deliberately separate from `fetch`, its
  own systemd timer) runs a **gap-filling scan**: for each configured
  format, it shells out to `cutting-garden capture <store-id>
  web:<permalink>` (with `CUTTING_GARDEN_WEB_FORMAT=<format>`) for any
  starred story that doesn't yet have a recorded receipt for that format,
  and records each resulting receipt markl-id -- parsed directly off the
  capture command's own tap-ndjson stdout (the default for a piped/
  non-TTY child, exactly nebulous's case; cutting-garden's "receipt
  store=..." phase already carries `receipt_id` machine-readably, no
  second process or file read needed) -- as a new
  `newsblur://story/{hash}/capture/{format}` leaf alongside the existing
  `.../metadata` leaf. Self-healing by construction: a failed capture
  just has no receipt, so the next scan retries it automatically -- no
  separate retry/failure-state design needed (resolves the "retry
  policy" open question below). Backlog scope is **new stories only**
  for now, via a persisted watermark (`story.Date >= watermark`, set on
  first-ever run); a `--backfill` flag overrides the watermark for one
  run while still skipping already-captured (story, format) pairs, as
  the escape hatch for the eventual full-corpus pass. The
  `nebulous-capture` timer (longer interval than fetch's, since a
  capture is a real headless-browser render) is gated in
  `nix/nixos-module.nix` on two new option values, `chrestPackage` +
  `cuttingGardenPackage` (both nullable `types.package`) -- supplied by
  the consumer (circus), not this flake, since neither is a flake input
  of nebulous's own flake.
- **Stage 4** (circus repo -- a separate session/repo, not this worktree):
  flake input + `services.nebulous` on krone, the dedicated durable volume
  via tofu (mirroring `infra/hosts/provision/krone/main.tf`'s existing
  volume pattern) bind-mounted onto `services.nebulous.stateDir` (mirroring
  forgejo's `/var/lib/forgejo` <- `/mnt/durable/data` bind-mount pattern —
  no new bind-mount mechanism needed, Stage 2's module already creates a
  plain `stateDir` via `systemd.tmpfiles` for exactly this reason), and a
  third moxy child in `mcp-origin.nix` for `nebulous serve mcp` (Path 1,
  circus's job entirely — Stage 2's module does not touch MCP serving).

## Open Questions

- **Still open**: whether the whole `newsblur://` tree shape should be
  re-examined to better match cutting-garden's own conventions, now that
  a capture-index story exists on the nebulous side (cutting-garden#124's
  tri-modal container-body gap is part of that). Stage 3 shipped the
  straightforward extension -- one leaf per format,
  `newsblur://story/{hash}/capture/{format}` -- deliberately without
  resolving this broader question; Sasha flagged it explicitly as a
  later pass, not blocking Stage 3.
- **Resolved (Stage 3)**: "newly-indexed" is approximated via a
  persisted watermark compared against each story's publish date
  (`story.Date`), not an exact first-starred timestamp -- an accepted
  approximation for a personal-scale tool; `--backfill` is the escape
  hatch for anything this misses.
- **Resolved (Stage 3)**: retry/failure policy is "no explicit policy" --
  the gap-filling scan is naturally self-healing (a failed capture has no
  receipt, so the next scan attempts it again), so no persistent
  failure-state tracking was built.
- **Resolved (Stage 3)**: one leaf per format was chosen over one leaf
  enumerating all formats, consistent with the existing
  content/original/metadata leaf pattern.
- Multiple format invocations mean multiple chrest browser launches per
  story per scan -- is this an acceptable cost, or does it argue for
  petitioning cutting-garden for the multi-format-per-call capability
  (Gap 5) instead of working around it nebulous-side? Not yet exercised
  at scale (only a single-story, single-format smoke test has run so
  far) -- revisit once a real `--backfill` pass or a longer unattended
  timer run surfaces the actual cost.
- Whether nebulous's dedicated store on krone should have its own
  home-manager-rendered config for local (non-krone) access too, or stay
  krone-only.

## More Information

- Full research trail: nebulous session transcript
  `9625e660-42f1-49d6-8ead-28f133cb28de` (2026-07-12).
- [nebulous#37][nebulous-37] -- the madder per-blob shell-out perf issue
  (Stage 1).
- [cutting-garden RFC 0002 -- Capture Plugin Protocol][cg-rfc-0002]
- [cutting-garden RFC 0003 -- Web-Archive Binding][cg-rfc-0003]
- `circus-host-integration(7)` -- the producer-module / secrets / MCP /
  network-posture conventions this feature must follow on the circus side.
- `infra/hosts/krone/configuration.nix`,
  `infra/hosts/krone/mcp-origin.nix`,
  `infra/hosts/modules/madder-daemon.nix` (circus) -- the existing krone
  substrate this feature plugs into.
- `plugins/web/` (cutting-garden) -- the already-wired chrest-driving
  capture backend this feature reuses as-is.

[nebulous-37]: https://github.com/amarbel-llc/nebulous/issues/37
[cg-rfc-0002]: https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md
[cg-rfc-0003]: https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md
