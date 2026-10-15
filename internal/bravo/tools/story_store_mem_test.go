package tools

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
)

// Memory-profile harness for nebulous#58. Skipped unless -corpus-stories is
// set, so the normal suite is unaffected:
//
//	just debug-go-test internal/bravo/tools -test.run=TestStoryStoreMemory -test.v -corpus-stories=30000
var (
	corpusStories = flag.Int("corpus-stories", 0, "synthetic story count for TestStoryStoreMemory (0 = skip)")
	corpusHeapOut = flag.String("corpus-heap-out", "", "write a post-build heap profile here")
)

// syntheticCorpus puts n starred stories (summary content plus ~2/3 with
// full original text) into a memSink-backed client. Words are drawn from a
// Zipf distribution over a large vocabulary so the distinct-word count is
// in the same range as production (752k words / 30.7k stories).
func syntheticCorpus(tb testing.TB, n int) *newsblur.Client {
	tb.Helper()
	c := newsblur.NewClient("test-token")
	if err := c.WithCache(filepath.Join(tb.TempDir(), "manifest.json"), time.Hour, newMemSink()); err != nil {
		tb.Fatal(err)
	}
	rng := rand.New(rand.NewSource(1))
	zipf := rand.NewZipf(rng, 1.07, 40, 900_000)
	word := func() string { return "w" + strconv.FormatUint(zipf.Uint64(), 36) + "x" }
	text := func(words int) string {
		var b strings.Builder
		b.WriteString("<p>")
		for i := 0; i < words; i++ {
			b.WriteString(word())
			b.WriteByte(' ')
			if i%40 == 39 {
				b.WriteString("</p><p>")
			}
		}
		b.WriteString("</p>")
		return b.String()
	}

	hashes := make([]string, n)
	for i := range hashes {
		hashes[i] = fmt.Sprintf("%d:%08x", 1000+i%500, i)
	}
	mb, _ := json.Marshal(map[string][]string{"starred_story_hashes": hashes})
	if err := c.PutCachedStarredStoryHashes(mb); err != nil {
		tb.Fatal(err)
	}
	for i, h := range hashes {
		raw, _ := json.Marshal(map[string]any{
			"story_hash":      h,
			"story_title":     text(8),
			"story_authors":   "A",
			"story_feed_id":   1000 + i%500,
			"story_date":      time.Unix(1.6e9+int64(i)*3600, 0).UTC().Format("2006-01-02 15:04:05"),
			"story_permalink": "https://example.com/" + h,
			"story_tags":      []string{"t1"},
			"user_tags":       []string{"interests"},
			"story_content":   text(150 + rng.Intn(300)),
			"starred":         true,
		})
		if err := c.PutCachedStarredStory(h, raw); err != nil {
			tb.Fatal(err)
		}
		if i%3 != 0 {
			ot, _ := json.Marshal(map[string]string{"original_text": text(800 + rng.Intn(1500))})
			if err := c.PutCachedOriginalText(h, ot); err != nil {
				tb.Fatal(err)
			}
		}
	}
	return c
}

func heapInUseMB() float64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / (1 << 20)
}

func TestStoryStoreMemory(t *testing.T) {
	if *corpusStories == 0 {
		t.Skip("set -corpus-stories=N to run")
	}
	c := syntheticCorpus(t, *corpusStories)
	for _, mode := range []struct {
		name  string
		build func(*newsblur.Client) *storyStore
	}{
		{"metadata-only (traversal-serve/capture)", newMetadataStoryStore},
		{"word-index (serve mcp)", newStoryStore},
	} {
		t.Run(mode.name, func(t *testing.T) { measureStoryStore(t, c, mode.build(c)) })
	}
}

func measureStoryStore(t *testing.T, c *newsblur.Client, s *storyStore) {
	base := heapInUseMB()
	start := time.Now()
	snap, err := s.current()
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	var peak runtime.MemStats
	runtime.ReadMemStats(&peak)
	after := heapInUseMB()
	t.Logf("MEM stories=%d words=%d build=%s", len(snap.stories), len(snap.words), elapsed)
	t.Logf("MEM corpus-baseline=%.0fMB  after-build(retained)=%.0fMB  index-cost=%.0fMB  sys-peak=%.0fMB (incl. test corpus)",
		base, after, after-base, float64(peak.Sys)/(1<<20))

	if *corpusHeapOut != "" {
		f, err := os.Create(*corpusHeapOut)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := pprof.Lookup("heap").WriteTo(f, 0); err != nil {
			t.Fatal(err)
		}
	}

	// A rebuild while the old snapshot is still referenced is the "two
	// copies" window; measure the high-water mark of it.
	if err := os.Chtimes(c.ManifestPath(), time.Now(), time.Now().Add(time.Hour)); err == nil {
		old := snap
		s.cache.lastCheckedAt = time.Time{}
		start = time.Now()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		if _, err := s.current(); err != nil {
			t.Fatal(err)
		}
		var mid runtime.MemStats
		runtime.ReadMemStats(&mid)
		t.Logf("MEM rebuild=%s heap-sys-after-rebuild=%.0fMB (old snapshot still held: %d stories)",
			time.Since(start), float64(mid.HeapSys)/(1<<20), len(old.stories))
	}
}
