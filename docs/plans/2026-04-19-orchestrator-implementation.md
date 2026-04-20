# Archive Orchestrator Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Ship the `nebulous archive` subcommand that runs a policy-driven web-archive capture via the flake-pinned chrest + madder, writing RFC 0001 archive records with linked-list history to `$XDG_DATA_HOME/nebulous/archives/`.

**Architecture:** New subcommand in `cmd/nebulous/` delegates to `internal/alfa/orchestrator/` which composes three new packages (`internal/alfa/policy/`, `internal/alfa/capturer/`, plus an extension to `internal/0/archive/`). Dependencies are injected for testability; production uses real implementations, tests substitute stubs.

**Tech Stack:**
- Go 1.26 (devShell)
- TOML via `amarbel-llc/tommy` (to be added as a Go dep)
- TAP output via `amarbel-llc/bob/tap-dancer` (to be added as a Go dep)
- Writer protocol client already exists at `internal/0/writer/`
- JCS canonicalizer already exists at `internal/0/jcs/` (not used here, but present)
- Archive record scaffold already exists at `internal/0/archive/`

**Design doc:** `docs/plans/2026-04-19-orchestrator-design.md` (committed in `4f96db3`).

**Rollback:** Greenfield addition. To revert: `git rm cmd/nebulous/archive.go internal/alfa/{orchestrator,capturer,policy} zz-tests_bats/orchestrator.bats` and revert the small `internal/0/archive/` extension. No existing code path relies on these files.

**Progress convention:** every task ends with a commit. Conventional commit prefix `feat:` for new packages / new tests, `test:` for test-only additions, `chore:` for dep bumps, `docs:` for doc updates.

---

## Prerequisites (verify before starting Task 1)

Verify that these already-landed pieces are present at current HEAD:

1. `internal/0/archive/archive.go` — has `Record`, `ArtifactRef`, `Capture`, `Error`, `Write`, `Read`, `DefaultPath`, `FormatTimestamp`, `ParseTimestamp`.
2. `internal/0/writer/writer.go` — has `Write`, `Result`, `Error`.
3. `internal/0/madder/store.go` — has `Store` with `Write(src io.Reader) (string, error)`. Used as the `archive.Writer` implementation in Task 1.
4. `flake.nix` — has `chrest` pinned as an input and exposed as `packages.chrest`. `chrestPkg` in scope.
5. Run `just test-go` — expect all tests green.

If any of these are missing or tests fail, STOP and escalate to the user. The plan assumes this substrate.

---

## Task 1: Extend `internal/0/archive/` with Previous field + WriteWithHistory

**Promotion criteria:** N/A. Additive. The existing `Write` stays and is still valid for callers that don't want history.

**Files:**
- Modify: `internal/0/archive/archive.go` — add `Previous` field to `Record`, add `Writer` interface, add `WriteWithHistory`.
- Modify: `internal/0/archive/archive_test.go` — add tests for the new surface.

### Step 1.1: Write failing test for WriteWithHistory (no prior file)

Add to `internal/0/archive/archive_test.go`:

```go
// stubWriter captures input and returns a deterministic markl id.
type stubWriter struct {
	captured [][]byte
}

func (w *stubWriter) Write(ctx context.Context, src io.Reader) (string, error) {
	buf, err := io.ReadAll(src)
	if err != nil {
		return "", err
	}
	w.captured = append(w.captured, buf)
	return "blake2b256-prior-" + strconv.Itoa(len(w.captured)), nil
}

func TestWriteWithHistory_noPriorFileLeavesPreviousNil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.json")
	sw := &stubWriter{}

	if err := WriteWithHistory(context.Background(), path, sampleRecord(), sw); err != nil {
		t.Fatalf("WriteWithHistory: %v", err)
	}
	if len(sw.captured) != 0 {
		t.Errorf("writer should not be called when no prior file: got %d captures", len(sw.captured))
	}

	r, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if r.Previous != nil {
		t.Errorf("Previous should be nil, got %q", *r.Previous)
	}
}
```

Add imports: `context`, `io`, `strconv`.

### Step 1.2: Run the test — expect failure

```bash
just test-go internal/0/archive/
```

Expected: build error — `Writer`, `WriteWithHistory`, `Previous` undefined.

### Step 1.3: Implement minimal surface

Add to `internal/0/archive/archive.go`:

```go
// Writer abstracts the shell-out path to a content-addressed blob
// store. internal/0/madder.Store satisfies this naturally. The
// package intentionally does not import madder.
type Writer interface {
	Write(ctx context.Context, src io.Reader) (id string, err error)
}
```

Extend the Record struct:

```go
type Record struct {
	// ... existing fields ...

	// Previous is the markl ID of the prior archive record for this
	// (subject, policy) tuple, as written to a content-addressed blob
	// store by WriteWithHistory. Nil when no prior record exists.
	Previous *string `json:"previous,omitempty"`
}
```

Add the new write function at the bottom of the file:

```go
// WriteWithHistory is Write plus linked-list history. If path already
// exists, the current contents are piped through w to get a markl ID,
// r.Previous is set to that ID, and r is then written atomically over
// path. If path does not exist, r.Previous is left unchanged (nil in
// typical callers).
//
// Atomicity is the same as Write: tempfile in the same directory,
// fsync, rename. On any error, the filesystem is left unchanged.
func WriteWithHistory(ctx context.Context, path string, r *Record, w Writer) error {
	if w == nil {
		return errors.New("archive: writer is required")
	}

	priorBytes, err := os.ReadFile(path)
	switch {
	case err == nil:
		id, werr := w.Write(ctx, bytes.NewReader(priorBytes))
		if werr != nil {
			return fmt.Errorf("archive: write prior to history store: %w", werr)
		}
		r.Previous = &id
	case errors.Is(err, os.ErrNotExist):
		// no prior; leave Previous as-is (typically nil)
	default:
		return fmt.Errorf("archive: read prior %s: %w", path, err)
	}

	return Write(path, r)
}
```

Already has `bytes`, `errors`, `fmt`, `os` imports; add `context`, `io` if missing (io is for the interface).

### Step 1.4: Run tests — expect pass

```bash
just test-go internal/0/archive/
```

Expected: all existing tests pass + new test passes.

### Step 1.5: Write failing test for WriteWithHistory with prior file

Add to same test file:

```go
func TestWriteWithHistory_priorFileWritesToStoreAndSetsPrevious(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.json")
	sw := &stubWriter{}

	// First write has no prior.
	first := sampleRecord()
	if err := WriteWithHistory(context.Background(), path, first, sw); err != nil {
		t.Fatalf("first WriteWithHistory: %v", err)
	}

	// Second write should pipe the first's bytes through sw.
	second := sampleRecord()
	second.Subject = "different-subject-2" // force different content
	if err := WriteWithHistory(context.Background(), path, second, sw); err != nil {
		t.Fatalf("second WriteWithHistory: %v", err)
	}

	if len(sw.captured) != 1 {
		t.Fatalf("expected exactly 1 prior write, got %d", len(sw.captured))
	}
	if !bytes.Contains(sw.captured[0], []byte("6327282:5d1cf5")) {
		t.Errorf("prior bytes should contain the first record's subject, got %s", sw.captured[0])
	}

	reloaded, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if reloaded.Previous == nil || *reloaded.Previous != "blake2b256-prior-1" {
		t.Errorf("Previous: got %v, want blake2b256-prior-1", reloaded.Previous)
	}
}
```

### Step 1.6: Run tests — expect pass

```bash
just test-go internal/0/archive/
```

Expected: pass (the implementation from step 1.3 already handles this).

