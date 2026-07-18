package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/server"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/transport"
	"github.com/friedenberg/nebulous/internal/0/madder"
	"github.com/friedenberg/nebulous/internal/alfa/newsblur"
	"github.com/friedenberg/nebulous/internal/bravo/tools"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "nebulous — a NewsBlur MCP server\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  nebulous serve mcp            Start MCP server over stdio\n")
		fmt.Fprintf(os.Stderr, "  nebulous generate-plugin      Generate plugin.json\n")
		fmt.Fprintf(os.Stderr, "  nebulous hook                 Handle purse-first hooks\n")
		fmt.Fprintf(os.Stderr, "  nebulous install-mcp          Install MCP server config\n")
		fmt.Fprintf(os.Stderr, "  nebulous fetch [-formats f1,f2] [-store id] [-no-capture]\n")
		fmt.Fprintf(os.Stderr, "                                Cache feeds, starred stories, and original text; also runs\n")
		fmt.Fprintf(os.Stderr, "                                the gap-filling capture phase when its interval has elapsed\n")
		fmt.Fprintf(os.Stderr, "                                (NEBULOUS_CAPTURE_INTERVAL, default 6h; -no-capture skips it)\n")
		fmt.Fprintf(os.Stderr, "  nebulous capture [-formats f1,f2] [-store id] [-backfill]\n")
		fmt.Fprintf(os.Stderr, "                                Run the capture phase standalone, ignoring the interval gate\n")
		fmt.Fprintf(os.Stderr, "  nebulous corpus-list [-limit N] List starred story keys (for maneater)\n")
		fmt.Fprintf(os.Stderr, "  nebulous corpus-read <key>    Extract story text by key (for maneater)\n\n")
		fmt.Fprintf(os.Stderr, "Environment:\n")
		fmt.Fprintf(os.Stderr, "  NEWSBLUR_TOKEN            NewsBlur session cookie (required for `serve mcp` and `fetch`)\n")
		fmt.Fprintf(os.Stderr, "  XDG_DATA_HOME              honored when resolving the nebulous manifest path ($XDG_DATA_HOME/nebulous/manifest.json)\n")
		fmt.Fprintf(os.Stderr, "  NEBULOUS_CAPTURE_INTERVAL  how often `fetch`'s folded-in capture phase actually runs (default 6h)\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	// generate-plugin and hook don't need a live NewsBlur connection.
	if flag.NArg() >= 1 && flag.Arg(0) == "generate-plugin" {
		app, _ := tools.RegisterAll(nil)
		if err := app.HandleGeneratePlugin(flag.Args()[1:], os.Stdout); err != nil {
			log.Fatalf("generating plugin: %v", err)
		}
		return
	}

	if flag.NArg() >= 1 && flag.Arg(0) == "hook" {
		app, _ := tools.RegisterAll(nil)
		if err := app.HandleHook(os.Stdin, os.Stdout); err != nil {
			log.Fatalf("handling hook: %v", err)
		}
		return
	}

	if flag.NArg() >= 1 && flag.Arg(0) == "install-mcp" {
		app, _ := tools.RegisterAll(nil)
		if err := app.InstallMCP(); err != nil {
			log.Fatalf("installing MCP: %v", err)
		}
		return
	}

	if flag.NArg() >= 1 && flag.Arg(0) == "corpus-list" {
		limit := 0
		corpusFlags := flag.NewFlagSet("corpus-list", flag.ExitOnError)
		corpusFlags.IntVar(&limit, "limit", 0, "maximum number of keys to emit (0 = all)")
		corpusFlags.Parse(flag.Args()[1:])

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		client, err := buildCacheOnlyClient(ctx)
		if err != nil {
			log.Fatalf("corpus-list: %v", err)
		}
		if err := tools.CorpusList(client, os.Stdout, limit); err != nil {
			log.Fatalf("corpus-list: %v", err)
		}
		return
	}

	if flag.NArg() >= 1 && flag.Arg(0) == "corpus-read" {
		if flag.NArg() < 2 {
			log.Fatal("corpus-read: missing key argument")
		}

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		client, err := buildCacheOnlyClient(ctx)
		if err != nil {
			log.Fatalf("corpus-read: %v", err)
		}
		if err := tools.CorpusRead(client, flag.Arg(1), os.Stdout); err != nil {
			log.Fatalf("corpus-read: %v", err)
		}
		return
	}

	if flag.NArg() >= 1 && flag.Arg(0) == "fetch" {
		token := os.Getenv("NEWSBLUR_TOKEN")
		if token == "" {
			log.Fatal("NEWSBLUR_TOKEN environment variable is required")
		}

		fetchFlags := flag.NewFlagSet("fetch", flag.ExitOnError)
		formatsFlag := fetchFlags.String(
			"formats", strings.Join(tools.DefaultCaptureFormats, ","),
			"comma-separated cutting-garden capture formats (for the folded-in capture phase)",
		)
		storeFlag := fetchFlags.String(
			"store", defaultCaptureStoreId,
			"cutting-garden blob-store id to capture receipts into (for the folded-in capture phase)",
		)
		noCapture := fetchFlags.Bool(
			"no-capture", false,
			"skip the capture phase entirely on this run, regardless of the interval gate",
		)
		fetchFlags.Parse(flag.Args()[1:])

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		client := newsblur.NewClient(token)
		if err := attachCache(ctx, client, 1*time.Hour); err != nil {
			log.Fatalf("fetch: cache setup: %v", err)
		}

		if err := fetchAll(ctx, client, splitFormats(*formatsFlag), *storeFlag, *noCapture); err != nil {
			log.Fatalf("fetch: %v", err)
		}
		return
	}

	if flag.NArg() >= 1 && flag.Arg(0) == "capture" {
		runCapture(flag.Args()[1:])
		return
	}

	if flag.NArg() >= 1 && flag.Arg(0) == "serve" {
		serveArgs := flag.Args()[1:]
		if len(serveArgs) == 0 {
			fmt.Fprintf(os.Stderr, "nebulous: serve: missing subcommand (expected 'mcp')\n")
			flag.Usage()
			os.Exit(1)
		}
		switch serveArgs[0] {
		case "mcp":
			if len(serveArgs) > 1 {
				fmt.Fprintf(os.Stderr, "nebulous: serve mcp: unexpected arguments: %v\n", serveArgs[1:])
				flag.Usage()
				os.Exit(1)
			}
			serveMCP()
			return
		default:
			fmt.Fprintf(os.Stderr, "nebulous: serve: unknown subcommand %q\n", serveArgs[0])
			flag.Usage()
			os.Exit(1)
		}
	}

	if flag.NArg() == 0 {
		flag.Usage()
		return
	}

	fmt.Fprintf(os.Stderr, "nebulous: unknown command %q\n", flag.Arg(0))
	flag.Usage()
	os.Exit(1)
}

