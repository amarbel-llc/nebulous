# Examination: Integrating the Hypothes.is Annotation Protocol with the Nebulous–Chrest Capture Pipeline

> **Status**: feasibility examination. No implementation steps; no Go edits in
> this document. The output is an analysis of where Hypothes.is annotations
> would slot into the existing `nebulous`/`chrest`/`madder` capture pipeline,
> what the integration would buy us, and what's in the way.

## Context

Nebulous already runs an end-to-end web-capture pipeline (RFC 0001 § Web
Capture Archive Protocol, `docs/rfcs/0001-web-capture-archive-protocol.md`):

- **Policy** (TOML, `internal/alfa/policy/policy.go`) declares per-URL
  capture intent: which formats, which browser, which extensions.
- **Orchestrator** (`internal/alfa/orchestrator/orchestrator.go`) fans out
  `(subject × policy)` jobs, expands URL templates, and assembles the
  archive record.
- **Capturer** (`internal/alfa/capturer/capturer.go` → external `chrest`)
  spawns a browser, renders the URL into format-specific artifacts, and
  streams `BatchOutput` JSON back.
- **Writer** (`internal/0/writer/writer.go` → external `madder`)
  content-addresses each artifact (spec/payload/envelope) into a blob
  store keyed by markl ID.
- **Archive record** (`internal/0/archive/archive.go`) ties markl IDs
  together at `~/.local/share/nebulous/archives/<subject>/<policy_id>.json`,
  with linked-list history via `Record.Previous`.

Captures fire automatically post-`fetch` for newly-indexed NewsBlur stories
(commit `6a9ce73`), so every starred story can already be a snapshot
target.

Hypothes.is is an open web-annotation service implementing (a profile of)
the W3C Web Annotation Data Model. Its public API
(`https://api.hypothes.is/api/`) is **URL-keyed**: given a target URI,
`GET /api/search?uri=<url>` returns the public annotations on that page
as JSON. Private/group annotations need a bearer token. Every URL nebulous
already captures is, in principle, a candidate for an annotation lookup.

The motivating value proposition: a captured page is a frozen snapshot of
content, but the *commentary* on that content (annotations, highlights,
public threads) lives elsewhere and rots independently. Co-archiving
annotations alongside the page snapshot preserves both the primary source
and the contemporaneous social/scholarly response.

## What "integrating Hypothes.is" could mean — four architectural options

Listed from least invasive to most invasive. Each preserves the RFC 0001
content-addressed model so annotations dedupe like any other artifact.

### Option A: Orchestrator-side sidecar fetch

After `runOneJob` finishes its chrest capture, the orchestrator makes an
HTTP call to `api.hypothes.is/api/search?uri=<expanded URL>`, JCS-canonicalizes
the response, and writes it through the same `madder` writer. The resulting
markl ID lands in a new `record.annotations` field (or as an extra entry in
`record.captures` with a nebulous-defined format name).

- **Code surface**: `orchestrator.Deps` gets a `FetchAnnotations(url) ([]byte, error)`
  callback; `archive.Record` gets an optional sidecar field; policy gets
  an `[annotations]` block toggling the fetch on/off and carrying optional
  bearer token. No chrest or RFC changes.
- **Pros**: smallest blast radius. Annotation fetch can be retried
  independently of capture (different failure modes — TCP to one HTTP API
  vs spawning a browser). No browser cost for an HTTP-only fetch.
- **Cons**: bifurcates the capture pipeline — chrest produces some
  artifacts, nebulous produces others. Two places to reason about
  artifact production. Bypasses the capabilities-artifact mechanism (RFC
  § 4.5) so consumers can't tell whether absent annotations mean "no
  data" vs "fetch disabled".

### Option B: New chrest format `hypothesis-annotations`

Nebulous adds `"hypothesis-annotations"` to `policy.allowedFormats`
(`policy.go:100`), the RFC adds the format under § Payload Artifact with
media type `application/vnd.hypothesis.annotations+json` (or echoes the
W3C `application/ld+json; profile=…`), and chrest implements the format
handler — for this format the "browser" is the Hypothes.is HTTP API, not
a real browser. The orchestrator/capturer interface is unchanged.

- **Code surface**: nebulous-side is two lines (allowedFormats + template).
  RFC and chrest carry the weight: format definition, normalization rules
  (canonicalize JSON-LD? sort by id? strip server-side timestamps?),
  options schema (`api_base`, `group`, `since`, `limit`).