### Step 1.7: Write failing test for writer-fails case

```go
type failingWriter struct{ err error }

func (w *failingWriter) Write(ctx context.Context, src io.Reader) (string, error) {
	_, _ = io.Copy(io.Discard, src)
	return "", w.err
}

func TestWriteWithHistory_writerFailsLeavesPathUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.json")

	// Establish a prior record.
	orig := sampleRecord()
	if err := Write(path, orig); err != nil {
		t.Fatal(err)
	}
	origBytes, _ := os.ReadFile(path)

	// Second write should fail before mutating the file.
	fw := &failingWriter{err: errors.New("simulated store outage")}
	second := sampleRecord()
	second.Subject = "should-not-be-written"
	err := WriteWithHistory(context.Background(), path, second, fw)
	if err == nil {
		t.Fatal("expected error from failing writer, got nil")
	}
	if !strings.Contains(err.Error(), "simulated store outage") {
		t.Errorf("error should wrap writer err, got %v", err)
	}

	after, _ := os.ReadFile(path)
	if !bytes.Equal(origBytes, after) {
		t.Errorf("file should be unchanged on writer failure")
	}
}
```

### Step 1.8: Run tests — expect pass

```bash
just test-go internal/0/archive/
```

Expected: pass (implementation returns early before `Write` is called).

### Step 1.9: Run vet + full Go suite

```bash
nix develop -c go vet ./...
just test-go
```

Expected: clean, all pass.

### Step 1.10: Commit

```bash
just grit-add internal/0/archive/archive.go internal/0/archive/archive_test.go
# or in claude: use grit_add with those paths
```

Commit message:

```
archive: add Previous field + WriteWithHistory for linked-list history

Non-destructive overwrite for archive records per the orchestrator
design doc (docs/plans/2026-04-19-orchestrator-design.md). The prior
record contents are piped through an archive.Writer (satisfied by
internal/0/madder.Store) to obtain a markl ID; the new record's
Previous field holds that ID, forming a linked list navigable via
`madder cat <id>`.

Atomicity preserved: any error in the writer path returns before
mutating the target file.

Unit tests added: no-prior-file, prior-file-writes-to-store,
writer-fails-leaves-path-unchanged.

:clown: [Clown](https://github.com/amarbel-llc/clown)
```

---

## Task 2: Add `amarbel-llc/tommy` + `amarbel-llc/bob/tap-dancer` Go dependencies

**Promotion criteria:** N/A. Additive.

**Files:**
- Modify: `go.mod`, `go.sum`
- Regenerate: `gomod2nix.toml`

### Step 2.1: Add tommy

```bash
nix develop -c go get github.com/amarbel-llc/tommy@latest
```

Verify it parses:

```bash
nix develop -c go mod tidy
```

### Step 2.2: Add tap-dancer

Tap-dancer's Go import path should be `github.com/amarbel-llc/bob/pkg/tapdancer` or similar — check `go doc` on the published module first:

```bash
nix develop -c go list -m -json github.com/amarbel-llc/bob 2>/dev/null || echo "run 'go get' first"
nix develop -c go get github.com/amarbel-llc/bob@latest
nix develop -c go mod tidy
```

Verify import paths available under `go/src/tapdancer` or equivalent in the module.

**If neither library exists or has no Go surface you can use**, STOP and escalate. The design assumes both are importable Go packages.

### Step 2.3: Regenerate gomod2nix.toml

```bash
nix develop -c gomod2nix
```

Verify `gomod2nix.toml` now contains entries for `github.com/amarbel-llc/tommy` and `github.com/amarbel-llc/bob`.

### Step 2.4: Verify build + tests

```bash
just build
just test-go
```

Expected: everything still green.

### Step 2.5: Commit

```
chore: add tommy + tap-dancer as Go deps

tommy (github.com/amarbel-llc/tommy) — TOML parser for the
nebulous.toml policy file, per the orchestrator design.

tap-dancer (github.com/amarbel-llc/bob/pkg/tapdancer or equivalent)
— TAP-14 stream writer for orchestrator progress output on an
interactive terminal.

gomod2nix.toml regenerated to track the new vendor set.

:clown: [Clown](https://github.com/amarbel-llc/clown)
```

---

## Task 3: `internal/alfa/policy/` — types, LoadAll, ExpandURL

**Promotion criteria:** N/A.

**Files:**
- Create: `internal/alfa/policy/policy.go`
- Create: `internal/alfa/policy/policy_test.go`
- Create: `internal/alfa/policy/testdata/valid.toml`
- Create: `internal/alfa/policy/testdata/bad-missing-id.toml`
- Create: `internal/alfa/policy/testdata/bad-unknown-format.toml`

### Step 3.1: Write fixtures

`internal/alfa/policy/testdata/valid.toml`:

```toml
[[policy]]
id        = "starred-default"
url       = "{{.Story.Permalink}}"
isolation = "fresh"

[[policy.capture]]
name    = "text"
format  = "text"
browser = "firefox"

[[policy.capture]]
name    = "pdf"
format  = "pdf"
browser = "firefox"
split   = true
options = { background = true }

[[policy]]
id  = "screenshot-only"
url = "{{.Story.Permalink}}"

[[policy.capture]]
name    = "screenshot"
format  = "screenshot"
browser = "firefox"
options = { format = "png", "full-page" = true }
```

`internal/alfa/policy/testdata/bad-missing-id.toml`:

```toml
[[policy]]
url = "{{.Story.Permalink}}"

[[policy.capture]]
name   = "text"
format = "text"
```

`internal/alfa/policy/testdata/bad-unknown-format.toml`:

```toml
[[policy]]
id  = "bad"
url = "{{.Story.Permalink}}"

[[policy.capture]]
name   = "weird"
format = "pictgif"
```

### Step 3.2: Write failing test for LoadAll happy path

`internal/alfa/policy/policy_test.go`:

```go
package policy

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAll_validFile(t *testing.T) {
	policies, err := LoadAll(filepath.Join("testdata", "valid.toml"))
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(policies))
	}
	if policies[0].ID != "starred-default" {
		t.Errorf("policies[0].ID: got %q, want starred-default", policies[0].ID)
	}
	if len(policies[0].Captures) != 2 {
		t.Errorf("policies[0].Captures: got %d, want 2", len(policies[0].Captures))
	}
	if policies[0].Isolation != "fresh" {
		t.Errorf("policies[0].Isolation: got %q, want fresh", policies[0].Isolation)
	}
	if policies[1].ID != "screenshot-only" {
		t.Errorf("policies[1].ID: got %q", policies[1].ID)
	}
	// Default isolation should still be "fresh" even though it's omitted.
	if policies[1].Isolation != "fresh" {
		t.Errorf("policies[1].Isolation default: got %q, want fresh", policies[1].Isolation)
	}
}
```

### Step 3.3: Run test — expect failure

```bash
just test-go internal/alfa/policy/
```

Expected: build error — package doesn't exist.

### Step 3.4: Implement `policy.go` — types and LoadAll

