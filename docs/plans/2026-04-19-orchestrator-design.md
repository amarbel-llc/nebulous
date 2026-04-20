# Archive Orchestrator Design

**Status**: proposed
**Date**: 2026-04-19
**Related**: RFC 0001 (`docs/rfcs/0001-web-capture-archive-protocol.md`),
ADR 0001 (`docs/decisions/0001-archive-normalization-in-orchestrator.md`),
amarbel-llc/nebulous#10, #13

## Context

RFC 0001 specifies the Web Capture Archive Protocol: an orchestrator, a
capturer (chrest), and a writer (madder) coordinate to produce content-
addressed archives of web pages. The protocol and its substrate packages
(`internal/0/jcs`, `internal/0/writer`, `internal/0/archive`) have already
landed. What is missing is the orchestrator itself — the `nebulous archive`
subcommand that stitches the substrate together into a working end-to-end
pipeline.

This document specifies the design for that orchestrator. Out-of-scope:
orchestrator-side normalization (see ADR 0001), triggers beyond manual CLI,
and any hooks/daemon wiring.

## Goals

- Produce archive records for starred stories (and ad-hoc URLs) by invoking
  the flake-pinned chrest `capture-batch` and the flake-pinned madder
  writer, then writing RFC 0001 archive records to local disk.
- All policies from a user-maintained `nebulous.toml` apply to every
  archive event.
- Non-destructive overwrites: a `previous` markl-id field on each archive
  record forms a linked-list history of prior states.
- Operator-friendly progress (TAP-14 on an interactive terminal), machine-
  friendly summary (JSON on a pipe).

## Non-goals

- Automatic triggers (hooks, daemons). v1 ships a manual subcommand only.
- Policy discovery across multiple files or directories. A single
  `nebulous.toml` contains every policy.
- Orchestrator-side normalization. Normalization stays in the capturer per
  RFC 0001 v1 until ADR 0001 is resolved.
- Parallelism across archive events. Serial only for v1.
- Retrieval, search, or cross-archive diffing — those are separate features.

## Architecture

Single new subcommand. Composition happens in a new package; existing
substrate packages are reused as-is plus one small extension to
`internal/0/archive/`.

```
cmd/nebulous/archive.go
    └── flag parsing, TTY detect, exit codes
        ▼
internal/alfa/orchestrator.Run(ctx, args) → Report
    ├── policy.LoadAll(xdgConfigPath) / policy.ExpandURL
    ├── story.Resolve(id)  ← from existing internal/alfa/newsblur
    ├── capturer.Run(ctx, BatchInput) → BatchOutput
    ├── archive.WriteWithHistory(ctx, path, record, madderStore)
    └── reporter (TAP via bob/tap-dancer, or JSON)
```

### New packages

- `internal/alfa/policy/` — TOML policy parser (using `amarbel-llc/tommy`),
  validator, `text/template` URL expander.
- `internal/alfa/capturer/` — spawns flake-pinned chrest via ldflags-
  injected `Bin`, pipes `BatchInput` JSON to stdin, parses `BatchOutput`
  JSON from stdout. Mirrors `internal/0/writer` in structure.
- `internal/alfa/orchestrator/` — the composition layer. `Run(ctx, args) Report`.
  Holds job sequencing, circuit breaker, progress reporter injection.

### Existing packages extended

- `internal/0/archive/` — add `Record.Previous *string`, `WriteWithHistory`,
  and a small `Writer` interface for the prior-record blob sink. The
  package remains leaf-tier; `internal/0/madder.Store` already satisfies
  the `Writer` interface.

### Tier placement rationale

New packages live at `internal/alfa/` because they import other internal
packages (archive, madder, newsblur). The policy package could move to
`internal/0/` later if it turns out leaf-pure; no external impact.

### Rollback

This is greenfield code. No existing subcommand or file format is replaced.
Rolling back the orchestrator means deleting `cmd/nebulous/archive.go`
plus the three new packages; no migration is needed.

## Decision record

