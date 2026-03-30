---
date: 2026-03-30
promotion-criteria: |
  Promote to proposed when: (1) tool choice is finalized based on fidelity
  review of test outputs, (2) storage strategy is decided (alongside cache vs
  separate directory), (3) integration point in fetch pipeline is designed.
status: exploring
---

# Periodic Story Snapshotting

## Problem Statement

Nebulous caches article text extracted by NewsBlur, but this content is lossy
--- it strips images, CSS, layout, and interactive elements. Many modern
articles are only fully readable with their original styling, embedded media,
and JS-rendered content. When the original URL goes offline (link rot, paywall
changes, site shutdowns), the extracted text is all that remains. A
full-fidelity snapshot preserved as a self-contained HTML file would make
starred stories permanently accessible regardless of the original site's
availability.

### Research Context

An exhaustive scan of 26k+ starred stories across 2021--2026 surfaced 26
relevant articles on this topic. The core technical candidates for single-file
web archiving were evaluated in depth:

**Tools evaluated:**

  ---------------------------------------------------------------------------------------------------
  Tool              Type         License       JS Rendering        File Size        Speed
  ----------------- ------------ ------------- ------------------- ---------------- -----------------
  **Monolith**      Rust CLI     CC0 (public   No (static HTML     Larger (fetches  Fast
                    binary       domain)       fetch)              all srcset       (\~2-7s/page)
                                                                   variants,        
                                                                   responsive       
                                                                   images)          

  **SingleFile      Node.js +    AGPL-3.0      Yes (full browser   Smaller          Slower
  CLI**             headless                   render)             (captures only   (\~10-12s/page,
                    Chromium                                       rendered         browser startup)
                                                                   content)         

  **Gwtar**         PHP script   N/A           Requires SingleFile Similar on disk, Two-step pipeline
                    (takes       (reference    first               lazy-loads on    
                    SingleFile   impl)                             serve            
                    input)                                                          

  **SingleFileZ**   SingleFile   AGPL-3.0      Yes                 \~30% smaller    Same as
                    variant                                        than SingleFile  SingleFile
                                                                   (ZIP             
                                                                   compression)     
  ---------------------------------------------------------------------------------------------------

**Empirical test results (3 representative URLs):**

  Page                         Monolith   SingleFile   Ratio
  ---------------------------- ---------- ------------ -------
  tonsky.me (static blog)      3.9 MiB    932 KiB      4.2x
  gwern.net (complex layout)   9.5 MiB    3.2 MiB      3.0x
  arstechnica.com (JS news)    7.3 MiB    1.2 MiB      6.1x

Both tools produced visually faithful archives for all three test pages. Key
observations:

- **SingleFile is dramatically smaller** because it captures only what the
  browser actually renders (strips ads, tracking, unused responsive variants)
- **Monolith is fast and dependency-light** (single binary, no browser needed)
  but eagerly fetches every referenced resource including all `srcset` variants
- **SingleFile requires headless Chromium** (\~400MB dependency, \~10s/page
  including browser startup)
- **Monolith cannot capture JS-rendered pages** --- content that requires
  JavaScript to render (SPAs, some paywalled readers) will be missing. The
  workaround is piping `chromium --headless --dump-dom` output to monolith,
  which reintroduces the Chromium dependency
- **Neither tool captures video** --- YouTube and similar streaming video pages
  produce a frozen player. Video archival requires a different tool (`yt-dlp`)

**License considerations:**

- Monolith's CC0 has zero restrictions --- ideal for integration
- SingleFile's AGPL-3.0 has network copyleft: if nebulous is run as a service,
  the source must be offered to users. Shelling out to an AGPL binary as a
  subprocess is generally considered safe (not a "combined work"), but this is
  legally untested. The common industry approach is to either avoid AGPL
  entirely or treat AGPL tools as optional user-supplied dependencies
- Given that JS rendering matters for a meaningful fraction of modern sites, the
  practical choice may be to support both: monolith as default (fast, no deps,
  CC0) with SingleFile as an optional upgrade when Chromium is available

**Formats not recommended:**

- **Gwtar**: Too immature (v1, Jan 2026, single PHP script). Does not work from
  `file://` URLs. Revisit in a year.
- **MHTML**: No good CLI tooling. Requires downloading everything (not lazy).
  Browser support is inconsistent.
- **WARC/WACZ**: Requires a complex viewer (ReplayWeb). Not self-contained in
  the "open in browser" sense.

### Open Questions

- What fraction of starred story URLs are JS-dependent for article content? This
  determines whether monolith-only is sufficient or SingleFile is needed.
- Should snapshots run during `nebulous fetch` or as a separate command? Fetch
  is already rate-limited; adding browser rendering would slow it significantly.
- Storage budget: at 1-4 MiB per article, 26k stories = 26-104 GiB. Is this
  acceptable? Should only starred/tagged stories be snapshotted?
- Should snapshots be exposed as an MCP resource (e.g.,
  `nebulous://story/{hash}/snapshot`)?
- Retry strategy for pages that fail to snapshot (rate limits, captchas,
  paywalled content).

## More Information

- [GitHub Issue #5: Explore RAG for exhaustive story content
  search](https://github.com/amarbel-llc/nebulous/issues/5) --- related:
  semantic search over story content would complement snapshots
- [Monolith](https://github.com/Y2Z/monolith) --- CC0 Rust CLI for single-file
  HTML archival
- [SingleFile CLI](https://github.com/gildas-lormeau/single-file-cli) --- AGPL
  Node.js + Chromium CLI
- [Gwtar](https://gwern.net/gwtar) --- experimental self-extracting HTML archive
  format
- Test outputs: `just archive-test` produces comparison files in
  `/tmp/nebulous-archive-test/`
