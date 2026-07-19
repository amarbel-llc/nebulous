package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/friedenberg/nebulous/internal/alfa/capture"
	"github.com/friedenberg/nebulous/internal/alfa/newsblur"
	"github.com/friedenberg/nebulous/internal/bravo/tools"
)

// defaultCaptureStoreId is the madder blob-store id cutting-garden
// writes capture receipts into — the same named store internal/0/madder
// resolves for nebulous's own response cache (Stage 1), so there is one
// store to back up/migrate rather than two. No leading dot: confirmed
// against a real `cutting-garden capture` invocation this session —
// `.nebulous` doesn't resolve ("blob store not found"), the actual store
// list shows the bare name.
const defaultCaptureStoreId = "nebulous"

// runCapture is `nebulous capture`: a gap-filling scan over the local
// starred-story corpus. For each configured format, it captures any
// eligible story that doesn't yet have a recorded receipt for that
// format via `cutting-garden capture` (which drives chrest under the
// hood). A capture that fails simply has no receipt, so the next
// `nebulous capture` invocation retries it automatically — no retry loop
// runs within a single invocation.
func runCapture(args []string) {
	captureFlags := flag.NewFlagSet("capture", flag.ExitOnError)
	formatsFlag := captureFlags.String(
		"formats", strings.Join(tools.DefaultCaptureFormats, ","),
		"comma-separated cutting-garden capture formats",
	)
	storeFlag := captureFlags.String(
		"store", defaultCaptureStoreId,
		"cutting-garden blob-store id to capture receipts into",
	)
	backfill := captureFlags.Bool(
		"backfill", false,
		"ignore the capture watermark: consider every starred story eligible this run "+
			"(still skips (story, format) pairs that already have a recorded receipt)",
	)
	captureFlags.Parse(args)

	formats := splitFormats(*formatsFlag)
	if len(formats) == 0 {
		log.Fatal("capture: --formats must name at least one format")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client, err := newsblur.NewDefaultCacheOnlyClient(ctx)
	if err != nil {
		log.Fatalf("capture: %v", err)
	}

	if err := runCaptureLoop(ctx, client, *storeFlag, formats, *backfill); err != nil {
		log.Fatalf("capture: %v", err)
	}
}

func splitFormats(raw string) []string {
	var out []string
	for _, f := range strings.Split(raw, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// runCaptureLoop is the gap-filling scan itself, factored out of
// runCapture for testability without a live cache.
func runCaptureLoop(
	ctx context.Context, client *newsblur.Client, storeId string, formats []string, backfill bool,
) error {
	watermark, hasWatermark := client.CaptureWatermark()
	if !hasWatermark {
		// First-ever run: anchor "new stories only, going forward" without
		// walking the existing backlog. --backfill on this same first
		// invocation still establishes the watermark but proceeds to
		// capture immediately, since eligibility no longer depends on it.
		now := time.Now()
		if err := client.PutCaptureWatermark(now); err != nil {
			return fmt.Errorf("establishing capture watermark: %w", err)
		}
		watermark = now
		log.Printf("[capture] first run: watermark set to %s", now.Format(time.RFC3339))
		if !backfill {
			log.Printf("[capture] no captures this run (new stories only, going forward — pass --backfill to capture the existing corpus too)")
			return nil
		}
	}

	index := tools.NewReadIndex(client)
	stories, err := index.Stories()
	if err != nil {
		return fmt.Errorf("listing stories: %w", err)
	}

	cc := capture.NewClient()

	var attempted, captured, failed, skippedNoPermalink int
	for _, s := range stories {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.Permalink == "" {
			skippedNoPermalink++
			continue
		}
		if !backfill && s.Date.Before(watermark) {
			continue
		}

		for _, format := range formats {
			if client.HasCaptureRecord(s.Hash, format) {
				continue
			}
			attempted++
			receiptID, err := cc.Capture(ctx, storeId, s.Permalink, format)
			if err != nil {
				failed++
				log.Printf("[capture] %s (%s): %v", s.Hash, format, err)
				continue
			}
			rec := newsblur.CaptureRecord{ReceiptID: receiptID, CapturedAt: time.Now()}
			if err := client.PutCaptureRecordFor(s.Hash, format, rec); err != nil {
				failed++
				log.Printf("[capture] %s (%s): captured (receipt %s) but failed to record: %v", s.Hash, format, receiptID, err)
				continue
			}
			captured++
		}
	}

	log.Printf("[capture] attempted %d, captured %d, failed %d, skipped (no permalink) %d",
		attempted, captured, failed, skippedNoPermalink)
	return nil
}
