package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/friedenberg/nebulous/internal/0/archive"
	"github.com/friedenberg/nebulous/internal/0/madder"
	"github.com/friedenberg/nebulous/internal/alfa/capturer"
	"github.com/friedenberg/nebulous/internal/alfa/orchestrator"
	"github.com/friedenberg/nebulous/internal/alfa/policy"
)

// archiveMain is the `nebulous archive-capture` subcommand entry
// point. Parses flags + positional targets, wires a production
// orchestrator.Deps, calls Run, emits the Report in the TTY-
// appropriate format, returns the orchestrator's exit code.
//
// Targets are positional args: story IDs (shape `<feed>:<hash>`) or
// URLs (contain `://`). A single `-` positional reads one target per
// line from stdin; mixing `-` with other positionals is rejected.
func archiveMain(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("archive-capture", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		policyPath  = fs.String("policy", defaultPolicyPath(), "path to nebulous.toml")
		archiveRoot = fs.String("archive-root", defaultArchiveRoot(), "directory for archive records")
		jobs        = fs.Int("jobs", 1, "worker-pool size for concurrent captures (1 = serial)")
		format      = fs.String("format", "auto", "output format: tap | json | auto (auto = tap on TTY, json otherwise)")
		ttl         = fs.Duration("ttl", 0, "skip targets whose prior fully-successful capture is newer than this duration (Go duration, e.g. 24h; 0 disables)")
	)

	if err := fs.Parse(args); err != nil {
		return 3
	}

	if *ttl < 0 {
		fmt.Fprintf(os.Stderr, "archive-capture: --ttl must not be negative, got %s\n", *ttl)
		return 3
	}

	effectiveFormat, err := resolveCaptureFormat(*format, term.IsTerminal(int(os.Stdout.Fd())))
	if err != nil {
		fmt.Fprintf(os.Stderr, "archive-capture: %v\n", err)
		return 3
	}

	positional := fs.Args()
	storyIDs, urls, err := resolveTargets(positional, os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archive-capture: %v\n", err)
		return 3
	}

	deps, err := newArchiveDeps(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archive-capture: %v\n", err)
		return 3
	}

	// TAP format streams live — the orchestrator emits plan + test
	// points to os.Stdout as jobs complete. JSON format stays a
	// single atomic end-of-run document.
	runArgs := orchestrator.Args{
		StoryIDs:    storyIDs,
		URLs:        urls,
		PolicyPath:  *policyPath,
		ArchiveRoot: *archiveRoot,
		Jobs:        *jobs,
		TTL:         *ttl,
	}
	if effectiveFormat == "tap" {
		runArgs.StreamTAP = os.Stdout
	}

	report := orchestrator.Run(ctx, runArgs, deps)

	if effectiveFormat == "json" {
		if err := orchestrator.WriteJSONReport(os.Stdout, report); err != nil {
			fmt.Fprintf(os.Stderr, "archive-capture: emit report: %v\n", err)
			// Prefer the orchestrator's exit code over the emit error —
			// the archive work either succeeded or failed independently
			// of how we rendered the report.
		}
	}
	return report.ExitCode()
}

// resolveCaptureFormat turns the user-supplied --format value into a
// concrete choice of "tap" or "json" for archive-capture. The
// special value "auto" (the default) picks TAP on an interactive
// TTY and JSON otherwise. Unlike archive-list's resolveFormat,
// unknown values are rejected with an error rather than passed
// through — archive-capture has exactly two valid output formats.
func resolveCaptureFormat(raw string, tty bool) (string, error) {
	switch raw {
	case "auto":
		if tty {
			return "tap", nil
		}
		return "json", nil
	case "tap", "json":
		return raw, nil
	default:
		return "", fmt.Errorf("invalid --format %q: want one of tap, json, auto", raw)
	}
}

// resolveTargets parses positional args into classified story IDs
// and URLs. A single `-` positional reads one target per line from
// stdin; mixing `-` with other positionals is rejected. Returns
// (nil, nil, err) for an empty target list — callers treat "no
// targets supplied" as an error, but "stdin produced zero non-blank
// lines" as a successful no-op.
func resolveTargets(positional []string, stdin io.Reader) (storyIDs, urls []string, err error) {
	if len(positional) == 0 {
		return nil, nil, fmt.Errorf("no targets supplied (pass story IDs or URLs as positional args, or `-` to read from stdin)")
	}

	var raw []string
	stdinRequested := false
	for _, p := range positional {
		if p == "-" {
			if len(positional) > 1 {
				return nil, nil, fmt.Errorf("`-` (stdin) cannot be mixed with other positional targets")
			}
			stdinRequested = true
			break
		}
		raw = append(raw, p)
	}

	if stdinRequested {
		sc := bufio.NewScanner(stdin)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			raw = append(raw, line)
		}
		if err := sc.Err(); err != nil {
			return nil, nil, fmt.Errorf("read stdin: %w", err)
		}
		// Empty stdin is a successful no-op: return empty slices and
		// let the orchestrator produce an empty report.
		if len(raw) == 0 {
			return nil, nil, nil
		}
	}

	for _, t := range raw {
		switch classifyTarget(t) {
		case targetURL:
			urls = append(urls, t)
		case targetStoryID:
			storyIDs = append(storyIDs, t)
		default:
			return nil, nil, fmt.Errorf("not a story ID or URL: %q", t)
		}
	}
	return storyIDs, urls, nil
}

