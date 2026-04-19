---
status: proposed
date: 2026-04-19
decision-makers: nebulous/firm-yew, chrest/eager-poplar
consulted: Sasha F
informed: Engineering (RFC 0001 co-design participants)
---

# Place archive normalization in the orchestrator rather than the capturer

## Context and Problem Statement

RFC 0001 (Web Capture Archive Protocol) places payload normalization —
stripping per-run variable fields like PDF `/CreationDate`, PNG `tIME`
chunks, and MHTML `Date:` headers — in the **capturer** role. During
the initial implementation in chrest, a question surfaced: is artifact
smoothing actually a capture concern, or a consumer-of-the-archive
concern that belongs to the orchestrator (nebulous)? The question
matters because normalization pulls heavy format-parser dependencies
(e.g. pdfcpu) into the capturer, and because rules for what to strip
are plausibly archive-policy-specific rather than capture-specific.

## Decision Drivers

* Capturer is used by tools other than nebulous (interactive capture,
  one-shot PDFs, CI smoke tests); those users pay a PDF-parser
  dependency cost they do not benefit from.
* Normalization rules are plausibly archive-policy-specific: different
  orchestrators may want different stripping aggressiveness (e.g.
  "keep `/CreationDate` for legal-preservation archives, strip it for
  deduplication-focused archives").
* Archive dedup, policy, retention, and the content-addressed store
  all already live on the orchestrator side; format parsing would
  colocate with those concerns.
* Counter-driver: every orchestrator would then need to implement
  format parsers, duplicating code that chrest already has. A single
  capturer-side parser is cheaper in total lines.
* The capturer has the raw bytes in the hottest possible state; doing
  normalization there is the zero-copy path.
* Spec drift risk: if normalization moves to the orchestrator,
  different orchestrators will drift, and cross-archive comparison
  becomes harder.

## Considered Options

1. **Status quo (RFC 0001 v1 as-drafted).** Normalization is the
   capturer's responsibility. Capturer emits normalized payload +
   envelope recording what was stripped.
2. **Orchestrator-owned normalization.** Capturer emits raw bytes and
   a transport-metadata envelope. Orchestrator parses, normalizes,
   and re-stores as a normalized payload; optionally keeps the raw
   payload alongside.
3. **Split responsibility.** Capturer does cheap format-local envelope
   stripping (e.g. PNG `tIME`); orchestrator does deeper
   format-parsing (e.g. PDF object rewriting).
4. **Capturer-side optional normalization.** Normalization is a
   capturer feature gated behind a flag (`--normalize={off,archive}`);
   non-archival users opt out of the pdfcpu dependency.

## Decision Outcome

**Chosen direction: Option 2 (orchestrator-owned normalization) —
*proposed, not yet ratified*.** We adopt this as the likely eventual
shape of the protocol, pending a full protocol-level review and a
superseding revision of RFC 0001.

In the interim:

* **Shipping implementation remains Option 1** — chrest stages 1-3
  include normalizers, conformant with RFC 0001 v1 as-drafted.
* **No nebulous-side normalization work is started yet.** We do not
  want two implementations of PDF normalization in play during the
  window where this ADR is still `proposed`.
* **When this ADR is promoted to `accepted`**, the protocol cutover
  is: RFC 0001 gets a v2 in which § Payload Artifact no longer
  describes normalization, § Envelope Artifact drops the
  `stripped.*` subtree, and a new § on the orchestrator describes
  normalization rules. chrest's normalizers then get factored out
  into a shared library (importable by nebulous and any other
  orchestrator) or ported into nebulous directly.

Y-statement: chosen option 2, because it colocates archive-specific
format-parsing with the archive consumer that defines the rules,
accepting duplication risk across orchestrators and a future RFC v2
break.

### Consequences

* **Good**: chrest returns to a lean capture tool without pdfcpu or
  format-specific rewriters; non-archival users pay nothing extra.
* **Good**: nebulous can evolve normalization rules without coupling
  to chrest release cycles — we can experiment with different
  stripping aggressiveness per policy, something capturer-owned
  normalization makes awkward.
* **Good**: aligns with the existing separation — dedup, retention,
  archive records, and policy all already live orchestrator-side;
  normalization joins its cohort.
* **Bad**: every capturer now emits strictly raw bytes, so the
  envelope artifact becomes less interesting (transport metadata
  only, no `stripped.*`). Some useful "here's what was stripped"
  introspection gets lost unless preserved in the archive record.
* **Bad**: any future capturer beyond chrest (e.g. a future
  `monolith`-wrapping orchestrator) would require the orchestrator to
  implement format parsers. If there are N orchestrators, there are
  now N format parsers instead of 1.
* **Bad**: requires a real RFC 0001 v2 once accepted, with migration
  guidance for any other implementors.
* **Neutral**: chrest's current normalizer work (stages 1-3, commits
  through `0ab0cd6`) is not wasted — the code moves rather than
  gets thrown away. Factored correctly, the same Go package serves
  as a library under either architecture.

