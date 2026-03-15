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
		fmt.Fprintf(os.Stderr, "  nebulous fetch-original-text  Progressively cache original article text\n\n")
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

	if flag.NArg() >= 1 && flag.Arg(0) == "fetch-original-text" {
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

		if err := fetchOriginalText(ctx, client); err != nil {
			log.Fatalf("fetch-original-text: %v", err)
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

func fetchOriginalText(ctx context.Context, client *newsblur.Client) error {
	log.Println("fetching starred story hashes...")
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

	log.Printf("total: %d, cached: %d, missing: %d", len(hashes), len(hashes)-len(missing), len(missing))

	if len(missing) == 0 {
		log.Println("all original text already cached")
		return nil
	}

	backoff := 1 * time.Second
	maxBackoff := 5 * time.Minute
	fetched := 0

	for _, hash := range missing {
		select {
		case <-ctx.Done():
			log.Printf("interrupted after fetching %d/%d", fetched, len(missing))
			return ctx.Err()
		default:
		}

		_, err := client.OriginalText(ctx, hash)
		if err != nil {
			var rle *newsblur.RateLimitError
			if errors.As(err, &rle) {
				wait := backoff
				if rle.RetryAfter > 0 {
					wait = rle.RetryAfter
				}
				log.Printf("rate limited at %d/%d, backing off %s", fetched, len(missing), wait)

				t := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					t.Stop()
					return ctx.Err()
				case <-t.C:
				}

				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}
			log.Printf("error fetching %s: %v (skipping)", hash, err)
			continue
		}

		fetched++
		backoff = 1 * time.Second // reset on success

		if fetched%100 == 0 {
			log.Printf("fetched %d/%d", fetched, len(missing))
		}
	}

	log.Printf("done: fetched %d/%d", fetched, len(missing))
	return nil
}

func parseStarredHashes(raw json.RawMessage) ([]string, error) {
	// Try flat array: ["hash1", "hash2"]
	var flat []string
	if err := json.Unmarshal(raw, &flat); err == nil {
		return flat, nil
	}

	// Try feed-grouped: {"123": [["hash1", "ts1"], ...]}
	var byFeed map[string][][2]string
	if err := json.Unmarshal(raw, &byFeed); err == nil {
		var hashes []string
		for _, pairs := range byFeed {
			for _, pair := range pairs {
				hashes = append(hashes, pair[0])
			}
		}
		return hashes, nil
	}

	return nil, fmt.Errorf("unrecognized starred_story_hashes format")
}