```go
// Package policy parses and validates nebulous.toml.
//
// The policy file is a single TOML document containing an array of
// [[policy]] entries. Every policy has an id, a URL template
// (text/template), a default browser isolation mode, and an ordered
// list of captures. See RFC 0001 § Policy Format for the semantics.
package policy

import (
	"fmt"
	"os"

	tommy "github.com/amarbel-llc/tommy" // adjust to real import path from `go doc`
)

type Policy struct {
	ID        string    `toml:"id"`
	URL       string    `toml:"url"`
	Isolation string    `toml:"isolation"`
	Captures  []Capture `toml:"capture"`
}

type Capture struct {
	Name       string         `toml:"name"`
	Format     string         `toml:"format"`
	Browser    string         `toml:"browser"`
	Options    map[string]any `toml:"options"`
	Split      bool           `toml:"split"`
	Extensions []Extension    `toml:"extensions"`
	Flags      []string       `toml:"flags"`
}

type Extension struct {
	ID      string `toml:"id"`
	Version string `toml:"version"`
}

type TemplateContext struct {
	Story Story
}

type Story struct {
	Hash      string
	Permalink string
	Title     string
}

type fileShape struct {
	Policies []Policy `toml:"policy"`
}

func LoadAll(path string) ([]Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("policy: read %s: %w", path, err)
	}
	var shape fileShape
	if err := tommy.Unmarshal(data, &shape); err != nil {
		return nil, fmt.Errorf("policy: parse %s: %w", path, err)
	}

	for i := range shape.Policies {
		applyDefaults(&shape.Policies[i])
	}
	if err := validate(shape.Policies); err != nil {
		return nil, fmt.Errorf("policy: validate %s: %w", path, err)
	}
	return shape.Policies, nil
}

func applyDefaults(p *Policy) {
	if p.Isolation == "" {
		p.Isolation = "fresh"
	}
	for i := range p.Captures {
		if p.Captures[i].Browser == "" {
			p.Captures[i].Browser = "firefox"
		}
	}
}

// Stub validate — expanded in later steps.
func validate(pols []Policy) error {
	for i, p := range pols {
		if p.ID == "" {
			return fmt.Errorf("policies[%d].id is required", i)
		}
	}
	return nil
}
```

Adjust the import path once the tommy module's Go surface is known (step 2.1 should have surfaced it).

### Step 3.5: Run test — expect pass

```bash
just test-go internal/alfa/policy/
```

Expected: pass.

### Step 3.6: Write failing test for validation — missing id

```go
func TestLoadAll_rejectsMissingID(t *testing.T) {
	_, err := LoadAll(filepath.Join("testdata", "bad-missing-id.toml"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Errorf("error should mention `id is required`, got %v", err)
	}
}
```

### Step 3.7: Run test — expect pass

Already implemented in step 3.4's validate stub.

### Step 3.8: Write failing test for unknown-format validation

```go
func TestLoadAll_rejectsUnknownFormat(t *testing.T) {
	_, err := LoadAll(filepath.Join("testdata", "bad-unknown-format.toml"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "format") {
		t.Errorf("error should mention format, got %v", err)
	}
}
```

### Step 3.9: Implement the format validation

Extend `validate`:

```go
var allowedFormats = map[string]bool{
	"text": true, "pdf": true, "screenshot": true, "mhtml": true, "a11y": true,
}

var allowedBrowsers = map[string]bool{"firefox": true, "chrome": true}
var allowedIsolations = map[string]bool{"fresh": true, "session": true, "shared": true}

func validate(pols []Policy) error {
	seen := make(map[string]bool, len(pols))
	for i, p := range pols {
		if p.ID == "" {
			return fmt.Errorf("policies[%d].id is required", i)
		}
		if seen[p.ID] {
			return fmt.Errorf("policies[%d].id %q is duplicated", i, p.ID)
		}
		seen[p.ID] = true

		if p.URL == "" {
			return fmt.Errorf("policies[%d].url is required", i)
		}
		if !allowedIsolations[p.Isolation] {
			return fmt.Errorf("policies[%d].isolation %q not in {fresh, session, shared}", i, p.Isolation)
		}
		if len(p.Captures) == 0 {
			return fmt.Errorf("policies[%d] requires at least one capture", i)
		}
		captureNames := make(map[string]bool, len(p.Captures))
		for j, c := range p.Captures {
			if c.Name == "" {
				return fmt.Errorf("policies[%d].capture[%d].name is required", i, j)
			}
			if captureNames[c.Name] {
				return fmt.Errorf("policies[%d].capture[%d].name %q is duplicated", i, j, c.Name)
			}
			captureNames[c.Name] = true
			if !allowedFormats[c.Format] {
				return fmt.Errorf("policies[%d].capture[%d].format %q not in {text, pdf, screenshot, mhtml, a11y}", i, j, c.Format)
			}
			if !allowedBrowsers[c.Browser] {
				return fmt.Errorf("policies[%d].capture[%d].browser %q not in {firefox, chrome}", i, j, c.Browser)
			}
		}
	}
	return nil
}
```

### Step 3.10: Run tests — expect all pass

```bash
just test-go internal/alfa/policy/
```

### Step 3.11: Write failing test for ExpandURL happy path

```go
func TestExpandURL_happyPath(t *testing.T) {
	ctx := TemplateContext{Story: Story{
		Permalink: "https://example.com/article",
		Hash:      "deadbeef",
		Title:     "An Article",
	}}
	got, err := ExpandURL("{{.Story.Permalink}}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/article" {
		t.Errorf("got %q", got)
	}
}
```

### Step 3.12: Implement ExpandURL

```go
import (
	"bytes"
	"text/template"
)

func ExpandURL(tmpl string, ctx TemplateContext) (string, error) {
	t, err := template.New("url").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("policy: parse url template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("policy: expand url template: %w", err)
	}
	return buf.String(), nil
}
```

### Step 3.13: Write failing test for ExpandURL with typo (strict mode)

```go
func TestExpandURL_unknownFieldErrors(t *testing.T) {
	ctx := TemplateContext{Story: Story{Permalink: "x"}}
	_, err := ExpandURL("{{.Story.Prmalink}}", ctx)
	if err == nil {
		t.Fatal("expected error on typo'd field, got nil")
	}
}
```

### Step 3.14: Run tests — expect pass

```bash
just test-go internal/alfa/policy/
```

### Step 3.15: Run full Go suite + vet

```bash
nix develop -c go vet ./...
just test-go
```

### Step 3.16: Commit

```
feat: add internal/alfa/policy package

TOML parser + validator + text/template URL expander for the
nebulous.toml policy file. Uses amarbel-llc/tommy. Strict-mode
template execution — typos like {{.Story.Prmalink}} error at
orchestration time instead of silently substituting empty strings.

Validation covers: required id/url/isolation, enum checks for
format/browser/isolation, at-least-one-capture, unique capture
names within a policy, unique policy ids within the file.

Test fixtures under testdata/: valid two-policy file,
bad-missing-id.toml, bad-unknown-format.toml.

:clown: [Clown](https://github.com/amarbel-llc/clown)
```

---

## Task 4: `internal/alfa/capturer/` — types + Run

**Promotion criteria:** N/A.

**Files:**
- Create: `internal/alfa/capturer/capturer.go`
- Create: `internal/alfa/capturer/types.go`
- Create: `internal/alfa/capturer/capturer_test.go`

### Step 4.1: Define types

`internal/alfa/capturer/types.go`:

