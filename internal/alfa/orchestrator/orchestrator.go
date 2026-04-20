// Package orchestrator composes the RFC 0001 substrate (policy,
// capturer, archive) into the `nebulous archive` pipeline.
//
// Design: docs/plans/2026-04-19-orchestrator-design.md
// Plan:   docs/plans/2026-04-19-orchestrator-implementation.md
//
// Public surface is Args + Report + Run. Callers pass Args,
// receive Report. Report.ExitCode maps to the orchestrator's
// exit-code contract (0 = all ok, 1 = mixed, 2 = bailed out,
// 3 = pre-job failure like bad policy).
//
// Internal composition uses a deps struct (see deps.go) injected
// into run(). run() is unexported and always receives deps; the
// public Run() calls run() with defaultDeps(). Tests call run()
// directly with stub deps to keep the orchestrator logic
// hermetically testable.
package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/friedenberg/nebulous/internal/0/archive"
	"github.com/friedenberg/nebulous/internal/alfa/capturer"
	"github.com/friedenberg/nebulous/internal/alfa/policy"
)

// Args is the public input to Run.
type Args struct {
	// StoryID identifies a nebulous story to archive. When set, the
	// orchestrator resolves its permalink from the local newsblur
	// cache and uses story:<StoryID> as the subject label.
	StoryID string

	// URL is a concrete URL to archive. When set alongside StoryID,
	// the orchestrator runs both (producing two archive records per
	// policy); when set alone, url:<sha256-of-URL>[:16] becomes the
	// subject label.
	URL string

	// PolicyPath is the path to nebulous.toml. Defaults to
	// $XDG_CONFIG_HOME/nebulous/nebulous.toml at the CLI layer.
	PolicyPath string

	// ArchiveRoot is the directory under which archive records land.
	// Defaults to $XDG_DATA_HOME/nebulous/archives at the CLI layer.
	ArchiveRoot string
}

// Job records one successful (policy, subject) archive result.
type Job struct {
	PolicyID string
	Subject  string
	Path     string
}

// JobFailure records one failed (policy, subject) archive attempt.
type JobFailure struct {
	PolicyID string
	Subject  string
	Kind     string
	Message  string
}

// Report is the accumulated outcome of a Run.
type Report struct {
	Written   []Job
	Failed    []JobFailure
	BailedOut bool
}

// ExitCode maps Report to the subcommand exit-code contract from the
// design doc.
//
//	2 — circuit breaker tripped (BailedOut == true)
//	1 — mixed ok + not-ok, no bail
//	0 — all jobs ok
func (r Report) ExitCode() int {
	if r.BailedOut {
		return 2
	}
	if len(r.Failed) > 0 {
		return 1
	}
	return 0
}

// Run is the public composition entry point. The caller (CLI layer)
// supplies a Deps with every field populated — Run does not construct
// defaults. Invariant violations inside orchestrator composition
// panic; expected failures (bad policy, capturer spawn, write errors)
// become Report entries.
func Run(ctx context.Context, args Args, d Deps) Report {
	return run(ctx, args, d)
}

// subject is the internal per-subject job source: the subject label
// as it appears in the archive record path, the concrete URL to
// capture, and the template context for URL expansion. If err is
// non-empty, the subject could not be resolved and every (policy,
// subject) job for it will record a JobFailure with the error.
type subject struct {
	label string
	url   string
	story policy.Story
	err   string
}

