---
status: proposed
date: 2026-07-12
promotion-criteria: |
  Promote to experimental when: (1) the madder shell-out perf fix (Stage 1)
  is merged, (2) a nixosModule + home-manager module exist and build,
  (3) a first end-to-end capture (nebulous fetch -> cutting-garden capture
  web:<url> -> chrest -> receipt in a dedicated madder store -> discoverable
  via a new newsblur:// leaf) has run successfully against a real story,
  even if only exercised manually rather than via a deployed timer.
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
`chrest capture-batch`. `cutting-garden capture web:<url> --store <name>`
already produces a full [RFC 0002][cg-rfc-0002]/[RFC 0003][cg-rfc-0003]
receipt (merkle tree: identity/outcome/payload) written into whatever
madder store is targeted -- **no new integration code is needed on the
chrest or cutting-garden side.** chrest needs no NixOS module either; it's
a self-contained package (headless Firefox included via its own
fixed-output derivation), so `environment.systemPackages = [ chrest ]` on
krone is sufficient.

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
- **Stage 2** (this repo): add `nix/nixos-module.nix` +
  `nix/home-manager-module.nix` to nebulous, mirroring cutting-garden's
  self-passing producer-module shape (`nix/nixos-module.nix` in that repo);
  wire `NEWSBLUR_TOKEN` as a secret-*name*-only option per
  `circus-host-integration(7)`.
- **Stage 3** (this repo): the multi-format capture loop -- for each
  newly-indexed story, shell out to `cutting-garden capture web:<permalink>
  --store <nebulous-store>` once per configured format, and record each
  resulting receipt markl-id as a new leaf under
  `newsblur://story/{hash}/...` (alongside the existing `.../metadata`) so
  captures are discoverable through nebulous's own structured tree.
- **Stage 4** (circus repo -- a separate session/repo, not this worktree):
  flake input + `services.nebulous` on krone, the dedicated durable volume
  via tofu (mirroring `infra/hosts/provision/krone/main.tf`'s existing
  volume pattern), a systemd timer unit, and a third moxy child in
  `mcp-origin.nix`.

## Open Questions

- What counts as "newly-indexed" for the capture loop to trigger on --
  every fetch cycle's diff against the previous run, or an explicit
  "captured" marker per story to avoid re-capturing on every timer tick?
- Retry/failure policy for a chrest capture that fails (rate limits,
  paywalls, JS-heavy pages chrest can't render) -- silently skip and retry
  next cycle, or surface a persistent failure state?
- Multiple format invocations mean multiple chrest browser launches per
  story per cycle -- is this an acceptable cost, or does it argue for
  petitioning cutting-garden for the multi-format-per-call capability
  (Gap 5) instead of working around it nebulous-side?
- Exact shape of the new `newsblur://story/{hash}/...` capture leaf(s) --
  one leaf per format, or one leaf enumerating all captured formats with
  their receipt ids?
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