```go
package capturer

import "encoding/json"

const Schema = "web-capture-archive/v1"

type WriterCmd struct {
	Cmd []string `json:"cmd"`
}

type Defaults struct {
	Browser   string `json:"browser,omitempty"`
	Isolation string `json:"isolation,omitempty"`
	Split     *bool  `json:"split,omitempty"`
}

type CaptureRequest struct {
	Name       string            `json:"name"`
	Format     string            `json:"format"`
	Browser    string            `json:"browser,omitempty"`
	Options    map[string]any    `json:"options,omitempty"`
	Split      *bool             `json:"split,omitempty"`
	Isolation  string            `json:"isolation,omitempty"`
	Extensions []ExtensionRef    `json:"extensions,omitempty"`
	Flags      []string          `json:"flags,omitempty"`
}

type ExtensionRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type BatchInput struct {
	Schema   string           `json:"schema"`
	Writer   WriterCmd        `json:"writer"`
	URL      string           `json:"url"`
	Defaults Defaults         `json:"defaults,omitempty"`
	Captures []CaptureRequest `json:"captures"`
}

type ArtifactRef struct {
	ID         string `json:"id"`
	Size       int64  `json:"size"`
	MediaType  string `json:"media_type"`
	Normalized *bool  `json:"normalized,omitempty"`
}

type CaptureResult struct {
	Name     string       `json:"name"`
	Spec     *ArtifactRef `json:"spec,omitempty"`
	Payload  *ArtifactRef `json:"payload,omitempty"`
	Envelope *ArtifactRef `json:"envelope,omitempty"`
	Error    *ErrorEntry  `json:"error,omitempty"`
}

type ErrorEntry struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type CapturerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type BatchOutput struct {
	Schema   string          `json:"schema"`
	Capturer CapturerInfo    `json:"capturer"`
	Errors   []ErrorEntry    `json:"errors"`
	Captures []CaptureResult `json:"captures"`
}

// Compile-time sanity: these should marshal cleanly.
var _ = json.Marshal
```

### Step 4.2: Write happy-path test

`internal/alfa/capturer/capturer_test.go`:

```go
package capturer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeStub(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "chrest-stub.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

const goodStub = `#!/usr/bin/env bash
set -euo pipefail
# Read stdin and ignore; emit canned output.
cat >/dev/null
cat <<'JSON'
{
  "schema": "web-capture-archive/v1",
  "capturer": {"name": "chrest-stub", "version": "0.0.1"},
  "errors": [],
  "captures": [
    {
      "name": "text",
      "spec": {"id": "blake2b256-s", "size": 10, "media_type": "application/vnd.web-capture-archive.spec+json"},
      "payload": {"id": "blake2b256-p", "size": 20, "media_type": "text/plain; charset=utf-8"}
    }
  ]
}
JSON
`

func TestRun_happyPath(t *testing.T) {
	Bin = writeStub(t, goodStub)
	t.Cleanup(func() { Bin = "chrest" })

	in := BatchInput{
		Schema: Schema,
		Writer: WriterCmd{Cmd: []string{"/bin/true"}},
		URL:    "https://example.com/",
		Captures: []CaptureRequest{
			{Name: "text", Format: "text"},
		},
	}

	out, err := Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Capturer.Name != "chrest-stub" {
		t.Errorf("capturer.name: got %q", out.Capturer.Name)
	}
	if len(out.Captures) != 1 {
		t.Errorf("captures length: got %d", len(out.Captures))
	}
}
```

### Step 4.3: Run test — expect failure

```bash
just test-go internal/alfa/capturer/
```

Expected: `Run` undefined.

### Step 4.4: Implement Run

`internal/alfa/capturer/capturer.go`:

```go
// Package capturer spawns the RFC 0001 capturer (chrest) and parses
// its batch output. Mirrors internal/0/writer's shape but at the
// capturer layer: stdin is JSON (not bytes), stdout is JSON, errors
// carry the child's stderr verbatim.
package capturer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Bin is the path to the chrest binary. Overridden at link time via
// ldflags (see flake.nix) for reproducible builds; falls back to
// $PATH lookup in dev builds.
var Bin = "chrest"

type Error struct {
	Kind   string
	Msg    string
	Stderr string
	Status int
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("capturer: %s: exit=%d: %s", e.Kind, e.Status, e.Msg)
	}
	return fmt.Sprintf("capturer: %s: %s", e.Kind, e.Msg)
}

// Run executes `chrest capture-batch`, pipes in as JSON on stdin,
// parses one JSON object of stdout.
func Run(ctx context.Context, in BatchInput) (BatchOutput, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return BatchOutput{}, fmt.Errorf("capturer: marshal input: %w", err)
	}

	cmd := exec.CommandContext(ctx, Bin, "capture-batch")
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return BatchOutput{}, &Error{
			Kind:   "nonzero-exit",
			Msg:    err.Error(),
			Stderr: stderr.String(),
			Status: exitStatus(err),
		}
	}

	trimmed := bytes.TrimSpace(stdout.Bytes())
	if len(trimmed) == 0 {
		return BatchOutput{}, &Error{
			Kind:   "empty-stdout",
			Msg:    "capturer exited 0 but produced no stdout",
			Stderr: stderr.String(),
		}
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var out BatchOutput
	if err := dec.Decode(&out); err != nil {
		return BatchOutput{}, &Error{
			Kind:   "bad-json",
			Msg:    err.Error(),
			Stderr: stderr.String(),
		}
	}
	if dec.More() {
		return BatchOutput{}, &Error{
			Kind:   "trailing-data",
			Msg:    "capturer emitted more than one JSON object",
			Stderr: stderr.String(),
		}
	}

	if out.Schema != Schema {
		return BatchOutput{}, &Error{
			Kind:   "bad-shape",
			Msg:    fmt.Sprintf("schema must be %q, got %q", Schema, out.Schema),
			Stderr: stderr.String(),
		}
	}
	return out, nil
}

func exitStatus(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 0
}

// Silences unused imports if the file shrinks; remove if lints trigger.
var _ = strings.TrimSpace
```

### Step 4.5: Run test — expect pass

```bash
just test-go internal/alfa/capturer/
```

### Step 4.6: Write failing test for non-zero exit

```go
const failingStub = `#!/usr/bin/env bash
echo "something broke" >&2
exit 7
`

func TestRun_nonzeroExit(t *testing.T) {
	Bin = writeStub(t, failingStub)
	t.Cleanup(func() { Bin = "chrest" })

	_, err := Run(context.Background(), BatchInput{
		Schema:   Schema,
		Writer:   WriterCmd{Cmd: []string{"/bin/true"}},
		URL:      "x",
		Captures: []CaptureRequest{{Name: "a", Format: "text"}},
	})
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if cerr.Kind != "nonzero-exit" || cerr.Status != 7 {
		t.Errorf("got %+v", cerr)
	}
	if !strings.Contains(cerr.Stderr, "something broke") {
		t.Errorf("stderr should be captured, got %q", cerr.Stderr)
	}
}
```

### Step 4.7: Write failing test for bad-schema output

```go
const badSchemaStub = `#!/usr/bin/env bash
cat >/dev/null
cat <<'JSON'
{"schema": "other/v1", "capturer": {"name":"x","version":"0"}, "errors": [], "captures": []}
JSON
`

func TestRun_badShape(t *testing.T) {
	Bin = writeStub(t, badSchemaStub)
	t.Cleanup(func() { Bin = "chrest" })

	_, err := Run(context.Background(), BatchInput{
		Schema:   Schema,
		Writer:   WriterCmd{Cmd: []string{"/bin/true"}},
		URL:      "x",
		Captures: []CaptureRequest{{Name: "a", Format: "text"}},
	})
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if cerr.Kind != "bad-shape" {
		t.Errorf("got kind %q", cerr.Kind)
	}
}
```

### Step 4.8: Run all tests — expect pass

```bash
just test-go internal/alfa/capturer/
```

### Step 4.9: Additional edge-case tests

Add: empty stdout stub, malformed JSON stub, trailing data stub. Each ~10 lines.

### Step 4.10: Run vet + full suite + commit

```bash
nix develop -c go vet ./...
just test-go
```

Commit:

