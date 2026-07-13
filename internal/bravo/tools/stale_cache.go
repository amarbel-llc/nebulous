package tools

import (
	"os"
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

	mu             sync.Mutex
	snapshot       *T
	lastMtimeNanos int64
	lastCheckedAt  time.Time
}

func newStaleCache[T any](manifestPath string) staleCache[T] {
	return staleCache[T]{manifestPath: manifestPath}
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

	if c.snapshot != nil && time.Since(c.lastCheckedAt) < staleCheckDebounce {
		return c.snapshot, nil
	}
	c.lastCheckedAt = time.Now()

	mtime, haveMtime := c.statMtime()
	if c.snapshot != nil && haveMtime && mtime == c.lastMtimeNanos {
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
		c.lastMtimeNanos = mtime
	}
	c.snapshot = fresh
	return fresh, nil
}

func (c *staleCache[T]) statMtime() (int64, bool) {
	if c.manifestPath == "" {
		return 0, false
	}
	fi, err := os.Stat(c.manifestPath)
	if err != nil {
		return 0, false
	}
	return fi.ModTime().UnixNano(), true
}
