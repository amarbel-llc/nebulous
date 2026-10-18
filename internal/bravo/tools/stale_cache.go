package tools

import (
	"os"
	"strconv"
	"sync"
	"time"
)

// staleCheckDebounce bounds how often storyStore/feedIndex stat the
// manifest file to check for another process's writes (they used to
// build once via sync.Once and never rebuild, so a concurrently-running
// `nebulous fetch` was invisible until the server itself restarted).
// Debounced rather than checked on every call for the same reason
// internal/0/manifest's own staleCheckDebounce is: a full corpus scan is
// expensive, and a busy fetch run bumps the manifest's mtime on every
// single write.
const staleCheckDebounce = 1 * time.Second

// staleCache holds an immutable snapshot of type T behind a debounced
// manifest-mtime staleness check, shared by storyStore and feedIndex so
// the stat-compare-rebuild dance lives in one place instead of two.
type staleCache[T any] struct {
	manifestPath string
	// watchPaths are further files whose (mtime, size) also invalidate the
	// snapshot — the on-disk word index, which fetch swaps in by rename
	// after the manifest has already changed. A missing watched file is a
	// valid state; its appearance counts as a change.
	watchPaths []string
	// localWrites, when set, counts this process's own writes to the
	// manifest. A changed count bypasses the debounce and forces a rebuild
	// (so a read right after a local write sees it: nebulous#56) even if the
	// file's mtime resolution did not move.
	localWrites func() uint64

	mu            sync.Mutex
	snapshot      *T
	lastStamp     string
	lastWrites    uint64
	lastCheckedAt time.Time
}

func newStaleCache[T any](manifestPath string, localWrites func() uint64, watchPaths ...string) staleCache[T] {
	return staleCache[T]{manifestPath: manifestPath, localWrites: localWrites, watchPaths: watchPaths}
}

// current returns the up-to-date snapshot, calling build to produce a
// fresh one when the manifest file's mtime has changed since the last
// check. The staleness check itself is debounced (staleCheckDebounce) so
// a burst of reads only stats the manifest file at most once per window;
// the mutex serializes concurrent callers onto one rebuild rather than
// racing independent ones (the coalescing a sync.Once used to give for
// free). On a transient build error, the last good snapshot keeps being
// served rather than the failure being cached forever.
func (c *staleCache[T]) current(build func() (*T, error)) (*T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var writes uint64
	if c.localWrites != nil {
		writes = c.localWrites()
	}
	if c.snapshot != nil && writes == c.lastWrites && time.Since(c.lastCheckedAt) < staleCheckDebounce {
		return c.snapshot, nil
	}
	c.lastCheckedAt = time.Now()

	stamp, haveMtime := c.stat()
	if c.snapshot != nil && haveMtime && stamp == c.lastStamp && writes == c.lastWrites {
		return c.snapshot, nil
	}

	fresh, buildErr := build()
	if buildErr != nil {
		if c.snapshot != nil {
			return c.snapshot, nil
		}
		return nil, buildErr
	}
	if haveMtime {
		c.lastStamp = stamp
	}
	// writes was read before build, so a write landing during the build is
	// seen as new on the next call rather than lost.
	c.lastWrites = writes
	c.snapshot = fresh
	return fresh, nil
}

// stat returns a change stamp over the manifest and every watched path; ok
// is false when the manifest itself cannot be stat'd (no staleness signal).
func (c *staleCache[T]) stat() (stamp string, ok bool) {
	if c.manifestPath == "" {
		return "", false
	}
	fi, err := os.Stat(c.manifestPath)
	if err != nil {
		return "", false
	}
	stamp = strconv.FormatInt(fi.ModTime().UnixNano(), 10)
	for _, p := range c.watchPaths {
		wi, err := os.Stat(p)
		if err != nil {
			stamp += "|-"
			continue
		}
		stamp += "|" + strconv.FormatInt(wi.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(wi.Size(), 10)
	}
	return stamp, true
}
