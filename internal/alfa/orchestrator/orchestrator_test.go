package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/friedenberg/nebulous/internal/0/archive"
	"github.com/friedenberg/nebulous/internal/alfa/capturer"
	"github.com/friedenberg/nebulous/internal/alfa/policy"
)

// fixedTime returns a deterministic timestamp so archive records
// don't churn across test runs.
func fixedTime() time.Time {
	return time.Date(2026, 4, 19, 12, 0, 0, 412_000_000, time.UTC)
}

// singlePolicySingleCapture is the minimal valid policy set: one
// policy with one text capture, split=false.
func singlePolicySingleCapture() []policy.Policy {
	return []policy.Policy{{
		ID:        "p1",
		URL:       "{{.Story.Permalink}}",
		Isolation: "fresh",
		Captures: []policy.Capture{{
			Name:    "text",
			Format:  "text",
			Browser: "firefox",
		}},
	}}
}

// successBatchOutput is the canned capturer response for a single
// text capture that succeeded (split=false → no envelope).
func successBatchOutput() capturer.BatchOutput {
	return capturer.BatchOutput{
		Schema:   capturer.Schema,
		Capturer: capturer.CapturerInfo{Name: "stub", Version: "0"},
		Errors:   []capturer.ErrorEntry{},
		Captures: []capturer.CaptureResult{{
			Name:    "text",
			Spec:    &capturer.ArtifactRef{ID: "blake2b256-s", Size: 10, MediaType: "application/vnd.web-capture-archive.spec+json"},
			Payload: &capturer.ArtifactRef{ID: "blake2b256-p", Size: 20, MediaType: "text/plain; charset=utf-8"},
		}},
	}
}

// stubDeps wires a minimally-viable deps: all success, fixed time,
// nop history writer, real archive.WriteWithHistory so we actually
// exercise the atomic write path to tmpDir.
func stubDeps(policies []policy.Policy, out capturer.BatchOutput) Deps {
	return Deps{
		LoadPolicies: func(string) ([]policy.Policy, error) {
			return policies, nil
		},
		ResolveStory: func(id string) (policy.Story, error) {
			return policy.Story{
				Hash:      id,
				Permalink: "https://example.com/" + id,
				Title:     "stubbed",
			}, nil
		},
		RunCapturer: func(context.Context, capturer.BatchInput) (capturer.BatchOutput, error) {
			return out, nil
		},
		WriteArchive: archive.WriteWithHistory,
		TimeNow:      fixedTime,
		HistoryStore: nopHistory{},
		WriterCmd:    []string{"/bin/true"},
	}
}

func TestRun_happyPath_singlePolicySingleSubject(t *testing.T) {
	dir := t.TempDir()
	args := Args{
		StoryID:     "6327282:5d1cf5",
		PolicyPath:  filepath.Join(dir, "nebulous.toml"),
		ArchiveRoot: filepath.Join(dir, "archives"),
	}

	rep := run(context.Background(), args, stubDeps(singlePolicySingleCapture(), successBatchOutput()))

	if len(rep.Failed) != 0 {
		t.Errorf("unexpected failures: %+v", rep.Failed)
	}
	if len(rep.Written) != 1 {
		t.Fatalf("written: got %d, want 1", len(rep.Written))
	}
	if rep.Written[0].PolicyID != "p1" {
		t.Errorf("policy id: got %q", rep.Written[0].PolicyID)
	}
	if rep.Written[0].Subject != "story:6327282:5d1cf5" {
		t.Errorf("subject: got %q", rep.Written[0].Subject)
	}
	if rep.ExitCode() != 0 {
		t.Errorf("exit: got %d, want 0", rep.ExitCode())
	}

	// Verify the archive record actually landed on disk with the
	// expected shape.
	got, err := archive.Read(rep.Written[0].Path)
	if err != nil {
		t.Fatalf("read written record: %v", err)
	}
	if got.Subject != "story:6327282:5d1cf5" {
		t.Errorf("persisted subject: got %q", got.Subject)
	}
	if got.PolicyID != "p1" {
		t.Errorf("persisted policy_id: got %q", got.PolicyID)
	}
	if len(got.Captures) != 1 {
		t.Fatalf("persisted captures: got %d", len(got.Captures))
	}
	if got.Captures[0].Payload == nil {
		t.Error("persisted payload should be present")
	}
}

func TestRun_pathIsUnderByStoryForStorySubject(t *testing.T) {
	dir := t.TempDir()
	args := Args{
		StoryID:     "abc",
		PolicyPath:  filepath.Join(dir, "nebulous.toml"),
		ArchiveRoot: filepath.Join(dir, "archives"),
	}
	rep := run(context.Background(), args, stubDeps(singlePolicySingleCapture(), successBatchOutput()))
	if len(rep.Written) != 1 {
		t.Fatalf("written: %d", len(rep.Written))
	}
	wantPrefix := filepath.Join(dir, "archives", "by-story", "abc")
	if !strings.HasPrefix(rep.Written[0].Path, wantPrefix) {
		t.Errorf("path should be under %q, got %q", wantPrefix, rep.Written[0].Path)
	}
}

