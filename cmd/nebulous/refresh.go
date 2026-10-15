package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
	"code.linenisgreat.com/nebulous/internal/bravo/tools"
)

// The refresh phase keeps cached starred stories (their tags and read state)
// from going permanently stale. `fetch` otherwise downloads a starred story
// exactly once, so a tag edited in the NewsBlur apps never reached the cache
// (tag-mutation review, finding 2).
//
// Three mechanisms, cheapest first:
//   - every run: one request returns every starred hash with NewsBlur's
//     "last updated" stamp; stories whose stamp differs from the one
//     recorded when we downloaded them are re-downloaded. The stamp moves on
//     every save of the starred story (a tag edit, a tag rename);
//   - every run: tags the cache carries but NewsBlur's own tag counts no
//     longer list (a tag deleted in the apps does not move the stamp) get
//     their cached holders re-downloaded, so the answer comes from NewsBlur
//     and not from inference;
//   - on a long interval (read state has no cheap change signal: NewsBlur
//     computes it per request from a separate set): every cached story is
//     re-downloaded. The first run has no recorded stamps and is such a run.

const (
	defaultRefreshFullInterval = 7 * 24 * time.Hour
	// refreshChunk is how many stories are downloaded between saves of the
	// recorded stamps, so an interrupted full re-sync resumes where it
	// stopped instead of starting over.
	refreshChunk = 500
)

// refreshFullInterval is how often the full re-sync (read state) runs,
// parsed from NEBULOUS_REFRESH_FULL_INTERVAL.
func refreshFullInterval() time.Duration {
	if raw := os.Getenv("NEBULOUS_REFRESH_FULL_INTERVAL"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			return d
		}
	}
	return defaultRefreshFullInterval
}

// refreshPlan is the stories one run will re-download.
type refreshPlan struct {
	ToFetch []string
	// Full reports that every cached story is being re-synced, which also
	// refreshes read state; the run records FullSyncAt when it completes.
	Full bool
}

// planRefresh decides what to re-download. live is NewsBlur's current stamps.
// justFetched holds stories this same run just downloaded: they are current,
// so they are recorded but not downloaded twice. Hashes are returned sorted,
// so a resumed run proceeds in the same order.
func planRefresh(state newsblur.StarredSyncState, haveState bool, live map[string]string, justFetched map[string]bool, forceFull bool, fullEvery time.Duration, now time.Time) refreshPlan {
	full := forceFull || !haveState || state.FullSyncAt.IsZero() ||
		(fullEvery > 0 && now.Sub(state.FullSyncAt) >= fullEvery)

	plan := refreshPlan{Full: full}
	for hash, stamp := range live {
		if justFetched[hash] {
			continue
		}
		if full || state.Timestamps[hash] != stamp {
			plan.ToFetch = append(plan.ToFetch, hash)
		}
	}
	slices.Sort(plan.ToFetch)
	return plan
}

// deletedTagHolders finds the tags the cache still carries that NewsBlur's
// live counts no longer list, with the cached stories holding each. Empty and
// untrimmed tags are skipped: NewsBlur's counts may well not list those
// even though the stories really carry them, and a wrong "deleted" verdict
// would only cause needless downloads (the re-download restores whatever
// NewsBlur says), but would never converge for them.
func deletedTagHolders(cached map[string][]string, live map[string]int) map[string][]string {
	holders := map[string][]string{}
	for hash, tags := range cached {
		for _, tag := range tags {
			if tag == "" || tag != strings.TrimSpace(tag) {
				continue
			}
			if _, ok := live[tag]; !ok {
				holders[tag] = append(holders[tag], hash)
			}
		}
	}
	for _, hashes := range holders {
		slices.Sort(hashes)
	}
	return holders
}

// refreshDeps are the phase's effects, as functions so the logic is testable
// without a live NewsBlur.
type refreshDeps struct {
	LiveStamps   func(ctx context.Context) (map[string]string, error)
	LiveTagCount func(ctx context.Context) (map[string]int, error)
	LoadState    func() (newsblur.StarredSyncState, bool)
	SaveState    func(newsblur.StarredSyncState) error
	// Fetch downloads the stories and overwrites their cached copies.
	Fetch func(ctx context.Context, hashes []string) (int, error)
	// CachedTags returns each cached starred story's user tags.
	CachedTags func() (map[string][]string, error)
	Now        func() time.Time
}

type refreshOpts struct {
	ForceFull   bool
	FullEvery   time.Duration
	JustFetched map[string]bool
}

