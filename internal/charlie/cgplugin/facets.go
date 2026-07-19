package cgplugin

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/nebulous/internal/bravo/tools"
)

// Facet dimension keys. Story dimensions are drawn from the fields
// already on tools.StoryRef; feed dimensions from tools.FeedRef. Both
// are populated by the same ReadIndex build the traversal already pays
// for, so FacetCounts stays a cheap in-memory pass with no extra fetch.
const (
	facetYear     = "year"
	facetUserTag  = "user_tag"
	facetStoryTag = "story_tag"
	facetFeed     = "feed"
	facetRead     = "read"
	facetAgeBand  = "age_band" // VOLATILE: a story's published date vs today (§11.3)

	facetFolder = "folder"
	facetActive = "active"
)

// The age_band closed domain: a total partition of time relative to the
// current host-local day, so every story occupies exactly one bucket at
// every instant. Order renders recency-first (most urgent/freshest last
// to fall out of relevance).
const (
	ageBandToday     = "today"
	ageBandThisWeek  = "this-week"
	ageBandThisMonth = "this-month"
	ageBandOlder     = "older"
)

// ageBandOrder renders recency-first (descending Order). The literal
// exists for stable Values ordering in DescribeFacets; the map is the
// single source at lift time.
var ageBandOrder = map[string]int64{
	ageBandToday:     4,
	ageBandThisWeek:  3,
	ageBandThisMonth: 2,
	ageBandOlder:     1,
}

// ageBandRevalidateAfter is the volatile window (RFC 0012 §11.3): a
// story crosses buckets at most this + the refresher interval late.
// Newsblur's own backend (a local index rebuilt on manifest-mtime
// change, internal/bravo/tools/stale_cache.go) is far cheaper to
// re-summarize than caldav's REPORT-per-component fetch, so this widens
// caldav's 15-minute window rather than matching it.
const ageBandRevalidateAfter = 30 * time.Minute

// ageBandNow is the evaluation clock, injectable for tests. Bucketing
// quantizes it to the host-local day start (§11.3 evaluation-instant
// quantization), so summaries memoized at different instants within one
// day agree exactly.
var ageBandNow = time.Now

var (
	_ cg.FacetDescriber = Plugin{}
	_ cg.FacetCounter   = Plugin{}
	_ cg.FacetLabeler   = Plugin{}
	_ cg.FacetVersioner = Plugin{}
)

// DescribeFacets declares the facet dimensions of the story and feed
// container types. "starred" is omitted: every story in this store is
// starred by construction (the local index only holds starred stories),
// so it would be a constant, uninformative dimension.
func (Plugin) DescribeFacets() []cg.NodeTypeFacets {
	return []cg.NodeTypeFacets{
		{
			Tag: typeStory,
			Dimensions: []cg.FacetDimension{
				{Key: facetYear, Label: "Year", Kind: cg.FacetNumericBucket},
				{Key: facetUserTag, Label: "User tag", Kind: cg.FacetCategorical, Multi: true},
				{Key: facetStoryTag, Label: "Story tag", Kind: cg.FacetCategorical, Multi: true},
				// Feed ids are opaque; ResolveFacetLabels resolves them to
				// feed titles once a consumer adopts FacetLabeler.
				{Key: facetFeed, Label: "Feed", Kind: cg.FacetLabelled},
				{
					Key: facetRead, Label: "Read", Kind: cg.FacetCategorical,
					Values: []cg.FacetValue{{Key: "read"}, {Key: "unread"}},
				},
				{
					// VOLATILE (RFC 0012 §11.3): bucketing is a function
					// of (published date, today). The label names the
					// anchor zone so consumers can reconcile boundaries
					// (host-local days — NewsBlur's story date carries
					// no per-feed TZID; absolute times convert).
					Key:   facetAgeBand,
					Label: "Age (host-local days)",
					Kind:  cg.FacetNumericBucket,
					Values: []cg.FacetValue{
						{Key: ageBandToday, Order: ageBandOrder[ageBandToday]},
						{Key: ageBandThisWeek, Order: ageBandOrder[ageBandThisWeek]},
						{Key: ageBandThisMonth, Order: ageBandOrder[ageBandThisMonth]},
						{Key: ageBandOlder, Order: ageBandOrder[ageBandOlder]},
					},
					RevalidateAfter: ageBandRevalidateAfter,
				},
			},
		},
		{
			Tag: typeFeed,
			Dimensions: []cg.FacetDimension{
				{Key: facetFolder, Label: "Folder", Kind: cg.FacetCategorical},
				{
					Key: facetActive, Label: "Active", Kind: cg.FacetCategorical,
					Values: []cg.FacetValue{{Key: "active"}, {Key: "inactive"}},
				},
			},
		},
	}
}