| Topic | Choice |
|---|---|
| Trigger | Manual CLI only (`nebulous archive …`). Hooks/daemon deferred. |
| Policy file | Single `~/.config/nebulous/nebulous.toml` parsed via `amarbel-llc/tommy`. |
| Policy scope | All `[[policy]]` entries in that file apply to every archive event. |
| Writer cmd | Hardcoded flake-pinned madder via ldflags-injected path (mirrors `internal/0/madder.Bin`). |
| Archive root | `$XDG_DATA_HOME/nebulous/archives/`. |
| Subject keying | Two records per event when both `--story` and `--url` supplied: `archives/by-story/<id>/<policy_id>.json` and `archives/by-url/<sha256>/<policy_id>.json`. |
| Re-archive | Overwrite HEAD; prior record serialized to a madder blob and referenced via `record.Previous` markl-id for linked-list history. |
| Partial capture failure | Write the record; per-capture `error` entries survive per RFC 0001. |
| Job failure (capturer spawn, write, etc.) | TAP `not ok`; continue to next job unless circuit trips. |
| Circuit breaker | Bail after 3 **consecutive** job failures. |
| Concurrency | Serial only in v1. |
| Error model | `Run` returns `Report` only. Invariants panic. Future migration to `dewey/errors.Context` per nebulous#13. |
| Output | TTY on stdout ⇒ TAP-14 via `bob/tap-dancer`. Non-TTY ⇒ one JSON object at end of run. |

## Components

### `internal/alfa/policy/`

```go
type TemplateContext struct {
    Story Story
}

type Story struct {
    Hash      string
    Permalink string
    Title     string
}

type Policy struct {
    ID        string
    URL       string   // text/template template
    Isolation string   // fresh | session | shared
    Captures  []Capture
}

type Capture struct {
    Name       string
    Format     string
    Browser    string
    Options    map[string]any
    Split      bool
    Extensions []Extension
    Flags      []string
}

type Extension struct {
    ID      string
    Version string
}

func LoadAll(path string) ([]Policy, error)
func ExpandURL(tmpl string, ctx TemplateContext) (string, error)
```

Uses `text/template` with `Option("missingkey=error")` — typos like
`{{.Story.Prmalink}}` error out at orchestration time rather than silently
substituting empty strings.

### `internal/alfa/capturer/`

```go
var Bin = "chrest" // overridden via ldflags

type BatchInput struct {
    Schema   string                       `json:"schema"`
    Writer   WriterCmd                    `json:"writer"`
    URL      string                       `json:"url"`
    Defaults Defaults                     `json:"defaults,omitempty"`
    Captures []CaptureRequest             `json:"captures"`
}

type BatchOutput struct {
    Schema   string                       `json:"schema"`
    Capturer CapturerInfo                 `json:"capturer"`
    Errors   []ErrorEntry                 `json:"errors"`
    Captures []CaptureResult              `json:"captures"`
}

func Run(ctx context.Context, in BatchInput) (BatchOutput, error)
```

Same error taxonomy as `internal/0/writer`: `*Error{Kind, Msg, Stderr, Status}`
for spawn failure, non-zero exit, bad JSON, bad shape, schema mismatch.
Stdin streams; no full-payload buffering.

### `internal/alfa/orchestrator/`

```go
type Args struct {
    StoryID    string
    URL        string
    PolicyPath string // default: $XDG_CONFIG_HOME/nebulous/nebulous.toml
    ArchiveRoot string // default: $XDG_DATA_HOME/nebulous/archives
}

type Report struct {
    Written   []Job
    Failed    []JobFailure
    BailedOut bool
}

func Run(ctx context.Context, args Args) Report
```

Internal injection interface for testability:

```go
type deps struct {
    LoadPolicies  func(path string) ([]policy.Policy, error)
    ResolveStory  func(id string) (policy.Story, error)
    RunCapturer   func(context.Context, capturer.BatchInput) (capturer.BatchOutput, error)
    WriteArchive  func(context.Context, string, *archive.Record, archive.Writer) error
    TimeNow       func() time.Time
}
```

Production `Run` uses real implementations; tests substitute stubs.

### `internal/0/archive/` (extensions)

```go
type Record struct {
    // existing fields...
    Previous *string `json:"previous,omitempty"` // markl-id of prior record blob
}

type Writer interface {
    Write(ctx context.Context, src io.Reader) (id string, err error)
}

func WriteWithHistory(
    ctx context.Context, path string, r *Record, w Writer,
) error
```

`WriteWithHistory` reads any existing file at `path`, pipes its bytes
through `Writer.Write` to obtain a markl id, sets `r.Previous` to that id,
and then atomically renames a tempfile over `path`. If no prior file
exists, `r.Previous` stays nil.

