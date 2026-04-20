// Package orchestrator composes the RFC 0001 substrate (policy,
// capturer, archive) into the `nebulous archive-capture` pipeline.
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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/friedenberg/nebulous/internal/0/archive"
	"github.com/friedenberg/nebulous/internal/alfa/capturer"
	"github.com/friedenberg/nebulous/internal/alfa/policy"
)

// Args is the public input to Run.
type Args struct {
	// StoryIDs are nebulous story identifiers to archive. For each,
	// the orchestrator resolves its permalink from the local newsblur
	// cache and uses story:<id> as the subject label.
	StoryIDs []string

	// URLs are concrete URLs to archive. Each becomes a subject with
	// label url:sha256-<first-8-bytes>. Mixing StoryIDs and URLs in
	// one Run produces one archive record per (subject, policy) pair.
	URLs []string

	// PolicyPath is the path to nebulous.toml. Defaults to
	// $XDG_CONFIG_HOME/nebulous/nebulous.toml at the CLI layer.
	PolicyPath string

	// ArchiveRoot is the directory under which archive records land.
	// Defaults to $XDG_DATA_HOME/nebulous/archives at the CLI layer.
	ArchiveRoot string

	// Jobs is the worker-pool size for (subject, policy) job
	// execution. Values < 2 run serially (current behavior). Values
	// >= 2 dispatch N workers concurrently; per-worker state uses
	// the same Deps instance, so Deps implementations MUST be
	// goroutine-safe. chrest invocations are subprocess-isolated
	// (each `capture-batch` launches its own browser + profile) and
	// madder blob writes are content-addressed and safe to race per
	// madder ADR-0002.
	Jobs int
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

// jobUnit is one (subject, policy) pair fed into the worker pool.
type jobUnit struct {
	subj subject
	pol  policy.Policy
}

// jobResult is runOneJob's return value, carrying either a Job
// (success) or a JobFailure (error). Kept as a value type so worker
// goroutines never touch shared Report state.
type jobResult struct {
	ok      bool
	job     Job
	failure JobFailure
}

// run is the unexported composition function. Shared between Run()
// (production) and tests (stub deps). Kept separate from Run only to
// keep the public entry-point signature stable if we later want a
// wrapper with, e.g., default-value fallbacks.
//
// Execution is a fan-out over (subject × policy) jobs via a worker
// pool of size args.Jobs (minimum 1). Bailout uses a "last-3-
// completed all failed" sliding window — order-insensitive so it
// survives non-deterministic completion under workers. Report is
// sorted by (subject, policy_id) before return so output is
// deterministic regardless of worker scheduling.
func run(ctx context.Context, args Args, d Deps) Report {
	policies, err := d.LoadPolicies(args.PolicyPath)
	if err != nil {
		return Report{Failed: []JobFailure{{Kind: "policy-load-failed", Message: err.Error()}}}
	}

	subjects := buildSubjects(args, d)

	jobs := make([]jobUnit, 0, len(subjects)*len(policies))
	for _, subj := range subjects {
		for _, pol := range policies {
			jobs = append(jobs, jobUnit{subj: subj, pol: pol})
		}
	}

	report := runJobs(ctx, args, d, jobs)
	sortReport(&report)
	return report
}

// runJobs fans jobs out over args.Jobs workers and collects results
// into a Report, honoring the window-based bailout. Returns an
// unsorted Report — callers sort before emitting.
func runJobs(ctx context.Context, args Args, d Deps, jobs []jobUnit) Report {
	if len(jobs) == 0 {
		return Report{}
	}

	nworkers := args.Jobs
	if nworkers < 1 {
		nworkers = 1
	}
	if nworkers > len(jobs) {
		nworkers = len(jobs)
	}

	// Cancelable sub-context so we can stop in-flight workers on bail.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobCh := make(chan jobUnit)
	resCh := make(chan jobResult, len(jobs))

	// Feeder: push jobs onto jobCh until exhausted or cancelled.
	go func() {
		defer close(jobCh)
		for _, j := range jobs {
			select {
			case <-runCtx.Done():
				return
			case jobCh <- j:
			}
		}
	}()

	// Workers.
	var wg sync.WaitGroup
	for i := 0; i < nworkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				resCh <- runOneJob(runCtx, d, args, j.subj, j.pol)
			}
		}()
	}

	// Close resCh once all workers are done.
	go func() {
		wg.Wait()
		close(resCh)
	}()

	// Consume results in completion order. Window-based bailout: if
	// the last 3 completed jobs all failed, we stop building the
	// report and cancel in-flight workers. Results that arrive
	// after bailout are drained without mutating the report so the
	// Report shape reflects state-at-bail, not cancellation noise.
	var report Report
	var window [3]bool
	windowLen := 0
	bailed := false

	for r := range resCh {
		if bailed {
			continue
		}
		if r.ok {
			report.Written = append(report.Written, r.job)
		} else {
			report.Failed = append(report.Failed, r.failure)
		}

		if windowLen < 3 {
			window[windowLen] = !r.ok
			windowLen++
		} else {
			window[0] = window[1]
			window[1] = window[2]
			window[2] = !r.ok
		}

		if windowLen == 3 && window[0] && window[1] && window[2] {
			report.BailedOut = true
			bailed = true
			cancel()
		}
	}

	return report
}