func TestRun_pathIsUnderByURLForURLSubject(t *testing.T) {
	dir := t.TempDir()
	args := Args{
		URL:         "https://example.com/canonical",
		PolicyPath:  filepath.Join(dir, "nebulous.toml"),
		ArchiveRoot: filepath.Join(dir, "archives"),
	}
	rep := run(context.Background(), args, stubDeps(singlePolicySingleCapture(), successBatchOutput()))
	if len(rep.Written) != 1 {
		t.Fatalf("written: %d", len(rep.Written))
	}
	if !strings.Contains(rep.Written[0].Path, filepath.Join("archives", "by-url")) {
		t.Errorf("path should contain archives/by-url/, got %q", rep.Written[0].Path)
	}
}

func TestRun_policyLoadFailureIsPreJobError(t *testing.T) {
	d := stubDeps(singlePolicySingleCapture(), successBatchOutput())
	d.LoadPolicies = func(string) ([]policy.Policy, error) {
		return nil, &stubErr{msg: "bad toml"}
	}

	rep := run(context.Background(), Args{}, d)
	if len(rep.Written) != 0 {
		t.Errorf("no writes expected, got %d", len(rep.Written))
	}
	if len(rep.Failed) != 1 {
		t.Fatalf("want 1 pre-job failure, got %+v", rep.Failed)
	}
	if rep.Failed[0].Kind != "policy-load-failed" {
		t.Errorf("kind: got %q", rep.Failed[0].Kind)
	}
}

type stubErr struct{ msg string }

func (e *stubErr) Error() string { return e.msg }

func TestRun_dualSubjectProducesTwoRecords(t *testing.T) {
	dir := t.TempDir()
	args := Args{
		StoryID:     "6327282:5d1cf5",
		URL:         "https://example.com/canonical",
		PolicyPath:  filepath.Join(dir, "nebulous.toml"),
		ArchiveRoot: filepath.Join(dir, "archives"),
	}
	rep := run(context.Background(), args, stubDeps(singlePolicySingleCapture(), successBatchOutput()))

	if len(rep.Failed) != 0 {
		t.Errorf("unexpected failures: %+v", rep.Failed)
	}
	if len(rep.Written) != 2 {
		t.Fatalf("written: got %d, want 2", len(rep.Written))
	}

	var storySeen, urlSeen bool
	for _, w := range rep.Written {
		if strings.HasPrefix(w.Subject, "story:") {
			storySeen = true
		}
		if strings.HasPrefix(w.Subject, "url:") {
			urlSeen = true
		}
	}
	if !storySeen {
		t.Error("expected a story:* subject in Written")
	}
	if !urlSeen {
		t.Error("expected a url:* subject in Written")
	}
}

// manyPolicies returns N policies each with one text capture,
// suitable for driving the circuit breaker across multiple jobs.
func manyPolicies(n int) []policy.Policy {
	out := make([]policy.Policy, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, policy.Policy{
			ID:        fmt.Sprintf("p%d", i),
			URL:       "{{.Story.Permalink}}",
			Isolation: "fresh",
			Captures: []policy.Capture{{
				Name:    "text",
				Format:  "text",
				Browser: "firefox",
			}},
		})
	}
	return out
}

func TestRun_threeConsecutiveFailuresBailOut(t *testing.T) {
	dir := t.TempDir()
	d := stubDeps(manyPolicies(5), capturer.BatchOutput{})
	d.RunCapturer = func(context.Context, capturer.BatchInput) (capturer.BatchOutput, error) {
		return capturer.BatchOutput{}, errors.New("simulated capturer failure")
	}

	args := Args{
		StoryID:     "abc",
		PolicyPath:  filepath.Join(dir, "nebulous.toml"),
		ArchiveRoot: filepath.Join(dir, "archives"),
	}
	rep := run(context.Background(), args, d)

	if !rep.BailedOut {
		t.Errorf("expected BailedOut=true")
	}
	if len(rep.Failed) != 3 {
		t.Errorf("expected exactly 3 failures before bail, got %d: %+v", len(rep.Failed), rep.Failed)
	}
	if rep.ExitCode() != 2 {
		t.Errorf("exit code: got %d, want 2", rep.ExitCode())
	}
}

func TestRun_interspersedSuccessesResetCounter(t *testing.T) {
	dir := t.TempDir()
	d := stubDeps(manyPolicies(5), successBatchOutput())

	// Pattern: fail, ok, fail, ok, fail. No 3-in-a-row; no bail.
	calls := 0
	d.RunCapturer = func(context.Context, capturer.BatchInput) (capturer.BatchOutput, error) {
		calls++
		if calls%2 == 1 {
			return capturer.BatchOutput{}, errors.New("simulated")
		}
		return successBatchOutput(), nil
	}

	args := Args{
		StoryID:     "abc",
		PolicyPath:  filepath.Join(dir, "nebulous.toml"),
		ArchiveRoot: filepath.Join(dir, "archives"),
	}
	rep := run(context.Background(), args, d)

	if rep.BailedOut {
		t.Errorf("should not bail out with interspersed successes")
	}
	if rep.ExitCode() != 1 {
		t.Errorf("exit code: got %d, want 1 (mixed ok+fail)", rep.ExitCode())
	}
	// Expect 3 failures (jobs 1, 3, 5) and 2 successes (jobs 2, 4).
	if len(rep.Failed) != 3 {
		t.Errorf("failed count: got %d, want 3", len(rep.Failed))
	}
	if len(rep.Written) != 2 {
		t.Errorf("written count: got %d, want 2", len(rep.Written))
	}
}