### Confirmation

This ADR is `proposed`. Confirmation path:

1. Protocol-level review (both session participants + any other
   RFC 0001 stakeholders).
2. If adopted: draft a superseding RFC (0001 → 0002 or 0001-v2)
   that moves § Payload Artifact normalization to an orchestrator
   chapter and drops `envelope.stripped.*`.
3. Factor chrest's normalizers into a shared Go package (under
   `github.com/amarbel-llc/<something>/normalize` or similar) or
   port them into `internal/<tier>/normalize/` in nebulous.
4. Update archive_capture.bats conformance tests to assert the new
   division of responsibility.

If the protocol review rejects this, update this ADR's status to
`rejected` and retain it as a record of why the status quo was kept.

## Pros and Cons of the Options

### Option 1: Status quo (capturer-owned normalization)

* Good, because the capturer has the raw bytes fresh (zero-copy path).
* Good, because one format-parser implementation serves all
  orchestrators.
* Good, because chrest's stages 1-3 are already shipped in this shape.
* Bad, because non-archival capturer users pay for pdfcpu and any
  future format-parser dependencies.
* Bad, because normalization policy is effectively hard-coded per
  format; orchestrators can't easily vary it.
* Neutral, because the envelope's `stripped.*` subtree gives useful
  audit info about what was removed.

### Option 2: Orchestrator-owned normalization

* Good, because archive-specific logic colocates with the archive
  consumer.
* Good, because orchestrators can vary normalization rules per
  policy without touching the capturer.
* Good, because the capturer stays generic — a wider audience can
  reuse it.
* Bad, because N orchestrators means N format-parser copies.
* Bad, because requires an RFC 0001 v2 once formalized.

### Option 3: Split (capturer does envelope, orchestrator does format)

* Good, because PNG `tIME` stripping is trivial and arguably belongs
  with the capturer (it's envelope, not content).
* Good, because the heavy parsers (PDF, MHTML) live with the consumer.
* Neutral, because the line between "envelope" and "format" is
  subjective and will generate bikeshedding.
* Bad, because the resulting protocol has two places normalization
  can happen, which complicates conformance.

### Option 4: Capturer optional normalization flag

* Good, because non-archival users opt out; archival users opt in.
* Good, because no protocol change needed.
* Neutral, because the capturer still carries the format-parser dep
  (the flag only gates invocation, not the build).
* Bad, because doesn't address the orchestrator-specific policy
  concern at all; it's a cost-mitigation patch, not a design choice.

## More Information

* **Related**: RFC 0001 (Web Capture Archive Protocol),
  `docs/rfcs/0001-web-capture-archive-protocol.md`. § Payload
  Artifact is the section most affected if this ADR is accepted.
* **Implementation commits on chrest side**: `5062fe4` (capture-batch
  MVP, split=false only), `9f9568f` (stages 1-2: text + PNG
  normalizers), `0ab0cd6` (stage 3: PDF normalizer via pdfcpu).
* **Raised by**: the chrest-side session (`chrest/eager-poplar`)
  during implementation of stage 3 (PDF normalizer). See the aim/
  session bus log for the original conversation.