// serveMCP starts the MCP server over stdio. Requires NEWSBLUR_TOKEN.
func serveMCP() {
	token := os.Getenv("NEWSBLUR_TOKEN")
	if token == "" {
		log.Fatal("NEWSBLUR_TOKEN environment variable is required")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client := newsblur.NewClient(token)
	if err := attachCache(ctx, client, 1*time.Hour); err != nil {
		log.Fatalf("cache setup: %v", err)
	}

	app, resources := tools.RegisterAll(client)

	t := transport.NewStdio(os.Stdin, os.Stdout)

	registry := server.NewToolRegistryV1()
	app.RegisterMCPToolsV1(registry)

	srv, err := server.New(t, server.Options{
		ServerName:    app.Name,
		ServerVersion: app.Version,
		Instructions:  "NewsBlur MCP server. Read nebulous://stories/facets first to understand the data shape (years, tags, feeds, counts), then use story_query to filter by year, tag, feed, words, or any combination. Use nebulous://story/{hash} for metadata, story/{hash}/content for text, story/{hash}/original for full articles. Delegate bulk story reads to subagents. Use feed_query for feed discovery. Bulk mutation tools (mark_read, mark_feed_read, mark_all_read, subscribe, folder management) hit the NewsBlur API directly; per-story/per-feed mutations (star/unstar, read/unread, rename/move) live on the nebulous-cg plugin's create_node/patch_node/delete_node instead.",
		Tools:         registry,
		Resources:     resources,
	})
	if err != nil {
		log.Fatalf("creating server: %v", err)
	}

	if err := srv.Run(ctx); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// defaultManifestPath resolves the nebulous manifest location under XDG
// conventions. Returns "" if no home can be resolved (run without cache).
func defaultManifestPath() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "nebulous", "manifest.json")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "nebulous", "manifest.json")
	}
	return ""
}

