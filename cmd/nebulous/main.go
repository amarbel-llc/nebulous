package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
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
		fmt.Fprintf(os.Stderr, "  nebulous [flags]              Start MCP server\n")
		fmt.Fprintf(os.Stderr, "  nebulous generate-plugin      Generate plugin.json\n")
		fmt.Fprintf(os.Stderr, "  nebulous hook                 Handle purse-first hooks\n")
		fmt.Fprintf(os.Stderr, "  nebulous install-mcp          Install MCP server config\n")
		fmt.Fprintf(os.Stderr, "  nebulous fetch                Progressively cache feeds, starred stories, and original text\n")
		fmt.Fprintf(os.Stderr, "  nebulous corpus-list [-limit N] List starred story keys (for maneater)\n")
		fmt.Fprintf(os.Stderr, "  nebulous corpus-read <key>    Extract story text by key (for maneater)\n\n")
		fmt.Fprintf(os.Stderr, "Environment:\n")
		fmt.Fprintf(os.Stderr, "  NEWSBLUR_TOKEN   NewsBlur session cookie (required except corpus-*/generate-plugin/hook/install-mcp)\n")
		fmt.Fprintf(os.Stderr, "  XDG_DATA_HOME    honored when resolving the nebulous manifest path ($XDG_DATA_HOME/nebulous/manifest.json)\n\n")
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

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		client := newsblur.NewClient(token)
		if err := attachCache(ctx, client, 1*time.Hour); err != nil {
			log.Fatalf("fetch: cache setup: %v", err)
		}

		if err := fetchAll(ctx, client); err != nil {
			log.Fatalf("fetch: %v", err)
		}
		return
	}

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

	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "nebulous: unexpected arguments: %v\n", flag.Args())
		flag.Usage()
		os.Exit(1)
	}

	t := transport.NewStdio(os.Stdin, os.Stdout)

	registry := server.NewToolRegistryV1()
	app.RegisterMCPToolsV1(registry)

	srv, err := server.New(t, server.Options{
		ServerName:    app.Name,
		ServerVersion: app.Version,
		Instructions:  "NewsBlur MCP server. Read nebulous://stories/facets first to understand the data shape (years, tags, feeds, counts), then use story_query to filter by year, tag, feed, words, or any combination. Use nebulous://story/{hash} for metadata, story/{hash}/content for text, story/{hash}/original for full articles. Delegate bulk story reads to subagents. Use feed_query for feed discovery. Mutation tools (star, mark_read, subscribe, etc.) hit the NewsBlur API directly.",
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
	store := madder.NewStore(ctx)
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

func fetchAll(ctx context.Context, client *newsblur.Client) error {
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
