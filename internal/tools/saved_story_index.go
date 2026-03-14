package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/friedenberg/nebulous/internal/newsblur"
)

// TODO: Index story_content (HTML stripped) in addition to story_title for deeper content search.

const (
	savedStoryMaxPages  = 100           // ~1000 stories, avoids hammering the API
	savedStoryPageDelay = 500 * time.Millisecond
	savedStoryMaxRetries = 3
	savedStoryBaseBackoff = 1 * time.Second
)

type storySummary struct {
	Hash      string `json:"hash"`
	Title     string `json:"title"`
	FeedID    int    `json:"feed_id"`
	Date      string `json:"date"`
	Permalink string `json:"permalink"`
}

type savedStoryIndexResult struct {
	words   map[string][]storySummary
	partial bool
	warning string
}

type savedStoryIndex struct {
	client      *newsblur.Client
	mu          sync.Mutex
	built       bool
	fingerprint string
	words       map[string][]storySummary
}

func newSavedStoryIndex(client *newsblur.Client) *savedStoryIndex {
	return &savedStoryIndex{
		client: client,
	}
}

func (idx *savedStoryIndex) ensureBuilt(ctx context.Context) savedStoryIndexResult {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	fp, fpErr := idx.fetchFingerprint(ctx)

	if idx.built && fpErr == nil && fp == idx.fingerprint {
		return savedStoryIndexResult{words: idx.words}
	}

	if fpErr != nil {
		log.Printf("saved story index: fingerprint fetch failed: %v", fpErr)
		if idx.built {
			return savedStoryIndexResult{words: idx.words}
		}
	}

	if fpErr == nil && fp != idx.fingerprint {
		idx.client.InvalidateStarredStoryPages()
	}

	return idx.buildAndReturn(ctx, fp)
}

func (idx *savedStoryIndex) buildAndReturn(ctx context.Context, fp string) savedStoryIndexResult {
	idx.words = make(map[string][]storySummary)

	if err := idx.build(ctx); err != nil {
		var rle *newsblur.RateLimitError
		if errors.As(err, &rle) && len(idx.words) > 0 {
			return savedStoryIndexResult{
				words:   idx.words,
				partial: true,
				warning: err.Error(),
			}
		}
		idx.words = nil
		return savedStoryIndexResult{warning: err.Error()}
	}

	idx.built = true
	idx.fingerprint = fp
	return savedStoryIndexResult{words: idx.words}
}

func (idx *savedStoryIndex) fetchFingerprint(ctx context.Context) (string, error) {
	raw, err := idx.client.StarredStoryHashes(ctx)
	if err != nil {
		return "", err
	}
	return computeStarredFingerprint(raw), nil
}

func computeStarredFingerprint(raw json.RawMessage) string {
	var hashes []string

	// Try flat array of strings: ["hash1", "hash2"]
	if err := json.Unmarshal(raw, &hashes); err == nil {
		sort.Strings(hashes)
		return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(hashes, ","))))
	}

	// Try object with feed_id keys mapping to arrays of [hash, timestamp] pairs:
	// {"123": [["hash1", "ts1"], ["hash2", "ts2"]]}
	var byFeed map[string][][2]string
	if err := json.Unmarshal(raw, &byFeed); err == nil {
		for _, pairs := range byFeed {
			for _, pair := range pairs {
				hashes = append(hashes, pair[0])
			}
		}
		sort.Strings(hashes)
		return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(hashes, ","))))
	}

	// Fallback: hash the raw response
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (idx *savedStoryIndex) build(ctx context.Context) error {
	totalStories := 0

	for page := 1; page <= savedStoryMaxPages; page++ {
		if page > 1 {
			if err := sleepCtx(ctx, savedStoryPageDelay); err != nil {
				return err
			}
		}

		raw, err := idx.fetchPageWithRetry(ctx, page)
		if err != nil {
			return fmt.Errorf("page %d (%d stories indexed so far): %w", page, totalStories, err)
		}

		var resp struct {
			Stories []json.RawMessage `json:"stories"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return err
		}

		if len(resp.Stories) == 0 {
			break
		}

		for _, storyRaw := range resp.Stories {
			var story struct {
				Hash      string `json:"story_hash"`
				Title     string `json:"story_title"`
				FeedID    int    `json:"story_feed_id"`
				Date      string `json:"story_date"`
				Permalink string `json:"story_permalink"`
			}
			if err := json.Unmarshal(storyRaw, &story); err != nil {
				continue
			}

			summary := storySummary{
				Hash:      story.Hash,
				Title:     story.Title,
				FeedID:    story.FeedID,
				Date:      story.Date,
				Permalink: story.Permalink,
			}

			seen := make(map[string]bool)
			for _, word := range extractWords(story.Title) {
				if !seen[word] {
					seen[word] = true
					idx.words[word] = append(idx.words[word], summary)
				}
			}
		}

		totalStories += len(resp.Stories)
	}

	log.Printf("saved story index: indexed %d stories, %d words", totalStories, len(idx.words))
	return nil
}

func (idx *savedStoryIndex) fetchPageWithRetry(ctx context.Context, page int) (json.RawMessage, error) {
	backoff := savedStoryBaseBackoff

	for attempt := range savedStoryMaxRetries {
		raw, err := idx.client.StoriesStarred(ctx, page, "", "")
		if err == nil {
			return raw, nil
		}

		var rle *newsblur.RateLimitError
		if !errors.As(err, &rle) {
			return nil, err
		}

		if attempt == savedStoryMaxRetries-1 {
			return nil, err
		}

		wait := backoff
		if rle.RetryAfter > 0 {
			wait = rle.RetryAfter
		}

		log.Printf("saved story index: rate limited on page %d, retrying in %s", page, wait)
		if err := sleepCtx(ctx, wait); err != nil {
			return nil, err
		}

		backoff *= 2
	}

	// unreachable
	return nil, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