```
feat: add internal/alfa/capturer package

Execs chrest capture-batch, pipes BatchInput JSON to stdin, parses
one BatchOutput JSON from stdout. Mirrors internal/0/writer's shape
but at the capturer layer. Typed error kinds: nonzero-exit,
empty-stdout, bad-json, trailing-data, bad-shape.

Bin is overridable via ldflags for the Nix-built path; falls back
to $PATH lookup in dev.

Unit tests with shell stubs covering happy path, non-zero exit,
bad schema, and each error kind.

:clown: [Clown](https://github.com/amarbel-llc/clown)
```

---

## Task 5: `internal/alfa/orchestrator/` — skeleton + happy path

**Promotion criteria:** N/A.

**Files:**
- Create: `internal/alfa/orchestrator/orchestrator.go`
- Create: `internal/alfa/orchestrator/deps.go`
- Create: `internal/alfa/orchestrator/orchestrator_test.go`

### Step 5.1: Define types + deps struct

`internal/alfa/orchestrator/deps.go`:

```go
package orchestrator

import (
	"context"
	"io"
	"time"

	"github.com/friedenberg/nebulous/internal/0/archive"
	"github.com/friedenberg/nebulous/internal/alfa/capturer"
	"github.com/friedenberg/nebulous/internal/alfa/policy"
)

type deps struct {
	LoadPolicies func(path string) ([]policy.Policy, error)
	ResolveStory func(id string) (policy.Story, error)
	RunCapturer  func(context.Context, capturer.BatchInput) (capturer.BatchOutput, error)
	WriteArchive func(context.Context, string, *archive.Record, archive.Writer) error
	TimeNow      func() time.Time

	// HistoryStore satisfies archive.Writer for WriteWithHistory.
	HistoryStore archive.Writer
}

type nopHistory struct{}

func (nopHistory) Write(ctx context.Context, src io.Reader) (string, error) {
	_, _ = io.Copy(io.Discard, src)
	return "blake2b256-nop-history", nil
}
```

### Step 5.2: Define public types

`internal/alfa/orchestrator/orchestrator.go`:

```go
// Package orchestrator composes the RFC 0001 substrate into an
// end-to-end archive pipeline. See
// docs/plans/2026-04-19-orchestrator-design.md for design context.
package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/friedenberg/nebulous/internal/0/archive"
	"github.com/friedenberg/nebulous/internal/alfa/capturer"
	"github.com/friedenberg/nebulous/internal/alfa/policy"
)

type Args struct {
	StoryID     string
	URL         string
	PolicyPath  string
	ArchiveRoot string
}

type Job struct {
	PolicyID string
	Subject  string
	Path     string
}

type JobFailure struct {
	PolicyID string
	Subject  string
	Kind     string
	Message  string
}

type Report struct {
	Written   []Job
	Failed    []JobFailure
	BailedOut bool
}

func (r Report) ExitCode() int {
	if r.BailedOut {
		return 2
	}
	if len(r.Failed) > 0 {
		return 1
	}
	return 0
}

// Run is the composition entry point. Invariants panic.
func Run(ctx context.Context, args Args) Report {
	return run(ctx, args, defaultDeps())
}

func run(ctx context.Context, args Args, d deps) Report {
	policies, err := d.LoadPolicies(args.PolicyPath)
	if err != nil {
		return Report{Failed: []JobFailure{{Kind: "policy-load-failed", Message: err.Error()}}}
	}

	subjects := buildSubjects(args, d)

	var report Report
	consecutive := 0

	for _, subj := range subjects {
		for _, pol := range policies {
			if subj.err != "" {
				report.Failed = append(report.Failed, JobFailure{
					PolicyID: pol.ID, Subject: subj.label,
					Kind: "story-resolve-failed", Message: subj.err,
				})
				consecutive++
				if consecutive >= 3 {
					report.BailedOut = true
					return report
				}
				continue
			}
			ok := runOneJob(ctx, &report, d, args, subj, pol)
			if ok {
				consecutive = 0
			} else {
				consecutive++
				if consecutive >= 3 {
					report.BailedOut = true
					return report
				}
			}
		}
	}
	return &report == nil // placeholder to satisfy "return report"
	// return report
}
```

*Note: the above has an intentional placeholder at the end to flag the final return; step 5.3's test will surface it and step 5.4 fixes.*

### Step 5.3: Write failing test — happy path with all stubs

```go
package orchestrator

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/friedenberg/nebulous/internal/0/archive"
	"github.com/friedenberg/nebulous/internal/alfa/capturer"
	"github.com/friedenberg/nebulous/internal/alfa/policy"
)

func fixedTime() time.Time {
	return time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
}

func stubDeps(tmpDir string) deps {
	return deps{
		LoadPolicies: func(string) ([]policy.Policy, error) {
			return []policy.Policy{{
				ID: "p1", URL: "{{.Story.Permalink}}", Isolation: "fresh",
				Captures: []policy.Capture{{Name: "text", Format: "text", Browser: "firefox"}},
			}}, nil
		},
		ResolveStory: func(id string) (policy.Story, error) {
			return policy.Story{Hash: id, Permalink: "https://example.com/" + id, Title: "t"}, nil
		},
		RunCapturer: func(context.Context, capturer.BatchInput) (capturer.BatchOutput, error) {
			return capturer.BatchOutput{
				Schema:   capturer.Schema,
				Capturer: capturer.CapturerInfo{Name: "stub", Version: "0"},
				Errors:   []capturer.ErrorEntry{},
				Captures: []capturer.CaptureResult{{
					Name:    "text",
					Spec:    &capturer.ArtifactRef{ID: "blake2b256-s", Size: 10, MediaType: "application/vnd.web-capture-archive.spec+json"},
					Payload: &capturer.ArtifactRef{ID: "blake2b256-p", Size: 20, MediaType: "text/plain; charset=utf-8"},
				}},
			}, nil
		},
		WriteArchive: archive.WriteWithHistory,
		TimeNow:      fixedTime,
		HistoryStore: nopHistory{},
	}
}

func TestRun_happyPath_singlePolicySingleSubject(t *testing.T) {
	dir := t.TempDir()
	args := Args{
		StoryID:     "6327282:5d1cf5",
		PolicyPath:  filepath.Join(dir, "nebulous.toml"),
		ArchiveRoot: filepath.Join(dir, "archives"),
	}
	rep := run(context.Background(), args, stubDeps(dir))

	if len(rep.Failed) != 0 {
		t.Errorf("failed: %+v", rep.Failed)
	}
	if len(rep.Written) != 1 {
		t.Fatalf("written: got %d, want 1", len(rep.Written))
	}
	if rep.Written[0].PolicyID != "p1" {
		t.Errorf("policy id: %q", rep.Written[0].PolicyID)
	}
	if rep.ExitCode() != 0 {
		t.Errorf("exit: %d", rep.ExitCode())
	}
}
```

### Step 5.4: Fill in the remaining orchestrator impl

Replace the placeholder `return` with a real return, and implement `buildSubjects`, `runOneJob`, `defaultDeps`. Key details:

```go
type subject struct {
	label   string
	url     string
	story   policy.Story
	err     string
}

func buildSubjects(args Args, d deps) []subject {
	var subs []subject
	if args.StoryID != "" {
		s, err := d.ResolveStory(args.StoryID)
		sub := subject{label: fmt.Sprintf("story:%s", args.StoryID), story: s, url: s.Permalink}
		if err != nil {
			sub.err = err.Error()
		}
		subs = append(subs, sub)
	}
	if args.URL != "" {
		h := sha256.Sum256([]byte(args.URL))
		subs = append(subs, subject{
			label: fmt.Sprintf("url:sha256-%s", hex.EncodeToString(h[:8])),
			url:   args.URL,
			story: policy.Story{Hash: hex.EncodeToString(h[:]), Permalink: args.URL},
		})
	}
	return subs
}

func runOneJob(ctx context.Context, report *Report, d deps, args Args, subj subject, pol policy.Policy) bool {
	url, err := policy.ExpandURL(pol.URL, policy.TemplateContext{Story: subj.story})
	if err != nil {
		report.Failed = append(report.Failed, JobFailure{
			PolicyID: pol.ID, Subject: subj.label,
			Kind: "template-expand-failed", Message: err.Error(),
		})
		return false
	}

	input := buildBatchInput(url, pol)
	out, err := d.RunCapturer(ctx, input)
	if err != nil {
		report.Failed = append(report.Failed, JobFailure{
			PolicyID: pol.ID, Subject: subj.label,
			Kind: "capturer-failed", Message: err.Error(),
		})
		return false
	}

	record := assembleRecord(subj, url, pol, out, d.TimeNow())
	path := subjectDir(args.ArchiveRoot, subj.label) + "/" + pol.ID + ".json"
	if err := d.WriteArchive(ctx, path, record, d.HistoryStore); err != nil {
		report.Failed = append(report.Failed, JobFailure{
			PolicyID: pol.ID, Subject: subj.label,
			Kind: "archive-write-failed", Message: err.Error(),
		})
		return false
	}

	report.Written = append(report.Written, Job{PolicyID: pol.ID, Subject: subj.label, Path: path})
	return true
}

func subjectDir(root, label string) string {
	// "story:abc" -> root/archives/by-story/abc
	// "url:sha256-ab12" -> root/archives/by-url/sha256-ab12
	switch {
	case strings.HasPrefix(label, "story:"):
		return filepath.Join(root, "by-story", strings.TrimPrefix(label, "story:"))
	case strings.HasPrefix(label, "url:"):
		return filepath.Join(root, "by-url", strings.TrimPrefix(label, "url:"))
	default:
		panic("orchestrator: unknown subject label: " + label)
	}
}

func buildBatchInput(url string, pol policy.Policy) capturer.BatchInput {
	// Translate policy.Capture -> capturer.CaptureRequest.
	// Exact mapping is mechanical; implement inline.
	// ...
}

func assembleRecord(subj subject, url string, pol policy.Policy, out capturer.BatchOutput, now time.Time) *archive.Record {
	// Translate capturer.BatchOutput captures into archive.Record.
	// Fields: schema, subject, url, policy_id, captured_at,
	// captures, errors. See RFC 0001 §Archive Record Format.
	// ...
}

func defaultDeps() deps {
	// Production wiring — real newsblur resolver, real capturer.Run,
	// real madder.Store satisfying archive.Writer.
	// ...
}
```

Fill in the `// ...` bodies inline — pure struct translation, no cleverness.

### Step 5.5: Run tests — expect pass

```bash
just test-go internal/alfa/orchestrator/
```

### Step 5.6: Commit

```
feat: add internal/alfa/orchestrator skeleton + happy path

orchestrator.Run composes policy + capturer + archive into the
archive pipeline per the design doc. Dependencies injected via a
deps struct for testability.

Happy path covered: single policy × single subject (story) produces
one archive record at <root>/by-story/<id>/<policy-id>.json.
Dual-subject, circuit breaker, and production defaultDeps land in
follow-up commits.

:clown: [Clown](https://github.com/amarbel-llc/clown)
```

---

## Task 6: Orchestrator — dual-subject (story + url) behavior

### Step 6.1: Failing test — both --story and --url supplied → two records

```go
func TestRun_dualSubjectProducesTwoRecords(t *testing.T) {
	dir := t.TempDir()
	args := Args{
		StoryID:     "6327282:5d1cf5",
		URL:         "https://example.com/canonical",
		PolicyPath:  filepath.Join(dir, "nebulous.toml"),
		ArchiveRoot: filepath.Join(dir, "archives"),
	}
	rep := run(context.Background(), args, stubDeps(dir))

	if len(rep.Written) != 2 {
		t.Fatalf("written: got %d, want 2", len(rep.Written))
	}
	var storySeen, urlSeen bool
	for _, w := range rep.Written {
		if strings.HasPrefix(w.Subject, "story:") { storySeen = true }
		if strings.HasPrefix(w.Subject, "url:")   { urlSeen = true }
	}
	if !storySeen || !urlSeen {
		t.Errorf("expected both story: and url: subjects, got %+v", rep.Written)
	}
}
```

### Step 6.2: Run test — expect pass (already handled by buildSubjects)

```bash
just test-go internal/alfa/orchestrator/
```

### Step 6.3: Commit

```
test: orchestrator dual-subject produces two archive records

Verifies that supplying both --story and --url results in two
Written entries under by-story/ and by-url/ subdirs, one per
(subject × policy) combination.

:clown: [Clown](https://github.com/amarbel-llc/clown)
```

---

## Task 7: Orchestrator — circuit breaker

### Step 7.1: Failing test — 3 consecutive failures trip bail

```go
func stubDepsAllFail(tmpDir string) deps {
	d := stubDeps(tmpDir)
	d.RunCapturer = func(context.Context, capturer.BatchInput) (capturer.BatchOutput, error) {
		return capturer.BatchOutput{}, errors.New("simulated")
	}
	d.LoadPolicies = func(string) ([]policy.Policy, error) {
		// 5 policies so we get >3 jobs and verify only 3 run before bail.
		var out []policy.Policy
		for i := 0; i < 5; i++ {
			out = append(out, policy.Policy{
				ID: fmt.Sprintf("p%d", i), URL: "{{.Story.Permalink}}", Isolation: "fresh",
				Captures: []policy.Capture{{Name: "t", Format: "text", Browser: "firefox"}},
			})
		}
		return out, nil
	}
	return d
}

func TestRun_threeConsecutiveFailuresBailOut(t *testing.T) {
	dir := t.TempDir()
	args := Args{StoryID: "6327282:5d1cf5", PolicyPath: filepath.Join(dir, "x"), ArchiveRoot: filepath.Join(dir, "a")}
	rep := run(context.Background(), args, stubDepsAllFail(dir))

	if !rep.BailedOut {
		t.Errorf("expected BailedOut=true")
	}
	if len(rep.Failed) != 3 {
		t.Errorf("expected exactly 3 failures before bail, got %d", len(rep.Failed))
	}
	if rep.ExitCode() != 2 {
		t.Errorf("exit code: got %d, want 2", rep.ExitCode())
	}
}
```

### Step 7.2: Run — expect pass (already implemented)

### Step 7.3: Failing test — failures interspersed with successes don't bail

```go
func TestRun_interspersedSuccessesResetCounter(t *testing.T) {
	dir := t.TempDir()
	d := stubDeps(dir)

	calls := 0
	d.RunCapturer = func(context.Context, capturer.BatchInput) (capturer.BatchOutput, error) {
		calls++
		// Pattern: fail, succeed, fail, succeed, fail (5 jobs).
		if calls%2 == 1 {
			return capturer.BatchOutput{}, errors.New("simulated")
		}
		return capturer.BatchOutput{
			Schema: capturer.Schema,
			Capturer: capturer.CapturerInfo{Name: "stub"},
			Errors: []capturer.ErrorEntry{},
			Captures: []capturer.CaptureResult{{
				Name: "text",
				Spec: &capturer.ArtifactRef{ID: "blake2b256-s", Size: 1, MediaType: "application/vnd.web-capture-archive.spec+json"},
				Payload: &capturer.ArtifactRef{ID: "blake2b256-p", Size: 1, MediaType: "text/plain; charset=utf-8"},
			}},
		}, nil
	}
	d.LoadPolicies = func(string) ([]policy.Policy, error) {
		var out []policy.Policy
		for i := 0; i < 5; i++ {
			out = append(out, policy.Policy{
				ID: fmt.Sprintf("p%d", i), URL: "{{.Story.Permalink}}", Isolation: "fresh",
				Captures: []policy.Capture{{Name: "t", Format: "text", Browser: "firefox"}},
			})
		}
		return out, nil
	}

	args := Args{StoryID: "s", PolicyPath: filepath.Join(dir, "x"), ArchiveRoot: filepath.Join(dir, "a")}
	rep := run(context.Background(), args, d)

	if rep.BailedOut {
		t.Errorf("should not bail out with interspersed successes")
	}
}
```