type targetKind int

const (
	targetInvalid targetKind = iota
	targetStoryID
	targetURL
)

// classifyTarget decides whether t is a URL, a story ID, or
// neither. URLs contain `://` (any scheme). Story IDs match the
// NewsBlur shape `<feed>:<hash>`: one colon, nonempty on both
// sides, no whitespace.
func classifyTarget(t string) targetKind {
	if strings.Contains(t, "://") {
		return targetURL
	}
	if strings.ContainsAny(t, " \t") {
		return targetInvalid
	}
	colon := strings.Index(t, ":")
	if colon <= 0 || colon == len(t)-1 {
		return targetInvalid
	}
	return targetStoryID
}

// newArchiveDeps builds an orchestrator.Deps wired with real
// implementations: a cache-only newsblur client for story → URL
// resolution, a madder.Store as the history sink, the flake-pinned
// madder binary as the writer command, and archive.WriteWithHistory.
func newArchiveDeps(ctx context.Context) (orchestrator.Deps, error) {
	store := madder.NewStore(ctx)
	if err := store.Init(); err != nil {
		return orchestrator.Deps{}, fmt.Errorf("madder init: %w", err)
	}

	client, err := buildCacheOnlyClient(ctx)
	if err != nil {
		return orchestrator.Deps{}, fmt.Errorf("newsblur cache: %w", err)
	}

	return orchestrator.Deps{
		LoadPolicies: policy.LoadAll,
		ResolveStory: func(id string) (policy.Story, error) {
			raw, ok := client.CachedStarredStory(id)
			if !ok {
				return policy.Story{}, fmt.Errorf("story %q not in cache; run `nebulous fetch` first", id)
			}
			var sr struct {
				Hash      string `json:"story_hash"`
				Permalink string `json:"story_permalink"`
				Title     string `json:"story_title"`
			}
			if err := json.Unmarshal(raw, &sr); err != nil {
				return policy.Story{}, fmt.Errorf("decode story %q: %w", id, err)
			}
			if sr.Permalink == "" {
				return policy.Story{}, fmt.Errorf("story %q has no permalink in cache", id)
			}
			return policy.Story{
				Hash:      sr.Hash,
				Permalink: sr.Permalink,
				Title:     sr.Title,
			}, nil
		},
		RunCapturer:  capturer.Run,
		WriteArchive: archive.WriteWithHistory,
		TimeNow:      time.Now,
		HistoryStore: madderWriter{store: store},
		WriterCmd: []string{
			// Madder syntax: `madder write -format=json <store-id> -`.
			// -format=json is a subcommand flag (single dash) and must
			// come after `write`; the positional `<store-id>` switches
			// the active store for this write; `-` reads from stdin.
			//
			// madder.Bin is absolute when ldflags-injected (flake build
			// and `just build-go`); defaults to "madder" otherwise.
			madder.Bin, "write", "-format=json", "nebulous", "-",
		},
	}, nil
}

// madderWriter adapts *madder.Store to archive.Writer.
//
// madder.Store.Write takes no context (its lifetime is tied to the
// ctx passed to madder.NewStore) and returns a markl-id directly
// — exactly what archive.Writer wants. This adapter just discards
// the per-call context, which is safe given the store's own
// lifetime bounding.
type madderWriter struct {
	store *madder.Store
}

func (w madderWriter) Write(_ context.Context, src io.Reader) (string, error) {
	return w.store.Write(src)
}

// defaultPolicyPath returns $XDG_CONFIG_HOME/nebulous/nebulous.toml
// with $HOME/.config fallback.
func defaultPolicyPath() string {
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		if home, err := os.UserHomeDir(); err == nil {
			xdg = filepath.Join(home, ".config")
		}
	}
	return filepath.Join(xdg, "nebulous", "nebulous.toml")
}

// defaultArchiveRoot returns $XDG_DATA_HOME/nebulous/archives with
// $HOME/.local/share fallback.
func defaultArchiveRoot() string {
	xdg := os.Getenv("XDG_DATA_HOME")
	if xdg == "" {
		if home, err := os.UserHomeDir(); err == nil {
			xdg = filepath.Join(home, ".local", "share")
		}
	}
	return filepath.Join(xdg, "nebulous", "archives")
}