History traversal (walking the `previous` chain) lives in
`internal/alfa/orchestrator/history.go` — composition territory; needs
both the archive decoder and a read-by-id capability that the small
`archive.Writer` interface does not expose.

### `cmd/nebulous/archive.go`

```go
func archiveMain(ctx context.Context, args ...string) int {
    parsed := parseFlags(args) // exits 3 on bad flags
    report := orchestrator.Run(ctx, parsed.orchArgs)
    emitReport(report, isTTY(os.Stdout))
    return report.ExitCode()
}
```

~50 lines. No business logic.

## Data flow

Example invocation with both selectors present (worst case — produces
two record files per policy):

```
$ nebulous archive --story=6327282:5d1cf5 --url=https://example.com/article
```

1. **Load policies.** `policy.LoadAll(xdgConfig)` → `[]Policy` or validation
   error (exit 3).
2. **Build subjects.** Both flags present, so two subjects emit:
   `story:6327282:5d1cf5` and `url:sha256-<hex16>`. Jobs = subjects × policies.
3. **Resolve story context.** For `story:*` subjects, look up the newsblur
   record and populate `TemplateContext.Story`. For `url:*` subjects, set
   `Story.Permalink = url, Story.Hash = sha256(url), Story.Title = ""`.
4. **Expand URL template** per policy: `policy.ExpandURL(pol.URL, ctx)`.
   Strict mode — missing keys are job-fatal.
5. **Build `capturer.BatchInput`.** Writer cmd is the flake-pinned madder:
   `[madderBin, "--format=json", "write", "--store", "nebulous"]`.
6. **Run capturer.** `capturer.Run(ctx, input)` execs chrest, streams JSON
   in, reads JSON out.
7. **Assemble `archive.Record`.** Copy `Captures` and `Errors` from batch
   output; add `Subject`, `URL`, `PolicyID`, `CapturedAt`.
8. **Write with history.** `archive.WriteWithHistory(ctx, path, record, madderStore)`
   where `path = DefaultPath(archiveRoot, subjectLabel, pol.ID)`.
9. **Report.** Append `Job{PolicyID, Subject, Path}` to `Report.Written`
   on success or `JobFailure{…}` on failure. TAP line emitted per result
   if on tty.

## Error handling

### Severity classes

| Site | Behavior |
|---|---|
| Internal invariant violation | `panic(msg)` |
| Flag parse error | Exit 3 immediately |
| Policy file missing / invalid | Pre-job `Report.Failed` entry; exit 3 |
| Story resolution failure | Affects all jobs for that subject; mark each `not ok` |
| URL template expansion error | `not ok` for that job only |
| Capturer spawn / schema reject / bad-shape | `not ok` for that job; surface `*Error.Kind` |
| Per-capture error inside a good batch | `ok` — record written with `captures[].error` |
| Archive write (fs error) | `not ok` for that job |
| 3 consecutive job failures | TAP `Bail out!`; exit 2 |

### Circuit breaker

Consecutive counter, not cumulative. A single `ok` resets the counter.
Threshold 3 in v1; make configurable via env var later (e.g.
`ARCHIVE_BAIL_AFTER`).

Rationale: systemic breakage (chrest can't launch, writer unreachable,
XDG path unwritable) manifests as a run of failures; isolated one-off
errors don't warrant stopping a multi-hour batch run. Cumulative would
mean a mostly-working run gets killed by an accumulated tail.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | All jobs `ok` |
| 1 | Mixed: some `ok`, some `not ok`; no bail |
| 2 | Circuit breaker tripped |
| 3 | Pre-job failure (bad flags, bad policy) |
| 4 | Internal error (should not happen; panics terminate with runtime-level exit) |

### Output contract

Single output stream, TTY-detected on stdout:

| TTY? | Output |
|---|---|
| yes | TAP-14 via `amarbel-llc/bob/tap-dancer`. Per-job `ok`/`not ok` with policy id + subject label. Failures get YAML block with kind / message / stderr. Circuit trip emits `Bail out! 3 consecutive failures`. |
| no | Single JSON object at end of run: `{"written":[…], "failed":[…], "bailed_out":bool}` |

Stderr is unused by the orchestrator except for runtime panic traces
(which Go emits automatically).