### Step 7.4: Run — expect pass

### Step 7.5: Commit

```
test: circuit breaker — 3 consecutive failures bail, interspersed
successes reset the counter. Verifies ExitCode=2 on bail.

:clown: [Clown](https://github.com/amarbel-llc/clown)
```

---

## Task 8: Orchestrator — TAP reporter (bob/tap-dancer)

**Files:**
- Create: `internal/alfa/orchestrator/report_tap.go`
- Create: `internal/alfa/orchestrator/report_test.go`
- Create: `internal/alfa/orchestrator/testdata/golden.tap`

### Step 8.1: Write failing test — TAP output matches golden

```go
func TestReport_TAP_golden(t *testing.T) {
	rep := Report{
		Written: []Job{
			{PolicyID: "p1", Subject: "story:abc"},
			{PolicyID: "p2", Subject: "story:abc"},
		},
		Failed: []JobFailure{
			{PolicyID: "p1", Subject: "url:sha256-def", Kind: "writer-failed", Message: "permission denied"},
		},
		BailedOut: false,
	}

	var buf bytes.Buffer
	if err := writeTAPReport(&buf, rep); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	golden, err := os.ReadFile(filepath.Join("testdata", "golden.tap"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(golden) {
		t.Errorf("TAP output diverged:\n--- want ---\n%s\n--- got ---\n%s", golden, got)
	}
}
```

### Step 8.2: Write the golden fixture

`internal/alfa/orchestrator/testdata/golden.tap`:

```
TAP version 14
1..3
ok 1 - p1 story:abc
ok 2 - p2 story:abc
not ok 3 - p1 url:sha256-def
  ---
  kind: writer-failed
  message: permission denied
  ...
```

### Step 8.3: Implement writeTAPReport

Use tap-dancer. Exact API comes from `go doc github.com/amarbel-llc/bob/...` — look up and call. Rough shape:

```go
func writeTAPReport(w io.Writer, r Report) error {
	tap := tapdancer.NewWriter(w)
	tap.Plan(len(r.Written) + len(r.Failed))
	// Emit test points in order: intermix by index would require
	// tracking; simplest to emit successes first then failures.
	// Acceptable since the design didn't pin ordering inside TAP.
	n := 1
	for _, j := range r.Written {
		tap.Ok(n, fmt.Sprintf("%s %s", j.PolicyID, j.Subject))
		n++
	}
	for _, f := range r.Failed {
		tap.NotOk(n, fmt.Sprintf("%s %s", f.PolicyID, f.Subject), tapdancer.YAML{
			"kind":    f.Kind,
			"message": f.Message,
		})
		n++
	}
	if r.BailedOut {
		tap.Bail("3 consecutive archive job failures")
	}
	return tap.Flush()
}
```

Adjust for the real tap-dancer API.

### Step 8.4: Run test — expect pass

```bash
just test-go internal/alfa/orchestrator/
```

Fix the golden or the implementation until they agree.

### Step 8.5: Commit

```
feat: orchestrator TAP reporter via bob/tap-dancer

Emits TAP-14 with per-job ok/not-ok and YAML block on failures.
Golden fixture at testdata/golden.tap covers the 3-job
mixed-success case. Bail-out path emits `Bail out!` per TAP spec.

:clown: [Clown](https://github.com/amarbel-llc/clown)
```

---

## Task 9: Orchestrator — JSON reporter + TTY dispatch

### Step 9.1: Failing test — JSON output shape

```go
func TestReport_JSON_shape(t *testing.T) {
	rep := Report{
		Written: []Job{{PolicyID: "p", Subject: "story:abc", Path: "/a/r.json"}},
		Failed:  []JobFailure{},
	}
	var buf bytes.Buffer
	if err := writeJSONReport(&buf, rep); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out["written"].([]any)) != 1 {
		t.Errorf("written: %+v", out["written"])
	}
	if out["bailed_out"].(bool) {
		t.Error("bailed_out should be false")
	}
}
```

### Step 9.2: Implement writeJSONReport

```go
func writeJSONReport(w io.Writer, r Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"written":    r.Written,
		"failed":     r.Failed,
		"bailed_out": r.BailedOut,
	})
}
```

### Step 9.3: Implement EmitReport(r, tty bool)

```go
func EmitReport(w io.Writer, r Report, tty bool) error {
	if tty {
		return writeTAPReport(w, r)
	}
	return writeJSONReport(w, r)
}
```

### Step 9.4: Failing test — dispatch chooses TAP in TTY mode, JSON otherwise

```go
func TestEmitReport_dispatchesByTTY(t *testing.T) {
	rep := Report{Written: []Job{{PolicyID: "p", Subject: "story:abc"}}}

	var tapOut, jsonOut bytes.Buffer
	_ = EmitReport(&tapOut, rep, true)
	_ = EmitReport(&jsonOut, rep, false)

	if !strings.HasPrefix(tapOut.String(), "TAP version 14") {
		t.Errorf("tty mode should start with TAP header, got: %q", tapOut.String()[:20])
	}
	if !strings.HasPrefix(strings.TrimSpace(jsonOut.String()), "{") {
		t.Errorf("non-tty mode should emit JSON object, got: %q", jsonOut.String()[:20])
	}
}
```

### Step 9.5: Run tests — expect pass

### Step 9.6: Commit

```
feat: orchestrator JSON reporter + TTY dispatch

Single output contract:
  tty   -> TAP-14 (writeTAPReport)
  pipe  -> one-shot JSON object (writeJSONReport)

EmitReport is the public dispatch. Caller decides tty by
calling term.IsTerminal(os.Stdout) on its own.

:clown: [Clown](https://github.com/amarbel-llc/clown)
```

---

## Task 10: `cmd/nebulous/archive.go` — subcommand wiring

**Files:**
- Create: `cmd/nebulous/archive.go`
- Modify: `cmd/nebulous/main.go` — register the new subcommand.

### Step 10.1: Implement subcommand

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/friedenberg/nebulous/internal/alfa/orchestrator"

	"golang.org/x/term" // or the tty-detect helper already in use
)

func archiveMain(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("archive", flag.ContinueOnError)
	story := fs.String("story", "", "story id (e.g. 6327282:5d1cf5)")
	url := fs.String("url", "", "url to capture")
	policyPath := fs.String("policy", defaultPolicyPath(), "path to nebulous.toml")
	archiveRoot := fs.String("archive-root", defaultArchiveRoot(), "directory for archive records")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}

	if *story == "" && *url == "" {
		fmt.Fprintln(os.Stderr, "archive: at least one of --story or --url is required")
		return 3
	}

	report := orchestrator.Run(ctx, orchestrator.Args{
		StoryID:     *story,
		URL:         *url,
		PolicyPath:  *policyPath,
		ArchiveRoot: *archiveRoot,
	})

	tty := term.IsTerminal(int(os.Stdout.Fd()))
	_ = orchestrator.EmitReport(os.Stdout, report, tty)
	return report.ExitCode()
}