// runRefresh runs the whole phase and returns how many stories it
// re-downloaded (the caller rebuilds the word index when it is not zero). A
// failure partway keeps what was already saved: the next run continues from
// the recorded stamps.
func runRefresh(ctx context.Context, d refreshDeps, o refreshOpts) (redownloaded int, err error) {
	live, err := d.LiveStamps(ctx)
	if err != nil {
		return 0, fmt.Errorf("reading NewsBlur's story timestamps: %w", err)
	}
	state, have := d.LoadState()
	if !have {
		state = newsblur.StarredSyncState{Timestamps: map[string]string{}}
	}

	plan := planRefresh(state, have, live, o.JustFetched, o.ForceFull, o.FullEvery, d.Now())
	kind := "incremental"
	if plan.Full {
		kind = "FULL re-sync"
	}
	log.Printf("[refresh] %s: %d starred stories, %d to re-download", kind, len(live), len(plan.ToFetch))

	// Stories just downloaded are current; unstarred ones leave the record.
	for hash := range o.JustFetched {
		if stamp, ok := live[hash]; ok {
			state.Timestamps[hash] = stamp
		}
	}
	for hash := range state.Timestamps {
		if _, ok := live[hash]; !ok {
			delete(state.Timestamps, hash)
		}
	}

	for start := 0; start < len(plan.ToFetch); start += refreshChunk {
		if err := ctx.Err(); err != nil {
			_ = d.SaveState(state)
			return redownloaded, err
		}
		chunk := plan.ToFetch[start:min(start+refreshChunk, len(plan.ToFetch))]
		if _, err := d.Fetch(ctx, chunk); err != nil {
			_ = d.SaveState(state)
			return redownloaded, fmt.Errorf("re-downloading stories %d-%d of %d: %w", start, start+len(chunk), len(plan.ToFetch), err)
		}
		redownloaded += len(chunk)
		for _, hash := range chunk {
			state.Timestamps[hash] = live[hash]
		}
		if err := d.SaveState(state); err != nil {
			return redownloaded, fmt.Errorf("saving the sync state: %w", err)
		}
		log.Printf("[refresh] re-downloaded %d/%d", start+len(chunk), len(plan.ToFetch))
	}
	if plan.Full {
		state.FullSyncAt = d.Now()
	}
	if err := d.SaveState(state); err != nil {
		return redownloaded, fmt.Errorf("saving the sync state: %w", err)
	}

	// Tags deleted in the apps do not move the stamp: compare against
	// NewsBlur's own tag counts and re-download the holders of any tag it no
	// longer lists.
	counts, err := d.LiveTagCount(ctx)
	if err != nil {
		return redownloaded, fmt.Errorf("reading NewsBlur's tag counts: %w", err)
	}
	cached, err := d.CachedTags()
	if err != nil {
		return redownloaded, fmt.Errorf("reading the cached tags: %w", err)
	}
	stale := deletedTagHolders(cached, counts)
	if len(stale) == 0 {
		return redownloaded, nil
	}
	var again []string
	for tag, hashes := range stale {
		log.Printf("[refresh] tag %q is no longer listed by NewsBlur: re-downloading its %d cached stories", tag, len(hashes))
		for _, h := range hashes {
			if !slices.Contains(again, h) {
				again = append(again, h)
			}
		}
	}
	slices.Sort(again)
	for start := 0; start < len(again); start += refreshChunk {
		chunk := again[start:min(start+refreshChunk, len(again))]
		if _, err := d.Fetch(ctx, chunk); err != nil {
			return redownloaded, fmt.Errorf("re-downloading stories of deleted tags: %w", err)
		}
		redownloaded += len(chunk)
	}
	return redownloaded, nil
}

// runFetchRefreshPhase wires runRefresh to a live client. Like the capture
// and index phases it never fails the fetch run: it logs and continues, and
// the next run picks up from the recorded stamps. It reports whether any
// cached story was re-downloaded.
func runFetchRefreshPhase(ctx context.Context, client *newsblur.Client, justFetched map[string]bool, skip, forceFull bool) (changed bool) {
	if skip {
		log.Println("[refresh] skipped (-no-refresh)")
		return false
	}
	deps := refreshDeps{
		LiveStamps:   client.StarredHashTimestamps,
		LiveTagCount: client.LiveStarredTagCounts,
		LoadState:    client.CachedStarredSyncState,
		SaveState:    client.PutCachedStarredSyncState,
		Fetch: func(ctx context.Context, hashes []string) (int, error) {
			return fetchStarredStoriesByHash(ctx, client, hashes)
		},
		CachedTags: func() (map[string][]string, error) {
			stories, err := tools.NewReadIndex(client).Stories()
			if err != nil {
				return nil, err
			}
			out := make(map[string][]string, len(stories))
			for _, s := range stories {
				out[s.Hash] = s.UserTags
			}
			return out, nil
		},
		Now: time.Now,
	}
	opts := refreshOpts{ForceFull: forceFull, FullEvery: refreshFullInterval(), JustFetched: justFetched}
	n, err := runRefresh(ctx, deps, opts)
	if err != nil {
		log.Printf("[refresh] error: %v (continuing)", err)
	}
	return n > 0
}
