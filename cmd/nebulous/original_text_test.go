package main

import (
	"slices"
	"testing"
	"time"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
)

type cachedAnswer struct {
	state     newsblur.OriginalTextState
	writtenAt time.Time
}

func statusOf(answers map[string]cachedAnswer) func(string) (newsblur.OriginalTextState, time.Time) {
	return func(hash string) (newsblur.OriginalTextState, time.Time) {
		a, ok := answers[hash]
		if !ok {
			return newsblur.OriginalTextMissing, time.Time{}
		}
		return a.state, a.writtenAt
	}
}

func TestPlanOriginalTextAsksForMissingAndDueFailedAnswers(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	interval := 7 * 24 * time.Hour
	answers := map[string]cachedAnswer{
		"final":         {newsblur.OriginalTextFinal, now.Add(-90 * 24 * time.Hour)},
		"failed-fresh":  {newsblur.OriginalTextRetryable, now.Add(-time.Hour)},
		"failed-due":    {newsblur.OriginalTextRetryable, now.Add(-8 * 24 * time.Hour)},
		"failed-oldest": {newsblur.OriginalTextRetryable, now.Add(-30 * 24 * time.Hour)},
	}
	hashes := []string{"never-asked", "final", "failed-fresh", "failed-due", "failed-oldest"}

	plan := planOriginalText(hashes, statusOf(answers), now, interval, 100)

	if want := []string{"never-asked"}; !slices.Equal(plan.missing, want) {
		t.Errorf("missing = %v, want %v", plan.missing, want)
	}
	// Oldest attempt first, so a cap defers the least stale ones.
	if want := []string{"failed-oldest", "failed-due"}; !slices.Equal(plan.retry, want) {
		t.Errorf("retry = %v, want %v (due answers only, oldest first)", plan.retry, want)
	}
	if want := []string{"never-asked", "failed-oldest", "failed-due"}; !slices.Equal(plan.todo(), want) {
		t.Errorf("todo = %v, want %v", plan.todo(), want)
	}
	if plan.final != 1 {
		t.Errorf("final = %d, want 1", plan.final)
	}
	if plan.failed != 3 || plan.failedDue != 2 {
		t.Errorf("failed = %d, failedDue = %d, want 3 and 2", plan.failed, plan.failedDue)
	}
}

func TestPlanOriginalTextCapsRetriesButNeverMissing(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	answers := map[string]cachedAnswer{
		"a": {newsblur.OriginalTextRetryable, now.Add(-10 * 24 * time.Hour)},
		"b": {newsblur.OriginalTextRetryable, now.Add(-20 * 24 * time.Hour)},
		"c": {newsblur.OriginalTextRetryable, now.Add(-30 * 24 * time.Hour)},
	}
	hashes := []string{"a", "b", "c", "new-1", "new-2"}

	plan := planOriginalText(hashes, statusOf(answers), now, 7*24*time.Hour, 2)

	if want := []string{"c", "b"}; !slices.Equal(plan.retry, want) {
		t.Errorf("retry = %v, want %v (the two oldest)", plan.retry, want)
	}
	if want := []string{"new-1", "new-2"}; !slices.Equal(plan.missing, want) {
		t.Errorf("missing = %v, want all of %v: the cap only limits retries", plan.missing, want)
	}
	if plan.failedDue != 3 {
		t.Errorf("failedDue = %d, want 3 (counted before the cap)", plan.failedDue)
	}
}

func TestPlanOriginalTextNonPositiveIntervalDisablesRetries(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	answers := map[string]cachedAnswer{
		"failed": {newsblur.OriginalTextRetryable, now.Add(-365 * 24 * time.Hour)},
	}

	plan := planOriginalText([]string{"failed", "new"}, statusOf(answers), now, 0, 100)

	if len(plan.retry) != 0 {
		t.Errorf("retry = %v, want none with retries disabled", plan.retry)
	}
	if want := []string{"new"}; !slices.Equal(plan.todo(), want) {
		t.Errorf("todo = %v, want %v", plan.todo(), want)
	}
	if plan.failed != 1 {
		t.Errorf("failed = %d, want 1 (still counted, just not retried)", plan.failed)
	}
}

func TestOriginalTextRetryIntervalFromEnvironment(t *testing.T) {
	t.Setenv("NEBULOUS_ORIGINAL_TEXT_RETRY_INTERVAL", "")
	if got := originalTextRetryInterval(); got != 168*time.Hour {
		t.Errorf("default = %v, want 168h", got)
	}
	t.Setenv("NEBULOUS_ORIGINAL_TEXT_RETRY_INTERVAL", "36h")
	if got := originalTextRetryInterval(); got != 36*time.Hour {
		t.Errorf("36h = %v", got)
	}
	t.Setenv("NEBULOUS_ORIGINAL_TEXT_RETRY_INTERVAL", "not-a-duration")
	if got := originalTextRetryInterval(); got != 168*time.Hour {
		t.Errorf("unparseable = %v, want the default", got)
	}
	t.Setenv("NEBULOUS_ORIGINAL_TEXT_RETRY_INTERVAL", "0")
	if got := originalTextRetryInterval(); got != 0 {
		t.Errorf("0 = %v, want 0 (retries off)", got)
	}
}