// FacetCounts summarizes node's children in one shot from the in-memory
// index — the preferred size-agnostic path (RFC 0012 §4.1). It handles
// every container that lists feed or story nodes; any other node (a
// story's own leaves, an unknown node) returns ok=false so the consumer
// falls back to folding Node.Facets over ListRoots.
func (Plugin) FacetCounts(
	ctx context.Context, node *url.URL, filter cg.FacetFilter,
) (cg.FacetResult, bool, error) {
	if node == nil {
		return cg.FacetResult{}, false, fmt.Errorf("newsblur plugin: FacetCounts requires a node URI")
	}
	if index == nil {
		return cg.FacetResult{}, false, fmt.Errorf("newsblur plugin: index not initialized")
	}

	segs := pathSegments(node)
	switch {
	case len(segs) == 1 && segs[0] == "feeds":
		feeds, err := index.Feeds(ctx)
		if err != nil {
			return cg.FacetResult{}, false, err
		}
		return feedFacetCounts(feeds, filter), true, nil
	case len(segs) == 1 && segs[0] == "stories":
		stories, err := index.Stories()
		if err != nil {
			return cg.FacetResult{}, false, err
		}
		return storyFacetCounts(stories, filter), true, nil
	case len(segs) == 2 && segs[0] == "feed":
		id, err := strconv.Atoi(segs[1])
		if err != nil {
			return cg.FacetResult{}, false, fmt.Errorf("newsblur plugin: invalid feed id %q: %w", segs[1], err)
		}
		stories, err := index.FeedStories(id)
		if err != nil {
			return cg.FacetResult{}, false, err
		}
		return storyFacetCounts(stories, filter), true, nil
	case len(segs) == 2 && segs[0] == "tag":
		stories, err := index.StoriesByTag(segs[1])
		if err != nil {
			return cg.FacetResult{}, false, err
		}
		return storyFacetCounts(stories, filter), true, nil
	default:
		return cg.FacetResult{}, false, nil
	}
}

// FacetVersion returns a cheap change token for a node's facet-relevant
// content, so the cutting-garden MCP server's summary memoization (RFC
// 0012 §11) recomputes only when something moved instead of on every
// read. Two wires, both cheaper than FacetCounts:
//
//   - Story-listing containers (stories/tags/tag/{tag}) share one
//     corpus-wide token: the local cache manifest file's mtime, which
//     changes exactly when a `nebulous fetch` run writes new data (one
//     os.Stat, no manifest content scan).
//   - A single feed's story-subset container (feed/{id}) tokens on that
//     feed's own NewsBlur-reported NT (unread count): it moves on both
//     new-story-arrival and read-state change, and `read` is itself a
//     declared story facet dimension, so NT is a legitimate proxy, not
//     just a correlate.
//   - `feeds` (the feed-listing container) also uses the manifest mtime —
//     feed metadata (folder/active) only refreshes via the same fetch
//     runs.
//
// A spuriously-changing token only costs an extra recompute; nebulous's
// fetch cadence is already periodic, so "stale until the next fetch"
// isn't a new class of staleness here — it's the same lag every other
// nebulous surface already has.
func (Plugin) FacetVersion(ctx context.Context, node *url.URL) (string, bool, error) {
	if node == nil {
		return "", false, fmt.Errorf("newsblur plugin: FacetVersion requires a node URI")
	}
	if index == nil {
		return "", false, fmt.Errorf("newsblur plugin: index not initialized")
	}

	segs := pathSegments(node)
	switch {
	case len(segs) == 1 && (segs[0] == "stories" || segs[0] == "tags" || segs[0] == "feeds"):
		return manifestVersionToken(index.ManifestPath())
	case len(segs) == 2 && segs[0] == "tag":
		return manifestVersionToken(index.ManifestPath())
	case len(segs) == 2 && segs[0] == "feed":
		feeds, err := index.Feeds(ctx)
		if err != nil {
			return "", false, err
		}
		for _, f := range feeds {
			if f.ID == segs[1] {
				return strconv.Itoa(f.NT), true, nil
			}
		}
		return "", false, nil
	default:
		return "", false, nil
	}
}