// buildBlobSink returns an initialized madder-backed sink bound to ctx. ctx
// governs the lifetime of every madder process invocation.
func buildBlobSink(ctx context.Context) (*madder.Store, error) {
	store, err := madder.NewStore(ctx)
	if err != nil {
		return nil, fmt.Errorf("madder new store: %w", err)
	}
	if err := store.Init(); err != nil {
		return nil, fmt.Errorf("madder init: %w", err)
	}
	return store, nil
}

// attachCache wires a madder-backed response cache onto client. ctx bounds
// the lifetime of madder invocations initiated via the cache.
func attachCache(ctx context.Context, client *newsblur.Client, ttl time.Duration) error {
	manifestPath := defaultManifestPath()
	if manifestPath == "" {
		return nil // no HOME/XDG_DATA_HOME; run without cache
	}
	sink, err := buildBlobSink(ctx)
	if err != nil {
		return err
	}
	return client.WithCache(manifestPath, ttl, sink)
}

func buildCacheOnlyClient(ctx context.Context) (*newsblur.Client, error) {
	manifestPath := defaultManifestPath()
	if manifestPath == "" {
		return nil, fmt.Errorf("cannot resolve nebulous manifest path (set HOME or XDG_DATA_HOME)")
	}
	sink, err := buildBlobSink(ctx)
	if err != nil {
		return nil, err
	}
	return newsblur.NewCacheOnlyClient(manifestPath, sink)
}

// defaultCaptureInterval is how often fetchAll's folded-in capture phase
// actually runs, absent NEBULOUS_CAPTURE_INTERVAL. The capture loop
// itself is cheap to no-op (HasCaptureRecord is a pure cache lookup per
// (story, format) pair), but the corpus scan behind it (index.Stories())
// is real cost tied to the manifest's mtime — running it every fetch
// tick (default 1h) instead of a slower cadence would multiply that
// cost 6x for no benefit, since new stories are gap-filled regardless of
// which fetch tick catches them.
const defaultCaptureInterval = 6 * time.Hour

// captureInterval returns the configured gate for fetchAll's capture
// phase, parsed from NEBULOUS_CAPTURE_INTERVAL (falls back to
// defaultCaptureInterval on unset/unparseable — same operator-facing
// default the standalone nebulous-capture systemd timer used to enforce).
func captureInterval() time.Duration {
	if raw := os.Getenv("NEBULOUS_CAPTURE_INTERVAL"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			return d
		}
	}
	return defaultCaptureInterval
}

// captureDue reports whether enough time has passed since the capture
// phase's last successful run (or it has never run) to run it again.
func captureDue(client *newsblur.Client) (due bool, lastScan time.Time) {
	last, ok := client.CaptureLastScanAt()
	if !ok {
		return true, time.Time{}
	}
	return time.Since(last) >= captureInterval(), last
}

