---
status: proposed
date: 2026-04-19
---

# Web Capture Archive Protocol

## Abstract

This document specifies a protocol for preserving web page content in a
content-addressed blob store. An *orchestrator* drives a *capturer* to fetch
a URL in zero or more formats (PDF, screenshot, MHTML, rendered text,
HTML archive, markdown, accessibility tree), and the capturer streams each
result through a generic *writer* into a content-addressed store. Every
capture produces three
content-addressed artifacts — a spec describing the capture configuration
and environment, an envelope describing per-run transport metadata, and a
payload containing the captured bytes — plus a plain-JSON archive record
owned by the orchestrator that cross-references them.

## Introduction

Stored pointers to the web decay. When a news aggregator, bookmarking tool,
or research notebook records a URL, that URL's content may disappear or
change silently. Preserving a copy of the content at the moment of
bookmarking extends the useful lifetime of the pointer well past the
lifetime of the source.

This RFC specifies an interface between three components that together
implement a preservation pipeline:

- An **orchestrator** that decides when and what to capture (for example,
  `nebulous` capturing a newly-starred story).
- A **capturer** that drives a headless browser to produce the captured
  bytes (for example, `chrest`).
- A **writer** that accepts bytes and returns a content-addressed identifier
  (for example, `madder`).

The protocol is designed so that each role can be implemented independently.
A different capturer (monolith, wkhtmltopdf) or writer (S3 shim, local
filesystem) MAY be substituted without changes to the other components, as
long as the substitute conforms to this specification.

### Scope

This document specifies:

- The wire format between orchestrator and capturer (a JSON-in / JSON-out
  batch command).
- The wire format between capturer and writer (a byte-stream-in,
  single-JSON-object-out CLI contract).
- The schemas of the three content-addressed artifacts produced per capture
  (spec, envelope, payload).
- The schema of the orchestrator-owned archive record.
- The schema of the orchestrator's policy file.

### Out of Scope

This document does not specify:

- How the orchestrator discovers URLs to capture, or how it decides when to
  trigger a capture. Those are orchestrator policy concerns.
- The internals of the headless browser, page fetch, or rendering pipeline
  in the capturer. This RFC is concerned only with the bytes the capturer
  emits and the metadata it records about how those bytes were produced.
- The on-disk layout or implementation of the writer's content-addressed
  store. The writer is a black box accessed through a narrow CLI contract.
- Retrieval, search, or presentation of captured archives.
- Cross-archive deduplication policy, garbage collection, or retention.

### Background

