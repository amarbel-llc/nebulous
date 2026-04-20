// Package archivelist walks an archive root, decodes each record,
// and projects a lightweight Summary per (subject, policy) pair.
//
// The disk layout walked is the one the orchestrator actually writes
// (see internal/alfa/orchestrator.recordPath):
//
//	<root>/by-story/<id>/<policyID>.json
//	<root>/by-url/<sha>/<policyID>.json
//
// archive.DefaultPath uses a different layout (<root>/archives/<subject>)
// and is intentionally unused here.
//
// Decode failures are surfaced via the Warn callback and skipped;
// they do not abort the walk. Callers that need strict behavior can
// make Warn panic or fail-loudly.
package archivelist

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/friedenberg/nebulous/internal/0/archive"
)

// Summary is the per-record projection emitted by Walk. It is the
// shape rendered in both JSONL and table output.
type Summary struct {
	Subject       string `json:"subject"`
	PolicyID      string `json:"policy_id"`
	URL           string `json:"url"`
	CapturedAt    string `json:"captured_at"`
	CapturesOK    int    `json:"captures_ok"`
	CapturesTotal int    `json:"captures_total"`
	Path          string `json:"path"`
}

// Options configures Walk.
type Options struct {
	// Root is the archive root — the directory containing by-story/
	// and by-url/ subtrees.
	Root string

	// SubjectPrefix filters emitted summaries to those whose Subject
	// starts with this string. Empty matches everything.
	SubjectPrefix string

	// Warn is called with (format, args...) for each decode failure
	// encountered during the walk. Nil routes warnings to stderr.
	Warn func(format string, args ...any)
}

// Walk walks opts.Root, decoding every *.json under by-story/ and
// by-url/ via archive.Read. Results are sorted by CapturedAt
// descending (newest first) — RFC 3339 millisecond UTC is
// lexicographic-equivalent, so string reverse-sort is correct.
//
// Returns an empty slice when opts.Root does not exist or contains
// no record files. Returns an error only for unrecoverable walk
// failures (permissions, IO); per-file decode errors are warned and
// skipped.
func Walk(opts Options) ([]Summary, error) {
	warn := opts.Warn
	if warn == nil {
		warn = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "archive-list: "+format+"\n", args...)
		}
	}

	var out []Summary
	for _, sub := range []string{"by-story", "by-url"} {
		dir := filepath.Join(opts.Root, sub)
		entries, err := walkDir(dir, warn)
		if err != nil {
			return nil, err
		}
		out = append(out, entries...)
	}

	if opts.SubjectPrefix != "" {
		filtered := out[:0]
		for _, s := range out {
			if strings.HasPrefix(s.Subject, opts.SubjectPrefix) {
				filtered = append(filtered, s)
			}
		}
		out = filtered
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].CapturedAt != out[j].CapturedAt {
			return out[i].CapturedAt > out[j].CapturedAt
		}
		// Stable tie-break by subject for deterministic output when
		// two captures share a millisecond.
		return out[i].Subject < out[j].Subject
	})
	return out, nil
}

// walkDir recursively walks dir looking for *.json files, decoding
// each. Missing directories are not an error (fresh archive root
// may have only by-story/ or only by-url/).
func walkDir(dir string, warn func(string, ...any)) ([]Summary, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", dir)
	}

	var out []Summary
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			warn("walk %s: %v", path, err)
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		rec, err := archive.Read(path)
		if err != nil {
			warn("decode %s: %v", path, err)
			return nil
		}
		out = append(out, summarize(rec, path))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", dir, err)
	}
	return out, nil
}

// summarize projects rec to a Summary. A capture is "ok" when it
// has artifact refs (no Error field); failed captures contribute to
// Total but not OK.
func summarize(rec *archive.Record, path string) Summary {
	ok := 0
	for _, c := range rec.Captures {
		if c.Error == nil {
			ok++
		}
	}
	return Summary{
		Subject:       rec.Subject,
		PolicyID:      rec.PolicyID,
		URL:           rec.URL,
		CapturedAt:    rec.CapturedAt,
		CapturesOK:    ok,
		CapturesTotal: len(rec.Captures),
		Path:          path,
	}
}