// runFetchCapturePhase is fetchAll's final phase: the gap-filling
// capture scan (internal/alfa/capture via cutting-garden+chrest), folded
// into fetch instead of living behind its own systemd timer (FDR 0001
// Stage 3's original separation reversed — see docs/features/0001).
// Soft-skips (single log line, no error) on -no-capture, the interval
// gate not yet elapsed, or a missing cutting-garden on PATH (a fetch-only
// host with no chrest wired) — mirrors the existing "[feeds] error: %v
// (continuing)" pattern: a capture-phase failure never fails the fetch
// run as a whole. lookPath is threaded as a parameter (matching every
// other capture-phase input) rather than a package-level var, so tests
// can substitute a fake PATH-miss/hit without a shared mutable global;
// checked last among the skip conditions since it's the only one that
// does real I/O (a PATH scan), and the interval gate above it already
// skips most fetch ticks for free.
func runFetchCapturePhase(ctx context.Context, client *newsblur.Client, storeId string, formats []string, skipCapture bool, lookPath func() (string, error)) {
	if skipCapture {
		log.Println("[capture] skipped (-no-capture)")
		return
	}
	due, last := captureDue(client)
	if !due {
		log.Printf("[capture] skipped: last ran %s ago (interval %s)", time.Since(last).Round(time.Second), captureInterval())
		return
	}
	if _, err := lookPath(); err != nil {
		log.Println("[capture] skipped: cutting-garden not on PATH")
		return
	}
	if err := runCaptureLoop(ctx, client, storeId, formats, false); err != nil {
		log.Printf("[capture] error: %v (continuing)", err)
		return
	}
	if err := client.PutCaptureLastScanAt(time.Now()); err != nil {
		log.Printf("[capture] error recording last-scan timestamp: %v (continuing)", err)
	}
}

