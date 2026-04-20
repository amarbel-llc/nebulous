// Package archive serializes and writes archive records per RFC 0001
// § Archive Record Format.
//
// An archive record is a plain-JSON file owned by the orchestrator
// that points at the three content-addressed artifacts (spec,
// payload, envelope) produced for each capture in a policy. The file
// is not content-addressed; it may be pretty-printed or compact as
// the caller prefers.
//
// The package deliberately has no dependency on internal/0/jcs:
// archive records are not hashed, so canonicalization is unnecessary.
//
// See docs/rfcs/0001-web-capture-archive-protocol.md.
package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Schema is the value required in a record's `schema` field.
const Schema = "web-capture-archive.record/v1"

// timestampFormat is RFC 3339 with exactly millisecond precision, in
// UTC, per § Archive Record Format.
const timestampFormat = "2006-01-02T15:04:05.000Z"

// ArtifactRef is a pointer to one content-addressed blob in the writer's
// blob store. Mirrors § Capturer Protocol's "artifact ref" shape.
type ArtifactRef struct {
	ID         string `json:"id"`
	Size       int64  `json:"size"`
	MediaType  string `json:"media_type"`
	Normalized *bool  `json:"normalized,omitempty"`
}

// Error is the error shape used both for batch-level errors (on a
// Record) and per-capture errors (on a Capture). `kind` is a short
// machine-readable category; `message` is human-readable.
type Error struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// Capture is one line-item in a Record. On success, Spec and Payload
// are set (and Envelope is set when split was true). On failure,
// Error is set and the artifact refs are absent.
type Capture struct {
	Name     string       `json:"name"`
	Spec     *ArtifactRef `json:"spec,omitempty"`
	Payload  *ArtifactRef `json:"payload,omitempty"`
	Envelope *ArtifactRef `json:"envelope,omitempty"`
	Error    *Error       `json:"error,omitempty"`
}

// Record is the archive record written per (subject, policy) tuple.
type Record struct {
	Schema     string    `json:"schema"`
	Subject    string    `json:"subject"`
	URL        string    `json:"url"`
	PolicyID   string    `json:"policy_id"`
	CapturedAt string    `json:"captured_at"`
	Captures   []Capture `json:"captures"`
	Errors     []Error   `json:"errors"`

	// Previous is the markl ID of the prior record for this
	// (subject, policy) tuple, stored by WriteWithHistory into a
	// content-addressed blob store. Nil when no prior record exists.
	// Forms a linked-list history that can be traversed by
	// repeatedly fetching the referenced blob and re-decoding.
	Previous *string `json:"previous,omitempty"`
}

// Writer abstracts a content-addressed blob store. Used by
// WriteWithHistory to stash prior records before overwriting.
// internal/0/madder.Store satisfies this interface via its CLI
// wrapper — this package intentionally does not import madder.
type Writer interface {
	Write(ctx context.Context, src io.Reader) (id string, err error)
}

// FormatTimestamp returns t formatted as a `captured_at` field value:
// RFC 3339 with exactly millisecond precision, in UTC.
func FormatTimestamp(t time.Time) string {
	return t.UTC().Format(timestampFormat)
}

// ParseTimestamp parses a `captured_at` string back into a time.Time.
func ParseTimestamp(s string) (time.Time, error) {
	return time.Parse(timestampFormat, s)
}

// Validate returns an error if r is missing a required field or
// contains an invalid capture entry.
func (r *Record) Validate() error {
	if r == nil {
		return errors.New("archive: nil record")
	}
	if r.Schema != Schema {
		return fmt.Errorf("archive: schema must be %q, got %q", Schema, r.Schema)
	}
	if r.Subject == "" {
		return errors.New("archive: subject is required")
	}
	if r.URL == "" {
		return errors.New("archive: url is required")
	}
	if r.PolicyID == "" {
		return errors.New("archive: policy_id is required")
	}
	if r.CapturedAt == "" {
		return errors.New("archive: captured_at is required")
	}
	if _, err := ParseTimestamp(r.CapturedAt); err != nil {
		return fmt.Errorf("archive: captured_at must be RFC 3339 ms UTC: %w", err)
	}
	if r.Errors == nil {
		return errors.New("archive: errors must be at least [] (not null)")
	}
	if r.Captures == nil {
		return errors.New("archive: captures must be at least [] (not null)")
	}
	names := make(map[string]bool, len(r.Captures))
	for i, c := range r.Captures {
		if c.Name == "" {
			return fmt.Errorf("archive: captures[%d].name is required", i)
		}
		if names[c.Name] {
			return fmt.Errorf("archive: captures[%d].name %q is duplicated", i, c.Name)
		}
		names[c.Name] = true
		if err := c.validate(i); err != nil {
			return err
		}
	}
	return nil
}