Informative references for context are listed under [References](#references).

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be
interpreted as described in RFC 2119.

## Specification

### Terminology

- **Orchestrator** — The component that initiates captures, maintains the
  policy file, generates capturer input, and writes archive records.
- **Capturer** — The component that accepts a batch of capture requests,
  drives a headless browser to produce bytes, and streams them through the
  writer.
- **Writer** — A CLI program that accepts bytes on standard input and emits
  one JSON object on standard output containing a content-addressed
  identifier for those bytes.
- **Blob** — An opaque byte sequence stored by the writer and retrievable by
  its content-addressed identifier.
- **Content-Addressed Identifier (markl ID)** — A self-describing,
  checksummed identifier of the form `<algorithm>-<blech32 digest>` (for
  example, `blake2b256-9ft3m74l5t2ppwjrvfg3wp380jqj2zfrm6zevxqx34sdethvey0s5vm9gd`).
  Defined by [markl-id][markl-id].
- **JCS** — JSON Canonicalization Scheme, per [RFC 8785][rfc-8785]. Used in
  this specification to produce a deterministic byte sequence from a JSON
  value prior to hashing or storage.
- **Policy** — A named, user-authored configuration describing a set of
  captures to perform for a given URL. Serialized as TOML in the
  orchestrator's policy file.
- **Capture** — A single `(format, browser, options, environment)` tuple
  applied to one URL, producing one spec artifact, one payload artifact,
  and optionally one envelope artifact (when `split` is true).
- **Spec Artifact** — A JCS-canonicalized JSON document describing the
  capture's configuration and the resolved browser and host environment.
  Its markl ID is the capture's canonical identity.
- **Envelope Artifact** — A JCS-canonicalized JSON document describing the
  per-run transport metadata (HTTP headers, timing, stripped per-run fields).
- **Payload Artifact** — The captured bytes themselves, optionally
  normalized per the rules in [§ Payload Artifact](#payload-artifact).
- **Archive Record** — A plain-JSON file written by the orchestrator
  referencing the three artifact markl IDs for each capture in a policy.
  Not content-addressed.
- **Normalization** — The act of stripping per-run variable data from a
  captured byte stream (e.g., `/CreationDate` from a PDF) before writing it
  to the blob store, to improve the likelihood that two captures of an
  unchanged resource produce the same payload markl ID.

### Architecture Overview

Three roles exchange data along two interfaces:

```
   ┌──────────────┐   JSON-in/-out     ┌───────────┐   bytes-in/JSON-out   ┌────────┐
   │ Orchestrator │ ─────────────────> │  Capturer │ ────────────────────> │ Writer │
   │  (nebulous)  │                    │  (chrest) │                       │(madder)│
   │              │ <───── JSON ────── │           │ <── markl ID JSON ─── │        │
   └──────────────┘                    └───────────┘                       └────────┘
          │                                  │                                 │
          │ writes archive record            │ spawns writer per artifact      │
          │                                  │ (2× or 3× per capture)          │
          ▼                                  ▼                                 ▼
   archive record                      captured bytes                    blob store
   (plain JSON file)                   (streamed, not buffered)          (content-addressed)
```

Each capture produces up to three content-addressed artifacts written to
the blob store:

1. The **payload** — the captured bytes.
2. The **envelope** — per-run metadata (HTTP response, timing). Produced
   only when the capture's resolved `split` is `true`.
3. The **spec** — capture configuration and the resolved browser and host
   environment. Its markl ID is the capture's canonical identity.

The **archive record** is a plain-JSON file owned by the orchestrator that
references the three artifact markl IDs. It is not content-addressed and
MAY be rewritten by the orchestrator.

An **orchestrator** MUST NOT write to the blob store directly; all blob
writes flow through the capturer via the writer command the orchestrator
supplies. This keeps the orchestrator unaware of writer-specific details
and allows the writer to be substituted independently.

A **capturer** MUST NOT retain captured bytes after emitting the batch
result, and SHOULD stream bytes into the writer rather than buffering the
full capture in memory.

A **writer** MUST be a plain CLI program matching the contract in
[§ Writer Protocol](#writer-protocol). It MUST NOT require out-of-band
configuration beyond what can be expressed in its command-line arguments.

### Writer Protocol

A writer is a CLI program invoked by the capturer. The capturer spawns one
writer process per artifact (spec, envelope, or payload).

#### Invocation

The capturer MUST spawn the writer with the exact argv supplied in the
batch input's `writer.cmd` field (see [§ Capturer Protocol](#capturer-protocol)).
The capturer MUST NOT shell-interpret this argv; it is passed directly to
the OS `exec` primitive.

The capturer MUST connect:
- The artifact's raw bytes to the writer's standard input.
- An open file descriptor to the writer's standard output (for the result).
- An open file descriptor to the writer's standard error (for diagnostics).

The capturer MUST close the writer's standard input after writing all bytes
to signal end-of-stream.

#### Output

The writer MUST write exactly one JSON object to standard output on success,
terminated by a single `\n`. The writer MUST NOT write any other bytes to
standard output — no preamble, no trailing content, no additional objects.

The output object MUST contain:

| Field       | Type    | Required | Description |
|-------------|---------|----------|-------------|
| `id`        | string  | yes      | A markl ID (self-describing content identifier). |
| `size`      | integer | yes      | Count of bytes read from stdin. MUST be ≥ 0. |

The output object MAY contain additional fields. Consumers MUST ignore
unknown fields.

Example:

```json
{"id":"blake2b256-9ft3m74l5t2ppwjrvfg3wp380jqj2zfrm6zevxqx34sdethvey0s5vm9gd","size":12034}
```

#### Errors

On failure, the writer MUST exit with a non-zero status. The writer SHOULD
write human-readable diagnostics to standard error. The capturer MUST
treat any non-zero exit as a failure and MUST NOT parse standard output in
that case.

The writer MUST NOT use standard output for diagnostics, progress, or
partial results.

#### Streams

The writer MAY begin reading stdin before its stdin is closed. The writer
MUST NOT require the full stdin to be buffered before beginning work, so
that the capturer can stream multi-megabyte captures without local
buffering.

### Capturer Protocol

The capturer is invoked as a CLI program. The orchestrator writes a single
JSON document to the capturer's standard input and reads a single JSON
document from its standard output.

#### Invocation

The orchestrator MUST invoke the capturer with a subcommand that accepts
the batch input format defined below. Implementations of this protocol
SHOULD name that subcommand `capture-batch` or `capture`.

The capturer MUST read its full input as a single JSON object from standard
input and close input processing on end-of-file.

#### Batch Input

The batch input is a JSON object with the following shape:

```json
{
  "schema": "web-capture-archive/v1",
  "writer": {
    "cmd": ["madder", "--format=json", "write", "--store", "nebulous"]
  },
  "url": "https://example.com/article",
  "defaults": {
    "browser":   "firefox",
    "isolation": "fresh",
    "split":     true
  },
  "captures": [
    {
      "name":    "pdf-clean",
      "format":  "pdf",
      "options": { "background": true, "landscape": false },
      "browser": "firefox",
      "extensions": [{ "id": "ublock-origin", "version": "1.62.0" }]
    },
    {
      "name":   "text",
      "format": "text"
    }
  ]
}
```

Input field requirements:

| Field                       | Type    | Required | Description |
|-----------------------------|---------|----------|-------------|
| `schema`                    | string  | yes      | MUST be `web-capture-archive/v1`. |
| `writer.cmd`                | array of strings | yes | Argv passed to the writer (see [§ Writer Protocol](#writer-protocol)). MUST have at least one element. |
| `url`                       | string  | yes      | Concrete URL to capture. MUST be a fully-qualified URL; the orchestrator MUST perform any templating before invoking the capturer. |
| `defaults`                  | object  | no       | Defaults applied to each capture entry that does not override. |
| `defaults.browser`          | string  | no       | Default browser backend. MUST be one of: `chrome`, `firefox`. |
| `defaults.isolation`        | string  | no       | Default browser isolation. MUST be one of: `fresh`, `session`, `shared`. If omitted, the capturer MUST treat it as `fresh`. |
| `defaults.split`            | boolean | no       | Default `split` value. If omitted, the capturer MUST treat it as `true`. |
| `captures`                  | array   | yes      | Non-empty list of captures to perform, in order. |
| `captures[].name`           | string  | yes      | Orchestrator-supplied label. MUST be unique within the batch. MUST NOT be included in the spec artifact (see [§ Capture Spec Artifact](#capture-spec-artifact)). |
| `captures[].format`         | string  | yes      | Capture format. MUST be one of: `text`, `pdf`, `screenshot`, `mhtml`, `a11y`, `html-monolith`, `markdown-full`, `markdown-reader`, `markdown-selector`. |
| `captures[].options`        | object  | no       | Format-specific options. Passed through to the browser driver; see [§ Payload Artifact](#payload-artifact). |
| `captures[].browser`        | string  | no       | Overrides `defaults.browser`. |
| `captures[].isolation`      | string  | no       | Overrides `defaults.isolation`. |
| `captures[].split`          | boolean | no       | Overrides `defaults.split`. |
| `captures[].extensions`     | array of objects | no | Browser extensions to load. Each element is an object with string fields `id` and `version`, and OPTIONAL string field `manifest_digest` (markl ID). |
| `captures[].flags`          | array of strings | no | Additional browser command-line flags. |

The capturer MUST reject batch input with `schema` not equal to
`web-capture-archive/v1` by exiting non-zero without writing a batch output.

#### Batch Output

On a successful batch run — whether or not individual captures succeeded —
the capturer MUST write exactly one JSON object to standard output. It has
the following shape:

```json
{
  "schema": "web-capture-archive/v1",
  "capturer": {
    "name":    "chrest",
    "version": "…"
  },
  "errors": [],
  "captures": [
    {
      "name": "pdf-clean",
      "spec":     { "id": "blake2b256-…", "size": 1287,   "media_type": "application/vnd.web-capture-archive.spec+json" },
      "payload":  { "id": "blake2b256-…", "size": 842103, "media_type": "application/pdf", "normalized": true },
      "envelope": { "id": "blake2b256-…", "size": 892,    "media_type": "application/vnd.web-capture-archive.envelope+json" }
    },
    {
      "name": "text",
      "error": { "kind": "fetch-failed", "message": "connection reset" }
    }
  ]
}
```

Output field requirements:

| Field                        | Type    | Required | Description |
|------------------------------|---------|----------|-------------|
| `schema`                     | string  | yes      | MUST be `web-capture-archive/v1`. |
| `capturer.name`              | string  | yes      | Identifier of the capturer implementation. |
| `capturer.version`           | string  | yes      | Version string of the capturer implementation. |
| `errors`                     | array of error objects | yes | Batch-wide errors. MUST be `[]` if the batch completed with per-capture resolution. |
| `captures`                   | array   | yes      | One entry per input capture, in input order. MUST have the same length as the input `captures` array. |
| `captures[].name`            | string  | yes      | Echo of the input `name`. |
| `captures[].spec`            | artifact ref | conditional | Present iff the capture succeeded. |
| `captures[].payload`         | artifact ref | conditional | Present iff the capture succeeded. |
| `captures[].envelope`        | artifact ref | conditional | Present iff the capture succeeded AND `split` was true. |
| `captures[].error`           | error object | conditional | Present iff the capture failed. MUST NOT be present alongside `payload`. |

An **artifact ref** is a JSON object with:

| Field         | Type    | Required | Description |
|---------------|---------|----------|-------------|
| `id`          | string  | yes      | markl ID returned by the writer. |
| `size`        | integer | yes      | Bytes written. |
| `media_type`  | string  | yes      | IANA-style media type identifying the artifact contents. |
| `normalized`  | boolean | no       | Present only on payload refs. `true` if the bytes were normalized per [§ Payload Artifact](#payload-artifact). |

An **error object** has:

| Field     | Type   | Required | Description |
|-----------|--------|----------|-------------|
| `kind`    | string | yes      | Short machine-readable error category. |
| `message` | string | yes      | Human-readable description. |

Batch-level errors (`errors[]`) are reserved for failures that were
detected after input parsing but that prevented per-capture resolution
for one or more captures (e.g., the only available browser failed to
launch, the writer command could not be spawned at all, a required host
probe failed). Per-capture errors (`captures[].error`) are reserved for
failures affecting only one capture (URL fetch failed for that capture,
format unsupported by the selected browser, writer rejected the
artifact, etc.).

Input that fails schema validation (missing required fields, unknown
`schema` string, malformed JSON) MUST cause the capturer to exit
non-zero without writing a batch output, per [§ Batch Input](#batch-input).
Such failures do NOT appear in `errors[]`.

#### Capture Execution Order

The capturer MUST execute captures in the order given by the input
`captures` array. Execution MUST NOT be parallelized unless the capturer
can guarantee that doing so does not affect any capture's resulting spec,
envelope, or payload artifacts.

#### Writer Invocation

For each successful capture, the capturer MUST invoke the writer in the
following order, skipping the envelope invocation when `split` is `false`:

1. Once for the payload bytes.
2. Once for the envelope artifact. MUST be skipped iff `split` is false.
3. Once for the spec artifact.

Thus each successful capture invokes the writer either two or three times,
depending on `split`. Each invocation MUST conform to
[§ Writer Protocol](#writer-protocol). The capturer MUST NOT reuse a
single writer process across multiple artifacts.

### Artifact Formats

Every capture produces three artifacts — spec, envelope (optional), and
payload — each stored as a blob addressed by its markl ID. The spec and
envelope are JSON; the payload is format-specific bytes.

#### Capture Spec Artifact

The spec artifact is a JSON document containing the capture's configuration
combined with the capturer's resolved browser and host fingerprint. The
capturer MUST produce this document, JCS-canonicalize it per [RFC 8785][rfc-8785],
and write the canonicalized bytes to the writer.

Media type: `application/vnd.web-capture-archive.spec+json`.

Schema:

```json
{
  "schema": "web-capture-archive.spec/v1",
  "capture": {
    "format":    "pdf",
    "options":   { "background": true, "landscape": false },
    "isolation": "fresh",
    "split":     true
  },
  "browser": {
    "name":         "firefox",
    "version":      "149.0.2",
    "user_agent":   "Mozilla/5.0 (…) Gecko/20100101 Firefox/149.0.2",
    "js_engine":    "SpiderMonkey",
    "platform":     "Linux x86_64",
    "command_line": ["--headless", "--remote-debugging-port=0", "--no-remote"],
    "prefs":        { },
    "extensions": [
      { "id": "ublock-origin", "version": "1.62.0", "manifest_digest": "blake2b256-…" }
    ]
  },
  "host": {
    "os":           "linux",
    "kernel":       "6.17.0-20-generic",
    "arch":         "x86_64",
    "libc":         "glibc 2.41",
    "fonts_digest": "blake2b256-…",
    "gpu":          { "vendor": "…", "model": "…", "driver": "…" }
  },
  "capturer": {
    "name":    "chrest",
    "version": "…"
  }
}
```

Field requirements:

| Field                        | Required | Description |
|------------------------------|----------|-------------|
| `schema`                     | yes      | MUST be `web-capture-archive.spec/v1`. |
| `capture.format`             | yes      | Echo of the batch input capture format. |
| `capture.options`            | yes      | Echo of the batch input options; `{}` if none. |
| `capture.isolation`          | yes      | Resolved isolation (after defaults applied). |
| `capture.split`              | yes      | Resolved split value (after defaults applied). |
| `browser.name`               | yes      | Browser backend name. |
| `browser.version`            | yes      | Browser version string as reported by the browser. |
| `browser.user_agent`         | yes      | User agent string. |
| `browser.platform`           | yes      | Browser-reported platform string. |
| `browser.js_engine`          | no       | JavaScript engine name (e.g., `V8`, `SpiderMonkey`). |
| `browser.command_line`       | no       | Command-line arguments passed to the browser process. Array of strings. When present, MUST be recorded in the order the capturer passed them. |
| `browser.prefs`              | no       | Browser preferences applied for this capture. Object with string keys. Implementations SHOULD include only rendering-affecting preferences. |
| `browser.extensions`         | yes      | Loaded extensions. MUST be `[]` if none. |
| `browser.extensions[].id`    | yes      | Extension identifier. |
| `browser.extensions[].version` | yes    | Extension version string. |
| `browser.extensions[].manifest_digest` | no | markl ID of the extension's manifest file. RECOMMENDED when the extension was loaded from a local path rather than a known registry version. |
| `host.os`                    | yes      | Operating system name. |
| `host.kernel`                | yes      | Kernel version string. |
| `host.arch`                  | no       | CPU architecture (e.g., `x86_64`, `aarch64`). |
| `host.libc`                  | no       | C library name and version. |
| `host.fonts_digest`          | no       | markl ID over the sorted font-file list available to the browser. |
| `host.gpu`                   | no       | GPU information where obtainable. Object with string fields `vendor`, `model`, `driver` when present. |
| `capturer.name`              | yes      | Identifier of the capturer implementation. |
| `capturer.version`           | yes      | Version of the capturer. |

The spec artifact's markl ID is the **capture identity**. Two captures that
produce identical spec bytes are considered interchangeable for
identity-matching purposes; this is the basis for cross-archive
deduplication and drift detection.

The spec artifact MUST NOT contain any time-varying data (`captured_at`,
per-run random IDs, etc.). Per-run data belongs in the envelope artifact.

#### Envelope Artifact

The envelope artifact is a JSON document containing the per-run transport
metadata associated with the capture. The capturer MUST produce this
document when the capture's resolved `split` is `true`, JCS-canonicalize it
per [RFC 8785][rfc-8785], and write the canonicalized bytes to the writer.

Media type: `application/vnd.web-capture-archive.envelope+json`.

Schema:

```json
{
  "schema": "web-capture-archive.envelope/v1",
  "url": "https://example.com/article",
  "captured_at": "2026-04-19T12:00:00.412Z",
  "http": {
    "status": 200,
    "headers": {
      "content-type": "text/html; charset=utf-8",
      "date": "Sun, 19 Apr 2026 12:00:00 GMT"
    },
    "timing_ms": { "dns": 12, "tcp": 31, "tls": 84, "ttfb": 187, "load": 412 }
  },
  "stripped": {
    "pdf": {
      "CreationDate": "D:20260419120000Z",
      "ID":           ["…", "…"]
    }
  }
}
```

Field requirements:

| Field                        | Required | Description |
|------------------------------|----------|-------------|
| `schema`                     | yes      | MUST be `web-capture-archive.envelope/v1`. |
| `url`                        | yes      | Concrete URL that was captured. Echo of the batch input `url`. |
| `captured_at`                | yes      | RFC 3339 timestamp with millisecond precision, in UTC. |
| `http.status`                | yes      | HTTP status code of the top-level document fetch. |
| `http.headers`               | yes      | HTTP response headers of the top-level document. Header names MUST be lowercased. |
| `http.timing_ms`             | no       | Network timing observations in milliseconds. |
| `stripped`                   | no       | Per-format object recording fields that were removed from the payload during normalization. Required when any normalization was performed. |

The capturer SHOULD record any field it stripped from the payload into
`stripped` so that a consumer can reconstruct an approximation of the
original bytes if needed.

If the capture's resolved `split` is `false`, the capturer MUST NOT produce
an envelope artifact, and the batch output MUST omit the `envelope` field
on that capture's entry.

#### Payload Artifact

The payload artifact is the captured bytes. The capturer writes them to the
writer unchanged, unless the capture's resolved `split` is `true`, in which
case the capturer MUST apply the format-specific normalization rules in
this section before writing.

Normalization is best-effort and format-specific. The capturer MUST NOT
claim `normalized: true` on the payload artifact reference if it did not
apply the normalization rules defined here. The capturer MUST set
`normalized: false` (or omit the field) if the bytes were written
as-captured.

Media types per format:

| Format              | Media type                     |
|---------------------|--------------------------------|
| `text`              | `text/plain; charset=utf-8`    |
| `pdf`               | `application/pdf`              |
| `screenshot`        | `image/png` or `image/jpeg`    |
| `mhtml`             | `multipart/related`            |
| `a11y`              | `application/json`             |
| `html-monolith`     | `text/html; charset=utf-8`     |
| `markdown-full`     | `text/markdown; charset=utf-8` |
| `markdown-reader`   | `text/markdown; charset=utf-8` |
| `markdown-selector` | `text/markdown; charset=utf-8` |

##### text

The capturer SHOULD normalize line endings to `\n` and strip trailing
whitespace on each line when `split` is true.

##### pdf

When `split` is true, the capturer SHOULD:

- Remove or zero the PDF trailer `/ID`.
- Remove or zero `/CreationDate` and `/ModDate` entries in the document
  information dictionary.
- Remove the `/Producer` string.
- Use a deterministic zlib configuration for any compressed streams
  generated by the capturer (fixed compression level, no timestamps).

The removed fields MUST be recorded in `envelope.stripped.pdf`.

Perfect byte-determinism across runs is not required and is unlikely to be
achievable for pages containing JS-rendered content, dynamic resources, or
timestamped content. The normalization SHOULD remove envelope-level drift
but need not address in-page drift.

##### screenshot

When `split` is true, the capturer SHOULD:

- For PNG output: strip the `tIME` chunk and any `tEXt`, `zTXt`, or `iTXt`
  chunks whose keyword identifies non-deterministic data.
- Use a deterministic compression configuration.

The removed chunks MUST be recorded in `envelope.stripped.screenshot`.

##### mhtml

When `split` is true, the capturer SHOULD:

- Replace the outer MIME boundary with a fixed, implementation-defined
  string.
- Remove `Date:` headers from individual parts.
- Order parts deterministically (e.g., lexicographically by Content-ID).

The original boundary and removed headers MUST be recorded in
`envelope.stripped.mhtml`.

##### a11y

The accessibility tree MUST be JCS-canonicalized per [RFC 8785][rfc-8785]
when `split` is true.

##### html-monolith

No byte-stability normalization rules are defined for this format in this
revision of the RFC. A conforming capturer MUST reject `split = true` for
`html-monolith` — e.g. by emitting a per-capture error with an
implementation-defined code (chrest currently returns `not-implemented`).
A future RFC revision MAY define normalization rules.

##### markdown-full

No byte-stability normalization rules are defined for this format in this
revision of the RFC. A conforming capturer MUST reject `split = true` for
`markdown-full` — e.g. by emitting a per-capture error with an
implementation-defined code (chrest currently returns `not-implemented`).
A future RFC revision MAY define normalization rules.

##### markdown-reader

No byte-stability normalization rules are defined for this format in this
revision of the RFC. A conforming capturer MUST reject `split = true` for
`markdown-reader` — e.g. by emitting a per-capture error with an
implementation-defined code (chrest currently returns `not-implemented`).
A future RFC revision MAY define normalization rules.

The capture's `options` object MAY include an optional `reader_engine`
string. Defined values: `"readability"` (default) selects a Readability-style
article extractor; `"browser"` is reserved for a future engine and a
conforming capturer MAY reject it as not-yet-implemented. The option is a
format-specific passthrough — the capturer interprets it; no schema-level
enforcement is imposed by this RFC.

##### markdown-selector

No byte-stability normalization rules are defined for this format in this
revision of the RFC. A conforming capturer MUST reject `split = true` for
`markdown-selector` — e.g. by emitting a per-capture error with an
implementation-defined code (chrest currently returns `not-implemented`).
A future RFC revision MAY define normalization rules.

The capture's `options` object MUST include a `selector` string — a CSS
selector scoping the conversion to a single element (the first match wins).
A conforming capturer MUST reject a `markdown-selector` capture that
omits or empty-strings the `selector` option.

### Archive Record Format

The archive record is a JSON file owned by the orchestrator that ties
together the three content-addressed artifacts per capture, along with
metadata about when and under what policy the capture was performed.

The archive record is **not** content-addressed. It MAY be rewritten by
the orchestrator (e.g., to correct a policy ID, re-run a failed capture,
or record additional captures against an existing story).

#### Path

The orchestrator SHOULD place archive records at a deterministic path
derived from the capture subject and policy. The RECOMMENDED convention is:

```
<orchestrator-data-root>/archives/<subject>/<policy_id>.json
```

where `<subject>` is a stable identifier for the captured target
(orchestrator-defined) and `<policy_id>` is the policy's `id` field. This
convention gives one archive record per `(subject, policy)` pair.

An orchestrator MAY choose an alternative path convention as long as
records remain uniquely addressable.

#### Schema

```json
{
  "schema": "web-capture-archive.record/v1",
  "subject": "6327282:5d1cf5",
  "url": "https://example.com/article",
  "policy_id": "starred-default",
  "captured_at": "2026-04-19T12:00:00.412Z",
  "captures": [
    {
      "name": "pdf-clean",
      "spec":     { "id": "blake2b256-…", "size": 1287,   "media_type": "application/vnd.web-capture-archive.spec+json" },
      "payload":  { "id": "blake2b256-…", "size": 842103, "media_type": "application/pdf", "normalized": true },
      "envelope": { "id": "blake2b256-…", "size": 892,    "media_type": "application/vnd.web-capture-archive.envelope+json" }
    },
    {
      "name":  "text",
      "error": { "kind": "fetch-failed", "message": "connection reset" }
    }
  ],
  "errors": []
}
```

Field requirements:

| Field                        | Required | Description |
|------------------------------|----------|-------------|
| `schema`                     | yes      | MUST be `web-capture-archive.record/v1`. |
| `subject`                    | yes      | Orchestrator-defined identifier for what was captured (story ID, URL hash, etc.). |
| `url`                        | yes      | Concrete URL that was captured. |
| `policy_id`                  | yes      | Value of the originating policy's `id` field. Used as a grouping label; the protocol assigns no semantic meaning to the value beyond string equality. |
| `captured_at`                | yes      | RFC 3339 timestamp with millisecond precision, in UTC. For each capture in which `split` was true, this value MUST match the `captured_at` in that capture's envelope artifact. |
| `captures`                   | yes      | Array of capture entries, in batch-execution order. MUST have the same length as the batch input's `captures` array. |
| `captures[].name`            | yes      | Echo of the batch input capture name. |
| `captures[].spec`            | conditional | Present iff the capture succeeded. MUST be an artifact ref. |
| `captures[].payload`         | conditional | Present iff the capture succeeded. MUST be an artifact ref. |
| `captures[].envelope`        | conditional | Present iff the capture succeeded AND `split` was true. MUST be an artifact ref. |
| `captures[].error`           | conditional | Present iff the capture failed. MUST have `kind` and `message` string fields. MUST NOT be present alongside `payload`. |
| `errors`                     | yes      | Array of batch-level errors (see [§ Capturer Protocol](#capturer-protocol)). |

#### Writing

The orchestrator MUST write the archive record atomically (write to a
temporary file in the same directory, then `rename(2)` over the target
path). Partial or truncated archive records MUST NOT be visible to
readers.

The orchestrator MUST NOT rewrite an archive record in place; any update
MUST go through the same atomic write path.

Archive records MAY be serialized with any whitespace (pretty-printed or
compact); they are not content-addressed and need not be canonicalized.

#### Error Kind Taxonomy

Error `kind` strings surfaced by the archive pipeline appear at three
distinct sites, each owned by a different component. This section
enumerates the orchestrator-owned kinds and points to the capturer for
the rest, so programmatic consumers (alerting, retry logic, dashboards)
have a single place to look.

**Orchestrator-owned.** Top-level job failures surface as
`Report.Failed[].Kind` entries in the orchestrator's run report. These
are NOT persisted in an archive record — a failed job never writes one
— but they are the kinds a CLI caller sees when a run produces failures
rather than artifacts.

| Kind                     | Site                    | Meaning                                                                                                                                 |
|--------------------------|-------------------------|-----------------------------------------------------------------------------------------------------------------------------------------|
| `policy-load-failed`     | pre-job (Report.Failed) | Policy file missing, malformed, or failed validation; no jobs were attempted.                                                           |
| `story-resolve-failed`   | per-job (Report.Failed) | A story selector was supplied but the story is not in the local newsblur store.                                                         |
| `template-expand-failed` | per-job (Report.Failed) | The policy's `url` template referenced a field unavailable in the template context.                                                     |
| `capturer-failed`        | per-job (Report.Failed) | The capturer subprocess exited non-zero or returned malformed batch output. Message embeds a capturer-invocation kind (see below).      |
| `archive-write-failed`   | per-job (Report.Failed) | The atomic rename into the archive root failed, or a prior record could not be stored in the history blob store.                        |

**Capturer-invocation kinds** appear embedded in `capturer-failed`
messages when the orchestrator could not interpret the capturer's batch
output. These describe protocol-level failures between orchestrator and
capturer, not per-capture or per-URL failures:

| Kind             | Meaning                                                                      |
|------------------|------------------------------------------------------------------------------|
| `nonzero-exit`   | Capturer process exited non-zero.                                            |
| `empty-stdout`   | Capturer exited zero but wrote no output.                                    |
| `bad-json`       | Capturer stdout was not a single valid JSON document.                        |
| `trailing-data`  | Capturer output contained content after the JSON document.                   |
| `bad-shape`      | Capturer output decoded but failed structural validation (wrong `schema`, missing required fields, etc.). |

**Capturer-owned** kinds land directly in the archive record:

- `captures[].error.kind` — per-capture failures owned by the capturer
  and versioned with it. This RFC deliberately does not duplicate the
  list, since new kinds may appear across capturer versions; the
  authoritative enumeration lives in the capturer's documentation.
- `errors[].kind` — batch-level failures, also capturer-owned. See
  [§ Capturer Protocol](#capturer-protocol) for the distinction between
  batch-level and per-capture errors.

### Policy Format (nebulous.toml)

The policy file is a user-authored TOML document describing a named set of
captures to perform against URLs. It is consumed by the orchestrator and
translated into the capturer batch-input JSON defined in
[§ Capturer Protocol](#capturer-protocol).

The policy file is not part of the capturer↔writer wire protocol; it is
orchestrator-local. It is specified here because the policy's identity
(`policy.id` and its canonicalized hash) is recorded in the archive
record.

Example:

```toml
[policy]
id        = "starred-default"
url       = "{{story.permalink}}"
isolation = "fresh"

[[capture]]
name    = "text"
format  = "text"
browser = "firefox"

[[capture]]
name      = "pdf-clean"
format    = "pdf"
browser   = "firefox"
split     = true
options   = { background = true, landscape = false }

  [[capture.extensions]]
  id      = "ublock-origin"
  version = "1.62.0"

[[capture]]
name      = "screenshot"
format    = "screenshot"
browser   = "firefox"
split     = true
isolation = "session"
options   = { format = "png", full-page = true }

[[capture]]
name    = "mhtml"
format  = "mhtml"
browser = "chrome"
split   = true
```

Field requirements:

| Field                           | Required | Description |
|---------------------------------|----------|-------------|
| `policy.id`                     | yes      | Stable identifier for the policy. MUST match `^[a-zA-Z0-9._-]+$`. |
| `policy.url`                    | yes      | URL template. See [§ URL Templating](#url-templating). |
| `policy.isolation`              | no       | Default browser isolation. MUST be one of: `fresh`, `session`, `shared`. If omitted, the orchestrator MUST treat it as `fresh`. |
| `capture[]`                     | yes      | At least one entry REQUIRED. |
| `capture[].name`                | yes      | Capture label. MUST be unique within the policy. MUST match `^[a-zA-Z0-9._-]+$`. |
| `capture[].format`              | yes      | Capture format. MUST be one of: `text`, `pdf`, `screenshot`, `mhtml`, `a11y`, `html-monolith`, `markdown-full`, `markdown-reader`, `markdown-selector`. |
| `capture[].browser`             | no       | Browser backend. MUST be one of: `chrome`, `firefox`. |
| `capture[].options`             | no       | Format-specific options passed to the capturer. |
| `capture[].split`               | no       | Whether to emit an envelope artifact and normalize the payload. Default: `true`. |
| `capture[].isolation`           | no       | Overrides `policy.isolation`. |
| `capture[].extensions`          | no       | Browser extensions to load. Expressed as an array of objects with `id` and `version`. |
| `capture[].flags`               | no       | Additional browser command-line flags. |

#### URL Templating

The orchestrator MUST expand URL templates in the policy's `url` field
before passing a concrete URL to the capturer. The capturer MUST NOT be
required to interpret templates.

The template syntax is not normatively specified here; it is an
orchestrator concern. Other orchestrators MAY use different syntax.

The reference implementation (`nebulous`) uses Go's `text/template`
in strict mode (`Option("missingkey=error")`), with a root context
exposing a `Story` field. Policies reference story fields as
`{{.Story.Permalink}}`, `{{.Story.Hash}}`, `{{.Story.Title}}`.
Unknown field references — e.g. a typo like `{{.Story.Prmalink}}` —
produce a template-expand error at orchestration time rather than
silently substituting an empty string.

#### Policy Hash

The orchestrator MAY compute a `policy_hash` as follows, for use in
cross-referencing policies across archive records:

1. Parse the TOML policy file into its semantic representation.
2. Serialize the representation as JSON and JCS-canonicalize per
   [RFC 8785][rfc-8785].
3. Compute the markl ID of the canonicalized bytes.

Consumers that compare policies across archives MUST compute the hash
using the same procedure. The hash is not required to be recorded in the
archive record.

### Flows

This section is informative; it describes the expected end-to-end
interaction between the three roles. Normative requirements live in the
earlier subsections.

#### Triggered Capture

```
Orchestrator                     Capturer                          Writer
     |                              |                                |
     | (1) trigger                  |                                |
     |                              |                                |
     | (2) load policy              |                                |
     |     substitute URL template  |                                |
     |     build batch input JSON   |                                |
     |                              |                                |
     | (3) exec capturer ─────────> | (4) parse input                |
     |     stdin: batch input JSON  |     launch browser(s)          |
     |                              |     per capture:               |
     |                              |       (5) fetch + render       |
     |                              |       (6) normalize payload    |
     |                              |                                |
     |                              | (7) exec writer ─────────────> | (8) read payload
     |                              |     stdin: payload bytes       |     compute markl ID
     |                              |                                |     write to store
     |                              |                           <─── |     stdout: {id, size}
     |                              | (9) exec writer (envelope) ──> | ... same ...
     |                              |                                |
     |                              | (10) build spec JSON (JCS)     |
     |                              |                                |
     |                              | (11) exec writer (spec) ─────> | ... same ...
     |                              |                                |
     |                              | (12) repeat for next capture   |
     |                              |                                |
     | <── (13) batch output JSON ──|                                |
     |     stdout: captures[], errors[]                              |
     |                                                               |
     | (14) build archive record                                     |
     |      write atomically (temp + rename)                         |
     |                                                               |
     v
  archive record on disk
```

#### Step Notes

- (1) Trigger source is orchestrator-defined: user action, scheduled job,
  external webhook, etc.
- (2) URL template substitution is orchestrator-local; see
  [§ URL Templating](#url-templating).
- (5) "Fetch" includes whatever browser-driven navigation the capturer
  requires for the requested format. The capturer MAY reuse a browser
  process across captures when `isolation` permits.
- (7–11) Each writer invocation is a separate process. The capturer MUST
  close the writer's stdin after writing all artifact bytes, then read a
  single JSON object from its stdout, then wait for exit.
- (13) On a non-zero exit from the capturer, the orchestrator MUST NOT
  attempt to parse standard output. Human-readable diagnostics SHOULD be
  read from standard error.
- (14) The orchestrator assembles the archive record by copying the
  `captures[]` and `errors[]` arrays from the capturer's output and adding
  `subject`, `url`, `policy_id`, and `captured_at` at the top level. For
  each successful capture whose resolved `split` was true, the
  orchestrator's `captured_at` MUST equal the `captured_at` recorded in
  that capture's envelope artifact.

#### Failure Modes

- **Writer spawn failure**: The capturer MUST record the affected capture
  with a `captures[].error` of `kind: "writer-failed"` and continue with
  the next capture. The capturer MUST NOT abort the batch.
- **Browser launch failure**: If the browser fails to launch and no
  captures using that browser can proceed, the capturer MUST place an
  error in batch-level `errors[]` describing the failure. Captures that
  would have used that browser MUST appear in `captures[]` with a
  `captures[].error` of `kind: "browser-unavailable"`.
- **Malformed input**: The capturer MUST exit non-zero without writing a
  batch output.
- **Writer returns malformed output**: The capturer MUST record the
  capture with a `captures[].error` of `kind: "writer-response-invalid"`
  including the writer's offending output in the `message` field.

## Security Considerations

### Writer Command Trust

The orchestrator supplies `writer.cmd` to the capturer, and the capturer
executes it via `exec`. Neither the orchestrator nor the capturer is
expected to sandbox the writer. The writer runs with the same privileges
as the capturer process, which in turn runs with the same privileges as
the orchestrator process.

Operators MUST treat the policy file and any other source of `writer.cmd`
as trusted input. An attacker with write access to the policy file can
execute arbitrary commands via the writer invocation. Implementations
SHOULD NOT accept `writer.cmd` from untrusted sources (e.g., a
network-received policy) without additional sandboxing.

### Captured Content Is Untrusted

The capturer drives a headless browser to render arbitrary URLs. Rendered
pages execute untrusted JavaScript, may attempt to exploit browser
vulnerabilities, and may probe the local network from within the browser
sandbox. Implementations SHOULD:

- Run the browser in its strictest available sandbox configuration.
- Disable browser features not needed for capture (e.g., WebRTC, service
  worker persistence) unless a capture explicitly needs them.
- Use ephemeral browser profile directories for `isolation: fresh`
  captures and delete them after use.

### Extension Trust

Browser extensions loaded per the capture's `extensions` field run with
elevated browser privileges, can modify rendered content, and may exfiltrate
captured data. Implementations MUST load extensions only from locations
explicitly configured by the operator, and SHOULD verify extension
identity via `manifest_digest` when the extension was loaded from a local
path rather than a trusted registry.

### Archive Content May Be Sensitive

Captured URLs and their contents may include personal data, authentication
artifacts (cookies, tokens in URLs), or otherwise sensitive content. The
envelope artifact in particular may include response headers that expose
authentication state.

Implementations SHOULD:

- Treat the content-addressed store as sensitive storage, with access
  controls appropriate to the captured material.
- Not transmit archive records or artifacts to third parties without
  explicit operator opt-in.
- Consider redaction of known-sensitive headers (e.g., `set-cookie`,
  `authorization`) from envelope artifacts, balanced against the loss of
  audit fidelity.

### Integrity vs Confidentiality

Content addressing provides strong integrity guarantees: a consumer can
verify that the bytes retrieved from the blob store match the markl ID
recorded in the archive record. Content addressing does NOT provide
confidentiality. A blob store visible to multiple parties SHOULD be
treated as such; capture artifacts are not encrypted by this protocol.

### TOCTOU Between Policy and Fetch

The orchestrator reads the policy, substitutes the URL, and invokes the
capturer. Between those steps, the policy file or the URL target may
change. This protocol does not define a coherency window. Orchestrators
that require fresh policy evaluation per capture SHOULD read the policy
immediately before building the batch input; orchestrators that cache
policy across many captures accept the risk of applying stale rules.

### JCS Hash Collisions

The spec artifact's markl ID is derived from JCS-canonicalized bytes. The
security of the resulting identifier against collisions depends entirely
on the chosen hash algorithm (blake2b-256 by default). Implementations
MUST use a cryptographically sound hash algorithm; weak algorithms
undermine the "capture identity" property on which deduplication depends.

## Conformance Testing

Conformance tests for this specification live in `zz-tests_bats/` under
each implementing repository. Tests MUST use binary injection via
`bats-emo`:

    require_bin CAPTURER_BIN chrest
    require_bin WRITER_BIN   madder

This makes the suite portable across implementations: a different
capturer or writer that conforms to the protocol MUST be able to run the
same tests by substituting the injected binary.

### Writer Conformance

Covered requirements for any binary claiming to implement the writer role:

| Requirement | Test File | Description |
|-------------|-----------|-------------|
| § Writer Protocol, MUST emit exactly one JSON object on stdout on success | `writer_stdout.bats` | Pipe known bytes; parse stdout; reject multiple objects or trailing content. |
| § Writer Protocol, MUST include `id` and `size` | `writer_fields.bats` | Verify both fields present with correct types. |
| § Writer Protocol, `size` MUST equal stdin byte count | `writer_size.bats` | Compare reported size to known input length. |
| § Writer Protocol, MUST exit non-zero on failure | `writer_errors.bats` | Close stdin prematurely / send invalid input and assert non-zero exit. |
| § Writer Protocol, MUST stream stdin (no requirement to buffer) | `writer_streaming.bats` | Pipe a large input slowly; verify writer begins reading before EOF. |

### Capturer Conformance

Covered requirements for any binary claiming to implement the capturer
role:

| Requirement | Test File | Description |
|-------------|-----------|-------------|
| § Capturer Protocol, MUST reject input with unknown `schema` | `capturer_schema.bats` | Pass `schema: bogus/v99` and assert non-zero exit. |
| § Capturer Protocol, MUST emit exactly one JSON object on stdout | `capturer_output.bats` | Parse stdout; reject multiple objects. |
| § Capturer Protocol, `captures[]` output length MUST match input length | `capturer_cardinality.bats` | Submit N captures; assert N entries in output. |
| § Capturer Protocol, Writer Invocation count | `capturer_writer_invocations.bats` | Substitute a counting writer binary; assert 3 invocations per split=true capture and 2 per split=false capture. |
| § Capture Spec Artifact, MUST be JCS-canonicalized | `capturer_spec_jcs.bats` | Substitute a writer that captures stdin to a file; verify the captured bytes are JCS canonical form. |
| § Payload Artifact, PDF normalization removes `/CreationDate` | `capturer_pdf_normalize.bats` | Substitute a writer that captures stdin to a file; assert `/CreationDate` absent from the file when split=true. |
| § Flows, writer spawn failure MUST NOT abort the batch | `capturer_resilience.bats` | Inject a writer that fails on the first invocation; verify subsequent captures still processed. |

### Orchestrator Conformance

Covered requirements for any binary claiming to implement the orchestrator
role:

| Requirement | Test File | Description |
|-------------|-----------|-------------|
| § Archive Record Format, MUST write atomically | `orchestrator_atomic.bats` | Monitor target path during write; assert no partial/truncated file visible. |
| § Archive Record Format, `captured_at` MUST match envelope `captured_at` | `orchestrator_captured_at.bats` | Substitute a writer that captures stdin to a file; for each split=true capture, compare archive record `captured_at` to envelope `captured_at`. |
| § URL Templating, MUST expand before invoking capturer | `orchestrator_templating.bats` | Inject a capturer that records its input; assert URL is already concrete. |

Implementations MAY add additional tests, but MUST pass all tests above
that apply to the role they claim to implement.

## Compatibility

This is the initial version of the specification; there are no previous
versions to remain compatible with. No deployed implementations are yet
expected to conform.

### Schema Versioning

Every JSON document defined by this specification carries a `schema` field
of the form `<name>/vN`. Version `v1` is defined in this document:

| Document                    | Schema string                            |
|-----------------------------|------------------------------------------|
| Capturer batch input        | `web-capture-archive/v1`                 |
| Capturer batch output       | `web-capture-archive/v1`                 |
| Capture spec artifact       | `web-capture-archive.spec/v1`            |
| Envelope artifact           | `web-capture-archive.envelope/v1`        |
| Archive record              | `web-capture-archive.record/v1`          |

Implementations MUST reject documents with a `schema` string they do not
understand. Implementations MUST NOT silently treat an unknown schema as
a known one.

### Forward Compatibility

Within a major version (`v1`), new OPTIONAL fields MAY be added to any
document. Existing fields MUST NOT change semantics. Consumers MUST ignore
unknown OPTIONAL fields.

A change that would make a previously valid document invalid — including
renaming fields, tightening types, changing required/optional status, or
altering the meaning of enum values — requires a major version bump
(`v2`). Major version bumps trigger a superseding RFC per the RFC
lifecycle.

### Cross-Role Version Matching

An orchestrator, capturer, and writer participating in the same capture
MUST all use the same major version of this specification. Mixed-version
batches are not supported. Implementations MAY detect mismatch via the
`schema` field and refuse to proceed.

### Envelope Stability

Within `v1`, the envelope artifact schema is stable. The capturer MAY add
new optional fields to `envelope.stripped` as it gains support for
additional normalization formats; consumers MUST NOT rely on the absence
of any `stripped.*` subkey.

## References

### Normative References

- **[RFC 2119][rfc-2119]**: Bradner, S., "Key words for use in RFCs to
  Indicate Requirement Levels", BCP 14, RFC 2119, March 1997.
- **[RFC 8785][rfc-8785]**: Rundgren, A., Jordan, B., and S. Erdtman,
  "JSON Canonicalization Scheme (JCS)", RFC 8785, June 2020.
- **[markl-id][markl-id]**: Markl ID format specification, documented in
  `madder` under `docs/man.7/markl-id.md` and RFC 0002 of the madder
  repository.
- **[RFC 3339][rfc-3339]**: Klyne, G. and C. Newman, "Date and Time on the
  Internet: Timestamps", RFC 3339, July 2002.

### Informative References

- **[nebulous#10][nebulous-10]**: Original motivation for content
  preservation in nebulous.
- **[chrest#21][chrest-21]**: CLI hygiene for capture output (raw
  stdout, streaming). Required for the writer protocol to function
  correctly over pipes.
- **[chrest#22][chrest-22]**: Split envelope/payload capture proposal.
  Source of the three-artifact model in this specification.
- **[chrest#23][chrest-23]**: Spec artifact with config and environment
  fingerprint. Source of the spec artifact schema in this specification.
- **[madder#26][madder-26]**: JSON output mode for writer commands.
  Required for the writer protocol's stdout contract.
- **bats-emo**: BATS test helper providing `require_bin` for binary
  injection in conformance suites.

[rfc-2119]: https://www.rfc-editor.org/rfc/rfc2119
[rfc-3339]: https://www.rfc-editor.org/rfc/rfc3339
[rfc-8785]: https://www.rfc-editor.org/rfc/rfc8785
[markl-id]: https://github.com/amarbel-llc/madder/blob/master/docs/man.7/markl-id.md
[nebulous-10]: https://github.com/amarbel-llc/nebulous/issues/10
[chrest-21]: https://github.com/amarbel-llc/chrest/issues/21
[chrest-22]: https://github.com/amarbel-llc/chrest/issues/22
[chrest-23]: https://github.com/amarbel-llc/chrest/issues/23
[madder-26]: https://github.com/amarbel-llc/madder/issues/26