// fetchAll runs all four phases (feeds, starred stories, original text,
// and the gap-filling capture scan), populating the local cache.
func fetchAll(ctx context.Context, client *newsblur.Client, captureFormats []string, captureStoreId string, skipCapture bool) error {
	// Phase 1: Feeds metadata
	log.Println("[feeds] fetching feed list...")
	if _, err := client.Feeds(ctx, false, true, false); err != nil {
		log.Printf("[feeds] error: %v (continuing)", err)
	} else {
		log.Println("[feeds] cached")
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Phase 2: Hash manifest + missing story fetch
	log.Println("[starred] fetching hash manifest...")
	raw, err := client.StarredStoryHashes(ctx)
	if err != nil {
		return fmt.Errorf("fetching hashes: %w", err)
	}

	if err := client.PutCachedStarredStoryHashes(raw); err != nil {
		return fmt.Errorf("caching hash manifest: %w", err)
	}

	hashes, err := newsblur.ParseStarredHashes(raw)
	if err != nil {
		return fmt.Errorf("parsing hashes: %w", err)
	}

	var missingStories []string
	for _, h := range hashes {
		if !client.HasCachedStarredStory(h) {
			missingStories = append(missingStories, h)
		}
	}

	log.Printf("[starred] total: %d, cached: %d, missing: %d",
		len(hashes), len(hashes)-len(missingStories), len(missingStories))

	if len(missingStories) > 0 {
		fetched, err := fetchStarredStoriesByHash(ctx, client, missingStories)
		if err != nil {
			return fmt.Errorf("fetching starred stories: %w", err)
		}
		log.Printf("[starred] fetched %d/%d stories", fetched, len(missingStories))
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Phase 3: Original text for starred story hashes
	var missingText []string
	for _, h := range hashes {
		if !client.HasCachedOriginalText(h) {
			missingText = append(missingText, h)
		}
	}

	log.Printf("[original-text] total: %d, cached: %d, missing: %d",
		len(hashes), len(hashes)-len(missingText), len(missingText))

	if len(missingText) > 0 {
		fetched := fetchWithBackoff(ctx, "original-text", missingText, func(hash string) error {
			_, err := client.OriginalText(ctx, hash)
			return err
		})
		log.Printf("[original-text] fetched %d/%d", fetched, len(missingText))
	}

	log.Printf("[done] feeds: cached, stories: %d, original text: %d/%d cached",
		len(hashes), len(hashes)-len(missingText), len(hashes))

	if err := ctx.Err(); err != nil {
		return err
	}

	// Phase 4: capture (best-effort, self-healing gap-filling scan)
	runFetchCapturePhase(ctx, client, captureStoreId, captureFormats, skipCapture, func() (string, error) {
		return exec.LookPath("cutting-garden")
	})

	return nil
}

// adaptiveBackoff learns from rate limit bursts. After a burst resolves
// (success), the peak wait from that burst becomes the new base for future
// waits, since it was the duration that actually cleared the rate limit.
type adaptiveBackoff struct {
	base  time.Duration // learned floor from past bursts
	max   time.Duration
	extra time.Duration // exponential addition on top of base
	peak  time.Duration // largest single wait in this burst
}

func newAdaptiveBackoff(max time.Duration) *adaptiveBackoff {
	return &adaptiveBackoff{
		base:  4*time.Minute + 15*time.Second,
		max:   max,
		extra: 1 * time.Second,
	}
}

func (b *adaptiveBackoff) nextWait(retryAfter time.Duration) time.Duration {
	wait := b.base + b.extra
	if retryAfter > wait {
		wait = retryAfter
	}
	if wait > b.peak {
		b.peak = wait
	}
	b.extra = b.extra * 2
	if b.base+b.extra > b.max {
		b.extra = b.max - b.base
	}
	return wait
}

func (b *adaptiveBackoff) onSuccess() {
	if b.peak > b.base {
		b.base = b.peak
		log.Printf("[backoff] learned new base: %s", b.peak)
	}
	b.extra = 1 * time.Second
	b.peak = 0
}

func (b *adaptiveBackoff) wait(ctx context.Context, wait time.Duration) error {
	t := time.NewTimer(wait)
	select {
	case <-ctx.Done():
		t.Stop()
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

const starredBatchSize = 100

func fetchStarredStoriesByHash(ctx context.Context, client *newsblur.Client, hashes []string) (int, error) {
	bo := newAdaptiveBackoff(5 * time.Minute)
	fetched := 0

	for i := 0; i < len(hashes); i += starredBatchSize {
		if err := ctx.Err(); err != nil {
			return fetched, err
		}

		end := i + starredBatchSize
		if end > len(hashes) {
			end = len(hashes)
		}
		batch := hashes[i:end]

		raw, err := client.StoriesStarredByHash(ctx, batch)
		if err != nil {
			var rle *newsblur.RateLimitError
			if errors.As(err, &rle) {
				wait := bo.nextWait(rle.RetryAfter)
				log.Printf("[starred] rate limited at %d/%d, backing off %s", fetched, len(hashes), wait)

				if err := bo.wait(ctx, wait); err != nil {
					return fetched, err
				}

				i -= starredBatchSize // retry same batch
				continue
			}
			return fetched, fmt.Errorf("batch at %d: %w", i, err)
		}

		bo.onSuccess()

		var resp struct {
			Stories []json.RawMessage `json:"stories"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return fetched, fmt.Errorf("parsing batch at %d: %w", i, err)
		}

		for _, storyRaw := range resp.Stories {
			var story struct {
				Hash string `json:"story_hash"`
			}
			if err := json.Unmarshal(storyRaw, &story); err != nil {
				log.Printf("[starred] skipping story with unparseable hash: %v", err)
				continue
			}
			if err := client.PutCachedStarredStory(story.Hash, storyRaw); err != nil {
				log.Printf("[starred] error caching story %s: %v", story.Hash, err)
				continue
			}
			fetched++
		}

		if fetched%500 == 0 && fetched > 0 {
			log.Printf("[starred] cached %d/%d stories", fetched, len(hashes))
		}
	}

	return fetched, nil
}

func fetchWithBackoff(ctx context.Context, label string, items []string, fetch func(string) error) int {
	bo := newAdaptiveBackoff(5 * time.Minute)
	fetched := 0

	for i := 0; i < len(items); i++ {
		if err := ctx.Err(); err != nil {
			return fetched
		}

		err := fetch(items[i])
		if err != nil {
			var rle *newsblur.RateLimitError
			if errors.As(err, &rle) {
				wait := bo.nextWait(rle.RetryAfter)
				log.Printf("[%s] rate limited at %d/%d, backing off %s", label, fetched, len(items), wait)

				if err := bo.wait(ctx, wait); err != nil {
					return fetched
				}

				i-- // retry same item
				continue
			}
			log.Printf("[%s] error fetching %s: %v (skipping)", label, items[i], err)
			continue
		}

		bo.onSuccess()
		fetched++

		if fetched%100 == 0 {
			log.Printf("[%s] fetched %d/%d", label, fetched, len(items))
		}
	}

	return fetched
}
