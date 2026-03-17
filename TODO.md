- [ ] purse-first go-cli-framework skill references `go-lib-mcp` throughout ---
  should say `go-mcp` (module is
  `github.com/amarbel-llc/purse-first/libs/go-mcp`)
- [ ] Add `/api/login` support: accept NEWSBLUR_USER + NEWSBLUR_PASS env vars,
  auto-login and persist session cookie (currently requires pre-obtained session
  cookie via NEWSBLUR_TOKEN)
- [x] Live verification against NewsBlur API (Task 15 from impl plan)
- [x] Add output.LimitText to feed_list, feed_stats, feed_autocomplete,
  story_feed, story_river (all exceeded 90K+ chars)
- [ ] Add spec + code quality review pass over all tool implementations
- [x] Index story_content (HTML stripped) in saved_story_index for deeper
  content search
- [ ] Move nebulous cache/index from \~/.cache/nebulous to
  \~/.local/share/nebulous (XDG_DATA_HOME) --- it's an index, not a throwaway
  cache
- [ ] Switch blob-storage portion of index to madder (github:amarbel-llc/dodder)
- [x] Persist index caches to \~/.cache/nebulous to avoid rebuilding on every
  session start
- [ ] Explore content-based cache addressing (etags or digests) for response
  cache freshness
- [x] Evaluate starred story word-index + query + cache workflows: are
  `starred_story_index_query` (local index) and `story_starred` with `query`
  param redundant? Consider index build cost (\~100 API calls, rate limit risk)
  vs server-side search
- [ ] `fetch`: retry rate-limited items instead of skipping --- currently
  `fetchWithBackoff` continues to the next item on 429, silently skipping it
  until the next run
- [ ] `fetch`: persist learned adaptive backoff base between runs --- currently
  resets to default on each invocation, losing rate limit knowledge
- [x] FDR: cache-as-persistent-index architecture --- MCP server is now fully
  offline (reads only from cache), `fetch` is the sole ingestion pipeline, cache
  is really a persistent index. Document design intent, data flow, and
  implications for the XDG migration
- [ ] rename `fetch` to `sync`
- [ ] add logs for `fetch` to help debug
- [ ] Migrate user_tags to dodder tag structure
- [ ] Sync non-starred stories in fetch (feed stories, unread) to enable full
  story_query filtering
- [ ] Add feed title to story facets by_feed entries (requires cross-referencing
  feed store)
- [ ] Add MCP prompt templates for progressive-disclosure fan-out workflows. The
  resource hierarchy (river → river/{page} → story/{hash} →
  story/{hash}/content) works well for single-context reads but requires the
  caller to know the traversal strategy. Prompt templates should encode these
  strategies so a fresh session can execute them without prior context.
  Candidates: (1) "summarize all stories" --- reads nebulous://river to get page
  count, fans out across river/{page} resources reading story summaries,
  synthesizes topic clusters; (2) "deep read" --- given a topic or keyword, uses
  starred_story_index_query to find matching hashes, reads story/{hash} metadata
  to assess content availability (has_content, content_tokens), then selectively
  reads story/{hash}/content or story/{hash}/original for the richest
  sources; (3) "feed discovery" --- reads feed_index, groups by folder/topic,
  summarizes subscription landscape. Each prompt should return structured
  instructions (not data) telling the model which resources to read and in what
  order, so the progressive-disclosure strategy is self-documenting rather than
  requiring the caller to reverse-engineer it from resource descriptions
- [ ] File issue: Claude Code subagents cannot use ReadMcpResourceTool --- MCP
  resources are designed as a context-saving progressive-disclosure strategy
  (compact index → per-page summaries → full story content), but subagents only
  have access to MCP tools, not MCP resources. This forces the parent agent to
  fetch all resource data itself and pass it as text to subagents, defeating the
  context-saving benefit. Ideal fix: subagents should inherit
  ReadMcpResourceTool access from the parent session