// manifestVersionToken stats the local cache manifest and formats its
// mtime as an opaque token. ok=false (no error) when the manifest hasn't
// been written yet (a fresh install before the first `nebulous fetch`).
func manifestVersionToken(manifestPath string) (string, bool, error) {
	if manifestPath == "" {
		return "", false, nil
	}
	fi, err := os.Stat(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return strconv.FormatInt(fi.ModTime().UnixNano(), 10), true, nil
}

// ResolveFacetLabels resolves the feed dimension's opaque feed-id keys to
// their feed titles: the live subscription list first, falling back to
// SeenFeedTitles' accumulating registry for a feed no longer subscribed —
// stories starred years ago routinely reference feeds since unsubscribed
// from, which the live list alone can never resolve (nebulous#49). No
// cutting-garden consumer resolves labels yet (cutting-garden#124); this
// exists so the newsblur plugin is ready the moment one does.
func (Plugin) ResolveFacetLabels(
	ctx context.Context, dimension string, keys []string,
) (map[string]string, error) {
	if dimension != facetFeed || index == nil {
		return nil, nil
	}
	feeds, err := index.Feeds(ctx)
	if err != nil {
		return nil, err
	}
	titleByID := make(map[string]string, len(feeds))
	for _, f := range feeds {
		titleByID[f.ID] = f.Title
	}
	// Fetched once up front rather than per key: keys can run into the
	// hundreds (every unresolved feed id in a large corpus), and this
	// registry only grows larger over a store's lifetime.
	seenTitles := index.SeenFeedTitles()
	labels := make(map[string]string, len(keys))
	for _, k := range keys {
		if title, ok := titleByID[k]; ok {
			labels[k] = title
			continue
		}
		if title, ok := seenTitles[k]; ok {
			labels[k] = title
		}
	}
	return labels, nil
}

func storyFacetCounts(stories []tools.StoryRef, filter cg.FacetFilter) cg.FacetResult {
	summary := cg.FacetSummary{}
	// One evaluation instant for the whole summary (§11.3): stories are
	// bucketed against the same "now" regardless of where they fall in
	// the loop, so a summary straddling local midnight can't split one
	// atomic FacetCounts call across two different "today"s.
	now := ageBandNow()
	var matched bool
	for _, s := range stories {
		facets := storyFacetValues(s, now)
		if !filter.Matches(facets) {
			continue
		}
		liftFacets(summary, facets)
		matched = true
	}
	// §11.3 emission rule: every element FacetCounts summarizes here is
	// a story (the switch in FacetCounts only ever hands storyFacetCounts
	// a story list), so any match at all means the volatile age_band
	// dimension must be present with informative zeros — the
	// memoization layer's expiry trigger stays correct even when every
	// bucket is currently empty.
	if matched {
		ensureAgeBandPresence(summary)
	}
	return cg.FacetResult{Summary: summary, Complete: true}
}

func storyFacetValues(s tools.StoryRef, now time.Time) map[string][]cg.FacetValue {
	facets := map[string][]cg.FacetValue{
		facetYear: {{Key: strconv.Itoa(s.Year), Order: int64(s.Year)}},
		facetFeed: {{Key: strconv.Itoa(s.FeedID)}},
		facetRead: {{Key: readKey(s.Read)}},
	}
	if len(s.UserTags) > 0 {
		facets[facetUserTag] = tagFacetValues(s.UserTags)
	}
	if len(s.Tags) > 0 {
		facets[facetStoryTag] = tagFacetValues(s.Tags)
	}
	// Unlike caldav's due_band (gated to open VTODOs — "overdue" is
	// meaningless once a task is done), age_band applies to every
	// story unconditionally: a read story is exactly as old as an
	// unread one, and `read` is already its own orthogonal dimension
	// (§6 FacetFilter composes them) rather than a gate on this one.
	if key, order := ageBandOf(s.Date, now); key != "" {
		facets[facetAgeBand] = []cg.FacetValue{{Key: key, Order: order}}
	}
	return facets
}

// ageBandOf buckets a story's published date against the current
// host-local day — the RFC 0012 §11.3 quantized evaluation instant, so
// summaries computed at different times within one day agree exactly.
// Empty key when date is the zero value (no published date recorded).
func ageBandOf(date time.Time, now time.Time) (key string, order int64) {
	if date.IsZero() {
		return "", 0
	}
	loc := now.Location()
	published := date.In(loc)
	// Built in time.UTC (never DST) rather than loc: since only the
	// calendar y/m/d is significant here, representing both day-starts
	// in a fixed offset makes every day exactly 24h, so the day count
	// below is exact with no DST rounding needed.
	day := time.Date(published.Year(), published.Month(), published.Day(), 0, 0, 0, 0, time.UTC)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	days := int(today.Sub(day) / (24 * time.Hour))
	switch {
	case days <= 0:
		key = ageBandToday
	case days <= 6:
		key = ageBandThisWeek
	case days <= 30:
		key = ageBandThisMonth
	default:
		key = ageBandOlder
	}
	return key, ageBandOrder[key]
}

// ensureAgeBandPresence implements the RFC 0012 §11.3 emission rule:
// whenever the summarized set contains stories, the volatile age_band
// dimension is present with informative zeros — the memoization
// layer's expiry trigger stays correct even when every bucket is
// currently empty (an empty bucket can fill purely by time passing; a
// story-free subtree can only gain a story via a data change, which the
// manifest-mtime token already catches).
func ensureAgeBandPresence(summary cg.FacetSummary) {
	hist := summary[facetAgeBand]
	if hist == nil {
		hist = cg.FacetHistogram{}
		summary[facetAgeBand] = hist
	}
	for key := range ageBandOrder {
		if _, ok := hist[key]; !ok {
			hist[key] = 0
		}
	}
}

func tagFacetValues(tags []string) []cg.FacetValue {
	values := make([]cg.FacetValue, 0, len(tags))
	for _, t := range tags {
		values = append(values, cg.FacetValue{Key: t})
	}
	return values
}

func readKey(read bool) string {
	if read {
		return "read"
	}
	return "unread"
}

func feedFacetCounts(feeds []tools.FeedRef, filter cg.FacetFilter) cg.FacetResult {
	summary := cg.FacetSummary{}
	for _, f := range feeds {
		facets := feedFacetValues(f)
		if !filter.Matches(facets) {
			continue
		}
		liftFacets(summary, facets)
	}
	return cg.FacetResult{Summary: summary, Complete: true}
}

func feedFacetValues(f tools.FeedRef) map[string][]cg.FacetValue {
	facets := map[string][]cg.FacetValue{
		facetActive: {{Key: activeKey(f.Active)}},
	}
	if f.Folder != "" {
		facets[facetFolder] = []cg.FacetValue{{Key: f.Folder}}
	}
	return facets
}

func activeKey(active bool) string {
	if active {
		return "active"
	}
	return "inactive"
}

// liftFacets folds one node's facet values into summary: +1 per
// (dimension, value key).
func liftFacets(summary cg.FacetSummary, facets map[string][]cg.FacetValue) {
	for dim, values := range facets {
		hist := summary[dim]
		if hist == nil {
			hist = cg.FacetHistogram{}
			summary[dim] = hist
		}
		for _, v := range values {
			hist[v.Key]++
		}
	}
}
