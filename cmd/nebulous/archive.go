package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/term"

	"github.com/friedenberg/nebulous/internal/0/archive"
	"github.com/friedenberg/nebulous/internal/0/madder"
	"github.com/friedenberg/nebulous/internal/alfa/capturer"
	"github.com/friedenberg/nebulous/internal/alfa/orchestrator"
	"github.com/friedenberg/nebulous/internal/alfa/policy"
)

// archiveMain is the `nebulous archive` subcommand entry point.
// Parses flags, wires a production orchestrator.Deps, calls Run,
// emits the Report in the TTY-appropriate format, returns the
// orchestrator's exit code.
func archiveMain(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("archive", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		storyID     = fs.String("story", "", "story id (e.g. 6327282:5d1cf5); mutually valid with --url")
		url         = fs.String("url", "", "url to capture; mutually valid with --story")
		policyPath  = fs.String("policy", defaultPolicyPath(), "path to nebulous.toml")
		archiveRoot = fs.String("archive-root", defaultArchiveRoot(), "directory for archive records")
	)

	if err := fs.Parse(args); err != nil {
		// ContinueOnError already printed the error.
		return 3
	}
	if *storyID == "" && *url == "" {
		fmt.Fprintln(os.Stderr, "archive: at least one of --story or --url is required")
		return 3
	}

	deps, err := newArchiveDeps(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archive: %v\n", err)
		return 3
	}

	report := orchestrator.Run(ctx, orchestrator.Args{
		StoryID:     *storyID,
		URL:         *url,
		PolicyPath:  *policyPath,
		ArchiveRoot: *archiveRoot,
	}, deps)

	tty := term.IsTerminal(int(os.Stdout.Fd()))
	if err := orchestrator.EmitReport(os.Stdout, report, tty); err != nil {
		fmt.Fprintf(os.Stderr, "archive: emit report: %v\n", err)
		// Prefer the orchestrator's exit code over the emit error —
		// the archive work either succeeded or failed independently
		// of how we rendered the report.
	}
	return report.ExitCode()
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