- **Pros**: the cleanest fit with the existing protocol — annotations get
  the same spec/payload/envelope tri-fold and the same identity story
  (spec markl ID is capture identity, dedupes across runs). The
  capabilities artifact tells consumers up-front that this capturer
  supports the format. Per-capture errors flow through the same
  `BatchOutput.Captures[].Error` channel.
- **Cons**: chrest is the wrong implementer in spirit — it's a *browser*
  capturer; an HTTP API client doesn't really need WebDriver/BiDi. Forces
  cross-repo work and an RFC revision before any line of nebulous code
  runs. Mixing browser and non-browser capture under one binary is a
  category error that will compound.

### Option C: New non-chrest capturer that conforms to RFC 0001

Define a sibling capturer (call it `chronicle` or similar) that speaks the
RFC § 3 capturer protocol (BatchInput JSON on stdin, BatchOutput JSON on
stdout, writer cmd injected) but only knows how to fetch annotations.
Orchestrator gains the ability to dispatch certain formats to certain
capturers (currently it always shells out to chrest via `capturer.Bin`).

- **Code surface**: new `internal/alfa/capturer/` adapter or per-format
  registry. New external binary (Nix-pinned alongside chrest). No
  protocol invention — RFC 0001 already permits multiple capturers.
- **Pros**: keeps each capturer focused. The "is this a browser thing or
  an API thing?" boundary becomes binary-level, not function-level.
- **Cons**: more moving parts. Today the orchestrator hardcodes one
  capturer; multi-capturer dispatch is non-trivial and likely not worth
  it for a single new format. ADR-0001 already chose to keep
  normalization in the orchestrator over per-capturer plugins; multi-capturer
  dispatch reopens that question.

### Option D: Hypothes.is browser extension during capture

Load the Hypothes.is Firefox extension via `policy.capture[].extensions`,
let it inject the annotation overlay into the rendered page, capture as
usual.

- **Pros**: zero new artifact types — annotations show up in the
  `screenshot`, `pdf`, `html-monolith` output of an already-supported
  format.
- **Cons**: annotations are baked into the page bytes, not extractable
  as structured data. No search, no per-annotation provenance, no
  filtering by group/user. Defeats the point of treating annotations as
  first-class data.

## Cross-cutting concerns regardless of option chosen

1. **Identity & determinism (RFC § 5.x).** Hypothes.is responses are
   not stable: server-side `updated` timestamps, paging order, and
   transient annotations all churn. Any payload definition must specify a
   normalization rule (canonical JCS over a sorted-by-id annotations
   array, with a defined set of stripped fields recorded in the
   envelope) or it cannot dedupe via markl ID. Without that rule,
   re-archiving a stable page produces a fresh blob every run, breaking
   the linked-list history's deduplication benefit.

2. **Auth and secrets.** Public annotations are anonymous; private
   groups need a bearer token. The token is sensitive and policy files
   are committed/templated (see `docs/templates/nebulous.toml`), so the
   token plumbing must mirror `NEWSBLUR_TOKEN`'s `.secrets.env` +
   direnv pattern, never inline in policy. RFC 0001 § Security
   Considerations → Writer Command Trust applies analogously.

3. **Rate limiting.** Hypothes.is publishes per-IP rate limits.
   `nebulous fetch` already runs adaptive backoff against NewsBlur
   (`internal/alfa/newsblur/`); a fan-out of N stories × annotation
   lookups will hit limits. Re-using `adaptiveBackoff` (or a cousin) is
   the natural pattern.

4. **Capture subject vs. annotation target.** Hypothes.is normalizes
   target URIs (canonicalization, query-string stripping). The expanded
   URL nebulous passes to chrest may not match the URL Hypothes.is
   indexes. The integration will need a target-canonicalization step,
   and ideally probe `via.hypothes.is` or use the `equivalent_uris`
   API to broaden the lookup.

5. **Empty results are normal.** Most pages have zero public
   annotations. The pipeline must treat an empty annotation set as a
   *successful* capture, not an error — otherwise the orchestrator's
   circuit breaker (`runJobs`, 3 consecutive failures bail) trips on
   the common case.

6. **PII and republication.** Public annotations carry author handles
   and may include user-quoted page fragments. Archiving them is a
   re-publication act with its own ethical envelope; RFC § Captured
   Content Is Untrusted covers technical concerns but the deployment
   should also document a policy stance.

## Existing nebulous extension points the integration would land on