func defaultPolicyPath() string {
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(xdg, "nebulous", "nebulous.toml")
}

func defaultArchiveRoot() string {
	xdg := os.Getenv("XDG_DATA_HOME")
	if xdg == "" {
		xdg = filepath.Join(os.Getenv("HOME"), ".local/share")
	}
	return filepath.Join(xdg, "nebulous", "archives")
}
```

### Step 10.2: Wire into main.go

In the existing `cmd/nebulous/main.go` subcommand dispatcher, add:

```go
case "archive":
    os.Exit(archiveMain(ctx, os.Args[2:]))
```

### Step 10.3: Verify build

```bash
just build
```

### Step 10.4: Smoke test manually

```bash
./build/debug/nebulous archive --help 2>&1 | head -20
```

Expected: flag-set help text listing `--story`, `--url`, `--policy`, `--archive-root`.

### Step 10.5: Commit

```
feat: cmd/nebulous archive subcommand

Thin wiring layer per design. Flag parsing, XDG defaults for
--policy and --archive-root, TTY detect, orchestrator.Run,
orchestrator.EmitReport, exit code from Report.ExitCode.

All business logic lives in internal/alfa/orchestrator.

:clown: [Clown](https://github.com/amarbel-llc/clown)
```

---

## Task 11: `zz-tests_bats/orchestrator.bats` — end-to-end

**Files:**
- Create: `zz-tests_bats/orchestrator.bats`
- Create: `zz-tests_bats/fixtures/orchestrator-nebulous.toml`
- Create: `zz-tests_bats/fixtures/orchestrator-writer-stub.sh`

### Step 11.1: Write the fixture

`zz-tests_bats/fixtures/orchestrator-nebulous.toml`:

```toml
[[policy]]
id  = "default"
url = "{{.Story.Permalink}}"
isolation = "fresh"

[[policy.capture]]
name    = "text"
format  = "text"
browser = "firefox"
```

`zz-tests_bats/fixtures/orchestrator-writer-stub.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
size=$(wc -c)
printf '{"id":"blake2b256-stub-%s","size":%s}\n' "$size" "$size"
```

Mark executable via git attributes or set `0o755` on write.

### Step 11.2: Write the test file

`zz-tests_bats/orchestrator.bats`:

```bash
setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
  require_bin NEBULOUS_BIN nebulous

  fixture_dir="$(dirname "$BATS_TEST_FILE")/fixtures"
  cp "$fixture_dir/orchestrator-nebulous.toml" "$BATS_TEST_TMPDIR/nebulous.toml"
  cp "$fixture_dir/orchestrator-writer-stub.sh" "$BATS_TEST_TMPDIR/writer-stub.sh"
  chmod +x "$BATS_TEST_TMPDIR/writer-stub.sh"
}

# bats file_tags=integration,orchestrator

function orchestrator_happy_path_produces_record_files { # @test
  mkdir -p "$BATS_TEST_TMPDIR/archives"

  local bin="${NEBULOUS_BIN:-nebulous}"
  run "$bin" archive \
    --url=https://example.com/ \
    --policy="$BATS_TEST_TMPDIR/nebulous.toml" \
    --archive-root="$BATS_TEST_TMPDIR/archives"

  assert_success

  # JSON output on non-tty stdout — parse with jq.
  assert_equal "$(echo "$output" | jq -r '.bailed_out')" 'false'
  assert_equal "$(echo "$output" | jq '.written | length')" '1'
  assert_equal "$(echo "$output" | jq '.failed | length')" '0'

  # Record file exists at the expected path.
  local pth
  pth=$(echo "$output" | jq -r '.written[0].path')
  [[ -f "$pth" ]] || fail "record file not at $pth"

  # Record has the expected RFC 0001 shape.
  assert_equal "$(jq -r '.schema' "$pth")" 'web-capture-archive.record/v1'
  assert_equal "$(jq -r '.policy_id' "$pth")" 'default'
}

function orchestrator_rejects_bad_policy { # @test
  echo '{not valid toml' > "$BATS_TEST_TMPDIR/nebulous.toml"

  local bin="${NEBULOUS_BIN:-nebulous}"
  run "$bin" archive \
    --url=https://example.com/ \
    --policy="$BATS_TEST_TMPDIR/nebulous.toml" \
    --archive-root="$BATS_TEST_TMPDIR/archives"

  [[ "$status" -eq 3 ]] || fail "expected exit 3, got $status"
}
```

### Step 11.3: Update `common.bash` if needed

Ensure `require_bin NEBULOUS_BIN nebulous` works; the existing file requires MIGRATE_CACHE_BIN + MADDER_BIN, add NEBULOUS_BIN there.

### Step 11.4: Run bats

```bash
just test-bats
```

Expected: all existing tests green + two new orchestrator tests green.

### Step 11.5: Commit

```
test: bats integration for `nebulous archive` subcommand

End-to-end via the real chrest binary and a shell writer stub.
Fixture policy has one capture (text) with split=false.

Covers:
- Happy path: exit 0, JSON report, record file at reported path,
  record schema + policy_id correct.
- Bad-policy: exit 3, stdout JSON still emitted.

:clown: [Clown](https://github.com/amarbel-llc/clown)
```

---

## Task 12: Update RFC 0001 informative note

### Step 12.1: Edit docs/rfcs/0001-web-capture-archive-protocol.md § URL Templating

Add after the existing mustache-style sentence:

> The reference implementation (`nebulous`) uses Go's `text/template` syntax in strict mode (`Option("missingkey=error")`). Policies reference story fields as `{{.Story.Permalink}}`. The earlier mustache-style example is informative only; the syntax is an orchestrator concern per this section.

### Step 12.2: Commit

```
docs: RFC 0001 — pin nebulous's chosen URL template syntax

Informative note: nebulous now uses Go's text/template in strict
mode. The RFC still defers syntax to orchestrators; this captures
what nebulous actually shipped for readers cross-referencing the
policy format.

:clown: [Clown](https://github.com/amarbel-llc/clown)
```

---

## Task 13: Merge + sync

### Step 13.1: Run full suite one last time

```bash
just build
just test-go
just test-bats
```

All green.

### Step 13.2: Merge and sync via spinclass

```
mcp__spinclass__merge-this-session with git_sync: true
```

---

## Execution notes

- **TDD discipline**: every task writes the failing test first, runs it, watches it fail with the expected message, then implements. Skipping this sacrifices the single strongest signal that the test actually tests what you think.
- **Commits**: one per task. Each commit leaves the tree green.
- **Skill cross-references**:
  - @superpowers:subagent-driven-development for task execution
  - @bob:tap-dancer API lookup happens in Task 8
  - @bob:commit for conventional-commit authoring if messages drift

## What's deliberately deferred

- History traversal helper (`orchestrator.HistoryChain`). Read path; ship when we have a consumer.
- Orchestrator-side normalization. Gated on ADR 0001.
- Parallelism / per-subject locking.
- Triggers (hooks, `--watch`).
- Richer `TemplateContext` fields (`.Env`, `.Now`).
- Split=true bats coverage.

## Final acceptance

- `just build` green.
- `just test-go` green — all new packages + all existing packages.
- `just test-bats` green — existing + new orchestrator.bats tests.
- `./build/debug/nebulous archive --story=<real id> --url=<any url>` against a real chrest + madder produces records under `$XDG_DATA_HOME/nebulous/archives/by-story/…` and `$XDG_DATA_HOME/nebulous/archives/by-url/…`.
- TAP output visible when stdout is a tty; JSON otherwise.