// run is the unexported composition function. Shared between Run()
// (production) and tests (stub deps). Kept separate from Run only to
// keep the public entry-point signature stable if we later want a
// wrapper with, e.g., default-value fallbacks.
func run(ctx context.Context, args Args, d Deps) Report {
	policies, err := d.LoadPolicies(args.PolicyPath)
	if err != nil {
		return Report{Failed: []JobFailure{{Kind: "policy-load-failed", Message: err.Error()}}}
	}

	subjects := buildSubjects(args, d)

	var report Report
	consecutive := 0

	for _, subj := range subjects {
		for _, pol := range policies {
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
	return report
}

// buildSubjects emits one subject per selector flag. Both supplied
// means two subjects; orchestrator iterates captures for every
// (policy, subject) pair.
func buildSubjects(args Args, d Deps) []subject {
	var subs []subject
	if args.StoryID != "" {
		s, err := d.ResolveStory(args.StoryID)
		sub := subject{
			label: "story:" + args.StoryID,
			url:   s.Permalink,
			story: s,
		}
		if err != nil {
			sub.err = err.Error()
		}
		subs = append(subs, sub)
	}
	if args.URL != "" {
		h := sha256.Sum256([]byte(args.URL))
		subs = append(subs, subject{
			label: "url:sha256-" + hex.EncodeToString(h[:8]),
			url:   args.URL,
			story: policy.Story{
				Hash:      hex.EncodeToString(h[:]),
				Permalink: args.URL,
			},
		})
	}
	return subs
}

// runOneJob handles one (policy, subject) archive attempt. Returns
// true on success, false on failure. Appends to report accordingly.
func runOneJob(ctx context.Context, report *Report, d Deps, args Args, subj subject, pol policy.Policy) bool {
	if subj.err != "" {
		report.Failed = append(report.Failed, JobFailure{
			PolicyID: pol.ID, Subject: subj.label,
			Kind: "story-resolve-failed", Message: subj.err,
		})
		return false
	}

	url, err := policy.ExpandURL(pol.URL, policy.TemplateContext{Story: subj.story})
	if err != nil {
		report.Failed = append(report.Failed, JobFailure{
			PolicyID: pol.ID, Subject: subj.label,
			Kind: "template-expand-failed", Message: err.Error(),
		})
		return false
	}

	out, err := d.RunCapturer(ctx, buildBatchInput(url, pol, d))
	if err != nil {
		report.Failed = append(report.Failed, JobFailure{
			PolicyID: pol.ID, Subject: subj.label,
			Kind: "capturer-failed", Message: err.Error(),
		})
		return false
	}

	record := assembleRecord(subj, url, pol, out, d.TimeNow())
	path := recordPath(args.ArchiveRoot, subj.label, pol.ID)
	if err := d.WriteArchive(ctx, path, record, d.HistoryStore); err != nil {
		report.Failed = append(report.Failed, JobFailure{
			PolicyID: pol.ID, Subject: subj.label,
			Kind: "archive-write-failed", Message: err.Error(),
		})
		return false
	}

	report.Written = append(report.Written, Job{
		PolicyID: pol.ID, Subject: subj.label, Path: path,
	})
	return true
}

func recordPath(root, label, policyID string) string {
	switch {
	case strings.HasPrefix(label, "story:"):
		return filepath.Join(root, "by-story", strings.TrimPrefix(label, "story:"), policyID+".json")
	case strings.HasPrefix(label, "url:"):
		return filepath.Join(root, "by-url", strings.TrimPrefix(label, "url:"), policyID+".json")
	default:
		panic(fmt.Sprintf("orchestrator: unknown subject label prefix: %q", label))
	}
}

// buildBatchInput translates a resolved (URL, Policy) pair into the
// capturer's BatchInput schema. Mechanical struct-to-struct mapping.
func buildBatchInput(url string, pol policy.Policy, d Deps) capturer.BatchInput {
	captures := make([]capturer.CaptureRequest, 0, len(pol.Captures))
	for _, c := range pol.Captures {
		var splitPtr *bool
		if c.Split {
			v := c.Split
			splitPtr = &v
		}
		exts := make([]capturer.ExtensionRef, 0, len(c.Extensions))
		for _, e := range c.Extensions {
			exts = append(exts, capturer.ExtensionRef{ID: e.ID, Version: e.Version})
		}
		captures = append(captures, capturer.CaptureRequest{
			Name:       c.Name,
			Format:     c.Format,
			Browser:    c.Browser,
			Options:    c.Options,
			Split:      splitPtr,
			Extensions: exts,
			Flags:      c.Flags,
		})
	}
	return capturer.BatchInput{
		Schema:   capturer.Schema,
		Writer:   capturer.WriterCmd{Cmd: d.WriterCmd},
		URL:      url,
		Defaults: capturer.Defaults{Isolation: pol.Isolation},
		Captures: captures,
	}
}

// assembleRecord translates a capturer.BatchOutput into an
// archive.Record with the orchestrator-supplied context fields
// (subject, url, policy_id, captured_at).
func assembleRecord(subj subject, url string, pol policy.Policy, out capturer.BatchOutput, now time.Time) *archive.Record {
	captures := make([]archive.Capture, 0, len(out.Captures))
	for _, c := range out.Captures {
		ac := archive.Capture{Name: c.Name}
		if c.Spec != nil {
			ac.Spec = toArchiveArtifactRef(c.Spec)
		}
		if c.Payload != nil {
			ac.Payload = toArchiveArtifactRef(c.Payload)
		}
		if c.Envelope != nil {
			ac.Envelope = toArchiveArtifactRef(c.Envelope)
		}
		if c.Error != nil {
			ac.Error = &archive.Error{Kind: c.Error.Kind, Message: c.Error.Message}
		}
		captures = append(captures, ac)
	}

	errs := make([]archive.Error, 0, len(out.Errors))
	for _, e := range out.Errors {
		errs = append(errs, archive.Error{Kind: e.Kind, Message: e.Message})
	}

	return &archive.Record{
		Schema:     archive.Schema,
		Subject:    subj.label,
		URL:        url,
		PolicyID:   pol.ID,
		CapturedAt: archive.FormatTimestamp(now),
		Captures:   captures,
		Errors:     errs,
	}
}

func toArchiveArtifactRef(a *capturer.ArtifactRef) *archive.ArtifactRef {
	return &archive.ArtifactRef{
		ID:         a.ID,
		Size:       a.Size,
		MediaType:  a.MediaType,
		Normalized: a.Normalized,
	}
}
