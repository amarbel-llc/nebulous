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
	"github.com/friedenberg/nebulous/internal/newsblur"
	"github.com/friedenberg/nebulous/internal/tools"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "nebulous — a NewsBlur MCP server\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  nebulous [flags]             Start MCP server\n")
		fmt.Fprintf(os.Stderr, "  nebulous generate-plugin      Generate plugin.json\n")
		fmt.Fprintf(os.Stderr, "  nebulous hook                 Handle purse-first hooks\n")
		fmt.Fprintf(os.Stderr, "  nebulous install-mcp          Install MCP server config\n")
		fmt.Fprintf(os.Stderr, "  nebulous fetch              Progressively cache feeds, starred stories, and original text\n\n")
		fmt.Fprintf(os.Stderr, "Environment:\n")
		fmt.Fprintf(os.Stderr, "  NEWSBLUR_TOKEN  NewsBlur session cookie (required)\n\n")
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

	if flag.NArg() >= 1 && flag.Arg(0) == "fetch" {
		token := os.Getenv("NEWSBLUR_TOKEN")
		if token == "" {
			log.Fatal("NEWSBLUR_TOKEN environment variable is required")
		}

		client := newsblur.NewClient(token)
		if home, err := os.UserHomeDir(); err == nil {
			cacheDir := filepath.Join(home, ".cache", "nebulous", "responses")
			client.WithCache(cacheDir, 1*time.Hour)
		}

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		if err := fetchAll(ctx, client); err != nil {
			log.Fatalf("fetch: %v", err)
		}
		return
	}

	token := os.Getenv("NEWSBLUR_TOKEN")
	if token == "" {
		log.Fatal("NEWSBLUR_TOKEN environment variable is required")
	}

	client := newsblur.NewClient(token)

	if home, err := os.UserHomeDir(); err == nil {
		cacheDir := filepath.Join(home, ".cache", "nebulous", "responses")
		client.WithCache(cacheDir, 1*time.Hour)
	}

	app, resources := tools.RegisterAll(client)

	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "nebulous: unexpected arguments: %v\n", flag.Args())
		flag.Usage()
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	t := transport.NewStdio(os.Stdin, os.Stdout)

	registry := server.NewToolRegistryV1()
	app.RegisterMCPToolsV1(registry)

	srv, err := server.New(t, server.Options{
		ServerName:    app.Name,
		ServerVersion: app.Version,
		Instructions:  "NewsBlur MCP server. Provides tools for reading feeds, managing stories, subscriptions, folders, and OPML import/export. Feed and story content responses are verbose — delegate queries to a subagent to keep the main context lean. Use feed_query/starred_story_index_query as lightweight entry points for discovery.",
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

	// Phase 2: Starred story pages
	log.Println("[starred] fetching starred story pages...")
	starredPages, err := fetchStarredStoryPages(ctx, client)
	if err != nil {
		return fmt.Errorf("starred stories: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Phase 3: Original text for all starred story hashes
	log.Println("[original-text] fetching starred story hashes...")
	raw, err := client.StarredStoryHashes(ctx)
	if err != nil {
		return fmt.Errorf("fetching hashes: %w", err)
	}

	hashes, err := parseStarredHashes(raw)
	if err != nil {
		return fmt.Errorf("parsing hashes: %w", err)
	}

	var missing []string
	for _, h := range hashes {
		if !client.HasCachedOriginalText(h) {
			missing = append(missing, h)
		}
	}

	log.Printf("[original-text] total: %d, cached: %d, missing: %d",
		len(hashes), len(hashes)-len(missing), len(missing))

	if len(missing) > 0 {
		fetched := fetchWithBackoff(ctx, missing, func(hash string) error {
			_, err := client.OriginalText(ctx, hash)
			return err
		})
		log.Printf("[original-text] fetched %d/%d", fetched, len(missing))
	}

	log.Printf("[done] feeds: cached, starred pages: %d, original text: %d/%d cached",
		starredPages, len(hashes)-len(missing), len(hashes))
	return nil
}

// adaptiveBackoff learns from rate limit bursts. After a burst of rate limits
// resolves (success), the cumulative wait minus the peak single wait becomes
// the starting backoff for the next burst. This progressively optimizes toward
// the actual rate limit window, avoiding wasted small waits.
type adaptiveBackoff struct {
	base       time.Duration // learned floor from past bursts
	max        time.Duration
	extra      time.Duration // exponential addition on top of base
	cumulative time.Duration // total wait accumulated in this burst
	peak       time.Duration // largest single wait in this burst
}

func newAdaptiveBackoff(max time.Duration) *adaptiveBackoff {
	return &adaptiveBackoff{
		base:  3 * time.Minute,
		max:   max,
		extra: 1 * time.Second,
	}
}

func (b *adaptiveBackoff) nextWait(retryAfter time.Duration) time.Duration {
	wait := b.base + b.extra
	if retryAfter > wait {
		wait = retryAfter
	}
	b.cumulative += wait
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
	if b.cumulative > 0 {
		learned := b.cumulative - b.peak
		if learned > b.base {
			b.base = learned
			log.Printf("[backoff] learned new base: %s (cumulative %s - peak %s)",
				learned, b.cumulative, b.peak)
		}
	}
	b.extra = 1 * time.Second
	b.cumulative = 0
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

func fetchStarredStoryPages(ctx context.Context, client *newsblur.Client) (int, error) {
	// Find first uncached page
	startPage := 1
	for {
		if _, ok := client.CachedStarredStoryPage(startPage); !ok {
			break
		}
		startPage++
	}

	if startPage > 1 {
		log.Printf("[starred] %d pages already cached, resuming from page %d", startPage-1, startPage)
	}

	fetched := 0
	bo := newAdaptiveBackoff(5 * time.Minute)

	for page := startPage; ; page++ {
		if err := ctx.Err(); err != nil {
			log.Printf("[starred] interrupted at page %d (%d new pages cached)", page, fetched)
			return startPage - 1 + fetched, err
		}

		raw, err := client.StoriesStarred(ctx, page, "", "")
		if err != nil {
			var rle *newsblur.RateLimitError
			if errors.As(err, &rle) {
				wait := bo.nextWait(rle.RetryAfter)
				log.Printf("[starred] rate limited at page %d, backing off %s", page, wait)

				if err := bo.wait(ctx, wait); err != nil {
					return startPage - 1 + fetched, err
				}

				page-- // retry same page
				continue
			}
			return startPage - 1 + fetched, fmt.Errorf("page %d: %w", page, err)
		}

		bo.onSuccess()

		var resp struct {
			Stories []json.RawMessage `json:"stories"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return startPage - 1 + fetched, fmt.Errorf("page %d: %w", page, err)
		}

		if len(resp.Stories) == 0 {
			break
		}

		fetched++

		if fetched%100 == 0 {
			log.Printf("[starred] cached %d pages (page %d)", fetched, page)
		}
	}

	log.Printf("[starred] done: %d total pages cached", startPage-1+fetched)
	return startPage - 1 + fetched, nil
}

func fetchWithBackoff(ctx context.Context, items []string, fetch func(string) error) int {
	bo := newAdaptiveBackoff(5 * time.Minute)
	fetched := 0

	for _, item := range items {
		select {
		case <-ctx.Done():
			return fetched
		default:
		}

		err := fetch(item)
		if err != nil {
			var rle *newsblur.RateLimitError
			if errors.As(err, &rle) {
				wait := bo.nextWait(rle.RetryAfter)
				log.Printf("[original-text] rate limited at %d/%d, backing off %s", fetched, len(items), wait)

				if err := bo.wait(ctx, wait); err != nil {
					return fetched
				}
				continue
			}
			log.Printf("[original-text] error fetching %s: %v (skipping)", item, err)
			continue
		}

		bo.onSuccess()
		fetched++

		if fetched%100 == 0 {
			log.Printf("[original-text] fetched %d/%d", fetched, len(items))
		}
	}

	return fetched
}

func parseStarredHashes(raw json.RawMessage) ([]string, error) {
	// API returns {"starred_story_hashes": ["hash1", ...], ...}
	var envelope struct {
		Hashes []string `json:"starred_story_hashes"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Hashes) > 0 {
		return envelope.Hashes, nil
	}

	// Try flat array: ["hash1", "hash2"]
	var flat []string
	if err := json.Unmarshal(raw, &flat); err == nil {
		return flat, nil
	}

	return nil, fmt.Errorf("unrecognized starred_story_hashes format")
}