| Concern | File | Hook |
|---|---|---|
| Format whitelist | `internal/alfa/policy/policy.go:100` | Add format to `allowedFormats` |
| Per-capture options schema | `internal/alfa/policy/policy.go:32` | `Capture.Options` already free-form `map[string]any` |
| Story-keyed metadata available to URL templates | `internal/alfa/policy/policy.go:57` | Add `Story.HypothesisURL` field if URL template needs to differ |
| Orchestrator subject resolution | `internal/alfa/orchestrator/orchestrator.go` `ResolveStory` callback | Could enrich Story with annotation metadata |
| Archive record additions | `internal/0/archive/archive.go:64` `Record` struct | Add optional `Annotations []ArtifactRef` if going Option A |
| Post-capture hook (currently none) | `internal/alfa/orchestrator/orchestrator.go:runOneJob` | Would be the insertion point for Option A |
| RFC artifact definitions | `docs/rfcs/0001-web-capture-archive-protocol.md` § Payload Artifact | Where Option B's format would be specified |

## Recommendation

If the goal is to ship something soon, **Option A (orchestrator-side
sidecar)** is the lowest-risk path — it keeps chrest a pure browser
capturer, owns the HTTP fetch in nebulous where the rest of the
NewsBlur HTTP code already lives, and its bifurcation cost is bounded
because there's exactly one non-browser data source on the roadmap.

If the goal is to set up nebulous to absorb a *family* of URL-keyed
metadata sources (Hypothes.is now, Wayback CDX, archive.today,
Pinboard notes, mastodon mentions, …), **Option C (sibling capturer)**
is the right long-term shape: it preserves RFC 0001 cleanly and pushes
each source into its own binary with its own auth/rate-limit story.
The upfront cost is a multi-capturer dispatch in the orchestrator and
a Nix-pinned second capturer, but each subsequent source becomes
incremental.

**Option B** is the most architecturally tempting (one protocol! one
binary!) and the most likely to age badly: bundling an HTTP API client
into a browser capturer makes the chrest capability surface
incoherent, and dragging the RFC + cross-repo work for one format is
out of proportion to the value.

**Option D** is a useful complement to A or C (capturing the
annotation overlay in screenshots is good provenance), but is not by
itself an integration of the protocol.

## Open questions to settle before any design phase

1. Does this need to support private Hypothes.is groups, or only the
   public stream? (Drives auth + secrets work.)
2. Are we annotating only NewsBlur-sourced URLs, or any URL nebulous
   captures (`archive-capture URL` form too)? (Drives where the
   target-canonicalization step lives.)
3. Is the goal to surface annotations in MCP tools (e.g. a
   `story_query` filter "has-annotations") or just to archive them?
   (Drives whether annotations need to feed the in-memory story
   index.)
4. Are we comfortable depending on the live Hypothes.is API
   indefinitely, or do we need a fallback (e.g. self-host) story?

## Verification approach (if/when an option is implemented)

Pure analysis for now; verification suggestions for whichever option is
chosen later:

- **Option A**: stub `FetchAnnotations` in tests (mirroring the
  `chrest-stub.sh` pattern in `internal/alfa/capturer/capturer_test.go`),
  then a bats integration test against a recorded API fixture under
  `zz-tests_bats/`.
- **Option B**: nebulous-side change is a one-line whitelist; the
  real test surface lives in chrest. Nebulous-side test would be a
  policy validation case that accepts the new format string.
- **Option C**: requires the sibling binary first; verification mirrors
  `archive_capture.bats` against the new capturer with a small
  recorded fixture.
- **Cross-cutting**: any payload definition must come with a
  determinism test — re-fetch the same URL twice through the
  pipeline, confirm identical markl IDs after normalization.

## Critical files to read alongside this examination

- `docs/rfcs/0001-web-capture-archive-protocol.md` — § Capturer
  Protocol, § Artifact Formats, § Capabilities Artifact.
- `docs/decisions/0001-archive-normalization-in-orchestrator.md` —
  prior reasoning that pushed normalization out of the capturer; any
  Option-C revival reopens this.
- `internal/alfa/orchestrator/orchestrator.go` — `runOneJob`,
  `buildBatchInput`, `assembleRecord`.
- `internal/alfa/capturer/capturer.go` + `types.go` — the protocol
  boundary an Option-C sibling capturer would have to honor.
- `internal/alfa/policy/policy.go` — the validator a new format must
  pass through.
- `internal/0/archive/archive.go` — `Record` struct shape; this is
  what gains a sidecar field under Option A.