func (c *Capture) validate(i int) error {
	hasSuccess := c.Spec != nil || c.Payload != nil || c.Envelope != nil
	hasError := c.Error != nil
	switch {
	case hasSuccess && hasError:
		return fmt.Errorf("archive: captures[%d] has both artifact refs and error; mutually exclusive", i)
	case !hasSuccess && !hasError:
		return fmt.Errorf("archive: captures[%d] has neither artifact refs nor error", i)
	case hasSuccess:
		if c.Spec == nil {
			return fmt.Errorf("archive: captures[%d] succeeded but is missing spec", i)
		}
		if c.Payload == nil {
			return fmt.Errorf("archive: captures[%d] succeeded but is missing payload", i)
		}
	case hasError:
		if c.Error.Kind == "" {
			return fmt.Errorf("archive: captures[%d].error.kind is required", i)
		}
		if c.Error.Message == "" {
			return fmt.Errorf("archive: captures[%d].error.message is required", i)
		}
	}
	return nil
}

// Write serializes r as JSON and writes it atomically to path.
//
// Atomicity: the bytes are first written to a tempfile in the target
// directory, fsynced, then renamed over path. A reader of path at any
// instant sees either the old contents or the new contents, never a
// torn partial file.
//
// Parent directories of path are created if they don't exist.
//
// The record is validated before any filesystem work happens; a
// validation failure leaves the filesystem untouched.
func Write(path string, r *Record) error {
	if err := r.Validate(); err != nil {
		return err
	}

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("archive: marshal: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("archive: mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".archive-"+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("archive: create tempfile: %w", err)
	}
	tmpName := tmp.Name()

	// Cleanup tempfile on any failure after this point.
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("archive: write tempfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("archive: fsync tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("archive: close tempfile: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("archive: rename %s → %s: %w", tmpName, path, err)
	}
	return nil
}

// Read loads an archive record from path. Convenience for tests and
// downstream consumers; not required by the write protocol itself.
func Read(path string) (*Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("archive: read %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var r Record
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("archive: decode %s: %w", path, err)
	}
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("archive: validate %s: %w", path, err)
	}
	return &r, nil
}

// DefaultPath returns the RECOMMENDED path convention from § Archive
// Record Format: dataRoot/archives/<subject>/<policyID>.json. Callers
// MAY ignore and construct their own.
func DefaultPath(dataRoot, subject, policyID string) string {
	return filepath.Join(dataRoot, "archives", subject, policyID+".json")
}

// WriteWithHistory is Write plus linked-list history. If path already
// exists, the current contents are piped through w to obtain a markl
// ID, r.Previous is set to that ID, and r is then written atomically
// over path. If path does not exist, r.Previous is left unchanged
// (typically nil in fresh callers).
//
// Atomicity is the same as Write: tempfile in the same directory,
// fsync, rename. If any step after the writer invocation fails, the
// existing file is left unchanged — the prior copy is already stored
// in w, so no history is lost.
func WriteWithHistory(ctx context.Context, path string, r *Record, w Writer) error {
	if w == nil {
		return errors.New("archive: writer is required")
	}

	priorBytes, err := os.ReadFile(path)
	switch {
	case err == nil:
		id, werr := w.Write(ctx, bytes.NewReader(priorBytes))
		if werr != nil {
			return fmt.Errorf("archive: store prior record: %w", werr)
		}
		r.Previous = &id
	case errors.Is(err, os.ErrNotExist):
		// No prior record; leave Previous untouched.
	default:
		return fmt.Errorf("archive: read prior %s: %w", path, err)
	}

	return Write(path, r)
}
