package main

import (
	"os"
	"slices"
	"time"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
)

const (
	// defaultOriginalTextRetryInterval is how long a cached "extraction
	// failed" answer is kept before fetch asks NewsBlur again. The failure is
	// often transient on NewsBlur's side, but a page that can never be
	// extracted (a paywall, a dead link) fails every time, so the interval is
	// long: such a story costs one request per interval, not one per run.
	defaultOriginalTextRetryInterval = 168 * time.Hour

	// maxOriginalTextRetriesPerRun bounds the retries one fetch makes, so the
	// first run after upgrading (when every old failed answer is due at once)
	// is not one long rate-limited crawl. The rest are retried by later runs,
	// oldest attempt first. Stories never asked for are not capped.
	maxOriginalTextRetriesPerRun = 500
)

// originalTextRetryInterval is the retry gate, parsed from
// NEBULOUS_ORIGINAL_TEXT_RETRY_INTERVAL. A non-positive value turns retries
// off; an unset or unparseable one falls back to the default.
func originalTextRetryInterval() time.Duration {
	if raw := os.Getenv("NEBULOUS_ORIGINAL_TEXT_RETRY_INTERVAL"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			return d
		}
	}
	return defaultOriginalTextRetryInterval
}

// originalTextPlan is what fetch's original-text phase will request.
type originalTextPlan struct {
	missing []string // nothing usable cached
	retry   []string // failed answers due for another attempt, oldest first, capped

	final     int // answers kept as they are
	failed    int // failed answers cached, due or not
	failedDue int // failed answers past the interval, before the cap
}

func (p originalTextPlan) todo() []string {
	return append(slices.Clone(p.missing), p.retry...)
}

// planOriginalText sorts hashes by what is cached for each. An answer that
// holds no text because extraction failed is asked for again once it is older
// than interval (a non-positive interval never retries), at most maxRetries
// per run, oldest attempt first. The cache's own "has an entry" test cannot
// make this call: it would keep a failed answer forever.
func planOriginalText(
	hashes []string,
	status func(string) (newsblur.OriginalTextState, time.Time),
	now time.Time,
	interval time.Duration,
	maxRetries int,
) originalTextPlan {
	type attempt struct {
		hash      string
		writtenAt time.Time
	}
	var plan originalTextPlan
	var due []attempt

	for _, hash := range hashes {
		state, writtenAt := status(hash)
		switch state {
		case newsblur.OriginalTextMissing:
			plan.missing = append(plan.missing, hash)
		case newsblur.OriginalTextFinal:
			plan.final++
		case newsblur.OriginalTextRetryable:
			plan.failed++
			if interval > 0 && now.Sub(writtenAt) >= interval {
				due = append(due, attempt{hash, writtenAt})
			}
		}
	}

	plan.failedDue = len(due)
	slices.SortStableFunc(due, func(a, b attempt) int { return a.writtenAt.Compare(b.writtenAt) })
	if len(due) > maxRetries {
		due = due[:max(maxRetries, 0)]
	}
	for _, a := range due {
		plan.retry = append(plan.retry, a.hash)
	}
	return plan
}
