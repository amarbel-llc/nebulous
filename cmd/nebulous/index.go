package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
	"code.linenisgreat.com/nebulous/internal/bravo/tools"
)

// runFetchIndexPhase is fetchAll's word-index phase (nebulous#59): it
// (re)builds the on-disk word index the serve processes open, after the
// corpus is complete and before the capture scan. It rebuilds when the
// corpus changed in this run (new stories or original text fetched) or the
// existing file is missing, unreadable, from another tokenizer, or covers a
// different story set; otherwise it leaves the file alone. Like the capture
// phase it never fails the fetch: a failed build keeps the previous file in
// place (the rename swap is the last step) and the serve side falls back to
// building in memory.
func runFetchIndexPhase(ctx context.Context, client *newsblur.Client, corpusChanged, skip bool) {
	if skip {
		log.Println("[index] skipped (-no-index)")
		return
	}
	path := newsblur.WordIndexPath(client.ManifestPath())
	if path == "" {
		log.Println("[index] skipped: no manifest path")
		return
	}
	raw, ok := client.CachedStarredStoryHashes()
	if !ok {
		log.Println("[index] skipped: no cached hash manifest")
		return
	}
	hashes, err := newsblur.ParseStarredHashes(raw)
	if err != nil {
		log.Printf("[index] error: %v (continuing)", err)
		return
	}
	if !corpusChanged && tools.WordIndexCurrent(path, hashes) {
		log.Println("[index] up to date")
		return
	}
	if _, err := tools.BuildWordIndex(ctx, client, path); err != nil {
		log.Printf("[index] error: %v (continuing)", err)
	}
}

// runIndex is `nebulous index`: rebuild the word index from the local cache
// now, without contacting NewsBlur. For manual rebuilds and the first build
// after upgrading.
func runIndex(args []string) {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	fs.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client, err := newsblur.NewDefaultCacheOnlyClient(ctx)
	if err != nil {
		log.Fatalf("index: %v", err)
	}
	path := newsblur.WordIndexPath(client.ManifestPath())
	if _, err := tools.BuildWordIndex(ctx, client, path); err != nil {
		log.Fatalf("index: %v", err)
	}
}