// sortReport orders report.Written and report.Failed by
// (subject, policy_id) so output is deterministic regardless of
// worker completion order.
func sortReport(r *Report) {
	sort.Slice(r.Written, func(i, j int) bool {
		if r.Written[i].Subject != r.Written[j].Subject {
			return r.Written[i].Subject < r.Written[j].Subject
		}
		return r.Written[i].PolicyID < r.Written[j].PolicyID
	})
	sort.Slice(r.Failed, func(i, j int) bool {
		if r.Failed[i].Subject != r.Failed[j].Subject {
			return r.Failed[i].Subject < r.Failed[j].Subject
		}
		return r.Failed[i].PolicyID < r.Failed[j].PolicyID
	})
}

// buildSubjects emits one subject per input target. Order is
// StoryIDs first (in input order), then URLs (in input order);
// orchestrator iterates captures for every (policy, subject) pair.
func buildSubjects(args Args, d Deps) []subject {
	subs := make([]subject, 0, len(args.StoryIDs)+len(args.URLs))
	for _, id := range args.StoryIDs {
		s, err := d.ResolveStory(id)
		sub := subject{
			label: "story:" + id,
			url:   s.Permalink,
			story: s,
		}
		if err != nil {
			sub.err = err.Error()
		}
		subs = append(subs, sub)
	}
	for _, u := range args.URLs {
		h := sha256.Sum256([]byte(u))
		subs = append(subs, subject{
			label: "url:sha256-" + hex.EncodeToString(h[:8]),
			url:   u,
			story: policy.Story{
				Hash:      hex.EncodeToString(h[:]),
				Permalink: u,
			},
		})
	}
	return subs
}

// runOneJob handles one (policy, subject) archive attempt. Returns
// a jobResult by value so it can be called from worker goroutines
// without touching shared Report state.
func runOneJob(ctx context.Context, d Deps, args Args, subj subject, pol policy.Policy) jobResult {
	fail := func(kind, message string) jobResult {
		return jobResult{failure: JobFailure{
			PolicyID: pol.ID, Subject: subj.label,
			Kind: kind, Message: message,
		}}
	}

	if subj.err != "" {
		return fail("story-resolve-failed", subj.err)
	}

	url, err := policy.ExpandURL(pol.URL, policy.TemplateContext{Story: subj.story})
	if err != nil {
		return fail("template-expand-failed", err.Error())
	}

	out, err := d.RunCapturer(ctx, buildBatchInput(url, pol, d))
	if err != nil {
		return fail("capturer-failed", err.Error())
	}

	record := assembleRecord(subj, url, pol, out, d.TimeNow())
	path := recordPath(args.ArchiveRoot, subj.label, pol.ID)
	if err := d.WriteArchive(ctx, path, record, d.HistoryStore); err != nil {
		return fail("archive-write-failed", err.Error())
	}

	return jobResult{
		ok:  true,
		job: Job{PolicyID: pol.ID, Subject: subj.label, Path: path},
	}
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