### Go signatures

`orchestrator.Run` returns `Report` only. Component functions
(`policy.LoadAll`, `capturer.Run`, `archive.WriteWithHistory`) return
`(T, error)` in the Go idiom. The orchestrator catches and classifies
those errors into `Report` entries. Callers of the orchestrator don't
touch error values.

### Future evolution

`nebulous#13` plans a migration to `dewey/errors.Context` for lifetime-
scoped cancellation and stack-attributed errors. This design uses
standard `context.Context` but structures signatures compatibly:
returning `Report` (not `(Report, error)`) means swapping
`context.Context → errors.Context` in one edit does not break callers.

## Testing strategy

### Unit tests per package

| Package | Tests |
|---|---|
| `internal/alfa/policy/` | TOML parse roundtrip, validation errors per field, strict template expansion, unknown-key error, nested struct reference (`{{.Story.Permalink}}`). |
| `internal/alfa/capturer/` | Happy path with canned chrest stub; schema reject; batch-level errors; per-capture errors; malformed JSON; non-zero exit; streaming stdin of non-trivial size. |
| `internal/alfa/orchestrator/` | See below. |
| `internal/0/archive/` (new) | `WriteWithHistory` with no prior file; `WriteWithHistory` with prior file (Previous set, blob written to stub Writer); history traversal walks chain backward; malformed prior record surfaces through the helper. |

### Integration-unit tests in `orchestrator/`

Inject stub implementations via the `deps` struct. Every test runs in
`t.TempDir()` with no network, no browser, no real filesystem outside
that dir. Target cases:

1. Single policy + single subject, all captures succeed → 1 written.
2. Two policies × two subjects → 4 written.
3. Capturer returns batch-level error → 1 not-ok, continue.
4. Capturer returns per-capture errors but otherwise succeeds → ok (record written).
5. Three consecutive failures → bail out, `BailedOut=true`, exit 2.
6. Five failures separated by successes → no bail, exit 1.
7. Context canceled mid-run → remaining jobs appear as skipped.
8. `archive.Writer` fails → `not ok`, error surfaced with kind.

### TAP golden-fixture test

In-memory buffer captures stdout; force-tty flag triggers TAP path.
Asserts against a small golden text file under `internal/alfa/orchestrator/testdata/`.
Catches TAP regressions without needing a real tty.

### Bats integration test

New file `zz-tests_bats/orchestrator.bats` exercising the real
`nebulous archive` subcommand end-to-end:

- Fixture `nebulous.toml`, shell writer stub, temp archive root.
- Force non-tty path so output is deterministic JSON, parse with `jq`.
- Cases: happy path (produces expected record files), bad policy
  (exit 3), bail-out (capturer always-fails stub, verify three-strike
  bail and exit 2).

### Existing tests stay green

`zz-tests_bats/archive_capture.bats` — chrest-direct test, unchanged.
`internal/0/jcs`, `internal/0/writer`, `internal/0/archive` (existing
tests) — unchanged, still run.

### What's deliberately NOT tested at orchestrator level

- Real chrest (covered by `archive_capture.bats`).
- Real madder CLI (covered in madder's own test suite).
- Real newsblur cache (covered in `internal/alfa/newsblur/`).

Each test layer has a single responsibility; overlap is minimal.

## Open follow-ups (out of scope for v1)

- Triggers: hook into star events, `--watch` daemon, scheduled invocations.
- Orchestrator-side normalization if ADR 0001 is accepted.
- Parallelism with per-subject locking.
- TAP tunability: `ARCHIVE_BAIL_AFTER`, `ARCHIVE_TAP_VERBOSITY`.
- Richer TemplateContext (`.Env`, `.Now`, custom template funcs).
- Split=true coverage in `archive_capture.bats` once nebulous consumes
  envelope artifacts.

## References

- RFC 0001 — `docs/rfcs/0001-web-capture-archive-protocol.md`
- ADR 0001 — `docs/decisions/0001-archive-normalization-in-orchestrator.md`
- Issue: amarbel-llc/nebulous#10 (content preservation)
- Issue: amarbel-llc/nebulous#13 (errors.Context migration)
- amarbel-llc/bob (`tap-dancer`, `tommy`)
- amarbel-llc/purse-first/libs/dewey/bravo/errors (future target for #13)
