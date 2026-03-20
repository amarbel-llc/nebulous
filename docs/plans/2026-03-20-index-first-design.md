# Index-First Architecture: Design

## Problem

The MCP server's query tools are structurally blind. The word index can find
stories by keyword but cannot answer structural queries like "all stories from
2024" or "stories tagged zz-nyc." Dates, tags, and feed IDs are not indexed.
Agents must guess keywords or paginate through `story_starred` at 10/page to
perform exhaustive analysis.

Meanwhile, the server has evolved into a two-phase architecture (sync + serve)
where the MCP server reads exclusively from a local persistent store. But 10
read-only tools still hit the NewsBlur API directly, creating an inconsistency
between the tools that use the index and those that bypass it.

## Design

### Data Model: Flat Story Store

Replace `savedStoryIndex` (word map only) with a `storyStore` — a flat slice of
typed records built from the persistent store at startup.

```go
type storyRecord struct {
    Hash          string
    Title         string
    Authors       string
    FeedID        int
    Date          time.Time
    Year          int
    Month         int
    Permalink     string
    Tags          []string       // story_tags
    UserTags      []string       // user_tags
    Words         map[string]bool
    HasContent    bool
    ContentTokens int
}

type storyStore struct {
    client  *newsblur.Client
    once    sync.Once
    stories []storyRecord
    words   map[string][]*storyRecord  // acceleration index
    err     error
}
```

The `build()` method walks `CachedStarredStoryPage` pages (same as today), but
parses each story into a `storyRecord` with typed fields. The word index is a
secondary acceleration structure pointing into the same records.

The `feedIndex` gets the same treatment — a `feedStore` with `[]feedRecord` and
a word acceleration layer built from the cached `feed_list` response.

Stories are stories — starred, read, unread are fields on the record, not
separate collections. Currently `fetch` only syncs starred story pages, so all
indexed stories have `starred: true`. The model is ready for when `fetch` learns
to sync feed stories too.

### Query Tool: `story_query`

One tool replaces `starred_story_index_query`, `story_starred`, `story_feed`,
`story_river`, `story_unread_hashes`, and `story_original_text`:

```
story_query
  params:
    words:    []string  (optional, OR-union within, AND with other filters)
    year:     int       (optional)
    month:    int       (optional, requires year)
    tag:      string    (optional, matches user_tags)
    feed_id:  int       (optional)
    starred:  bool      (optional)
    read:     bool      (optional)
    offset:   int       (optional, default 0)
    limit:    int       (optional, default 100)
```

Returns compact summaries: hash, title, feed_id, date, user_tags, permalink.
No content, no HTML. Sorted by date descending.

Each provided filter narrows the result set (AND). Words are OR-union within
themselves, then AND with structural filters.

Examples:
- All starred stories from 2024: `{year: 2024}`
- NYC stories from March 2024: `{tag: "zz-nyc", year: 2024, month: 3}`
- Go articles: `{words: ["golang", "go"]}`
- Everything (paginated): `{offset: 0, limit: 100}`

### Discovery Resources

```
nebulous://stories/facets
  {
    total_stories: 1523,
    by_year: {"2024": 342, "2023": 289, ...},
    by_tag: {"interests": 520, "news": 180, "zz-nyc": 45, ...},
    by_feed: {"6327282": {"count": 400, "title": "Hacker News"}, ...},
    by_status: {"starred": 1523, "read": 890, "unread": 312},
    years: [2024, 2023, 2022, ...]
  }

nebulous://feeds/facets
  {
    total_feeds: 35,
    by_folder: {"Tech": 12, "NYC": 5, ...},
    active: 30,
    inactive: 5
  }
```

These let agents plan queries without fetching raw data.

### Removed API-Coupled Read Tools

| Removed Tool               | Replacement                                      |
|----------------------------|--------------------------------------------------|
| `feed_list`                | `nebulous://feeds/facets` + `feed_query`          |
| `feed_stats`               | `nebulous://feed/{id}` (from store)               |
| `feed_autocomplete`        | `feed_query`                                      |
| `feed_search`              | `feed_query`                                      |
| `story_feed`               | `story_query` with `feed_id`                      |
| `story_river`              | `story_query` with `feed_id`                      |
| `story_starred`            | `story_query` with `starred: true`                |
| `story_unread_hashes`      | `story_query` with `read: false`                  |
| `story_original_text`      | `nebulous://story/{hash}/original`                |
| `starred_story_index_query`| `story_query` with `words`                        |

### Remaining Tool Surface

**Query tools (read from store):**
- `story_query` — single query entry point
- `feed_query` — word search over feeds (already index-based)

**Mutation tools (hit API):**
- `mark_read`, `mark_unread`, `star`, `unstar`, `mark_feed_read`, `mark_all_read`
- `subscribe`, `unsubscribe`, `rename_feed`
- `folder_create`, `folder_rename`, `folder_delete`, `move_feed`, `move_folder`
- `opml_export`, `opml_import`

**Resources (read from store):**
- `nebulous://stories/facets` — aggregate counts
- `nebulous://feeds/facets` — feed overview
- `nebulous://story/{hash}` — compact metadata
- `nebulous://story/{hash}/content` — cached text
- `nebulous://story/{hash}/original` — full article (hits API)
- `nebulous://feed/{id}` — feed metadata
- `nebulous://feed/{id}/stories` — feed stories (from store when available)

### Agent Query Pipeline

```
nebulous://stories/facets → story_query(filters) → story/{hash} → story/{hash}/content → story/{hash}/original
nebulous://feeds/facets → feed_query(words) → feed/{id}
```

Server instructions updated to:
> Read nebulous://stories/facets first to understand the data shape, then use
> story_query to filter by year, tag, feed, words, or any combination. Use
> nebulous://story/{hash} for metadata, story/{hash}/content for text,
> story/{hash}/original for full articles. Delegate bulk story reads to
> subagents.

### Rollback

Hard cutover. Rollback via `git revert`.

### Future

- Tag model migrates to dodder's structure
- `fetch` learns to sync non-starred stories (feed stories, unread)
- Move persistent store from `~/.cache/nebulous` to `~/.local/share/nebulous`
