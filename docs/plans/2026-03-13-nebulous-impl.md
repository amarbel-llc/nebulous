# Nebulous Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Build a Go MCP server that exposes the NewsBlur API via go-mcp's `command.App`.

**Architecture:** Standalone Go module with two internal packages: `newsblur` (HTTP client wrapping the NewsBlur REST API) and `tools` (MCP tool registration and handlers). Entry point creates a `newsblur.Client` from `NEWSBLUR_TOKEN` env var, registers all tools, starts MCP server on stdio.

**Tech Stack:** Go, go-mcp (`github.com/amarbel-llc/purse-first/libs/go-mcp`), `net/http`, Nix flake with purse-first devenvs.

**Rollback:** N/A — new project.

---

### Task 1: Project Scaffold

**Files:**
- Create: `cmd/nebulous/main.go`
- Create: `go.mod`
- Create: `flake.nix`
- Create: `.envrc`

**Step 1: Create go.mod**

```
module github.com/friedenberg/nebulous

go 1.23.0

require github.com/amarbel-llc/purse-first/libs/go-mcp v0.0.3
```

Run: `cd /home/sasha/eng/repos/nebulous && go mod tidy` (will fetch go-mcp and populate go.sum)

**Step 2: Create flake.nix**

```nix
{
  description = "NewsBlur MCP server";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/6d41bc27aaf7b6a3ba6b169db3bd5d6159cfaa47";
    nixpkgs-master.url = "github:NixOS/nixpkgs/5b7e21f22978c4b740b3907f3251b470f466a9a2";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    go = {
      url = "github:amarbel-llc/purse-first?dir=devenvs/go";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    shell = {
      url = "github:amarbel-llc/purse-first?dir=devenvs/shell";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      utils,
      go,
      shell,
      nixpkgs-master,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [
            go.overlays.default
          ];
        };

        version = "0.1.0";

        nebulous = pkgs.buildGoModule {
          pname = "nebulous";
          inherit version;
          src = ./.;
          vendorHash = null; # Will update after go mod tidy

          subPackages = [ "cmd/nebulous" ];

          postInstall = ''
            $out/bin/nebulous generate-plugin $out
          '';

          meta = with pkgs.lib; {
            description = "NewsBlur MCP server";
            homepage = "https://github.com/friedenberg/nebulous";
            license = licenses.mit;
          };
        };
      in
      {
        packages = {
          default = nebulous;
          inherit nebulous;
        };

        devShells.default = pkgs.mkShell {
          inputsFrom = [
            go.devShells.${system}.default
            shell.devShells.${system}.default
          ];
        };
      }
    );
}
```

**Step 3: Create .envrc**

```
use flake
```

**Step 4: Create minimal main.go**

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "nebulous: not yet implemented")
	os.Exit(1)
}
```

**Step 5: Run go mod tidy and verify build**

Run: `cd /home/sasha/eng/repos/nebulous && go mod tidy`
Run: `go build ./cmd/nebulous`
Expected: binary builds without errors.

**Step 6: Commit**

```
git add go.mod go.sum cmd/nebulous/main.go flake.nix .envrc
git commit -m "Scaffold nebulous project"
```

---

### Task 2: NewsBlur HTTP Client

**Files:**
- Create: `internal/newsblur/client.go`

**Step 1: Create the client**

```go
package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const DefaultBaseURL = "https://www.newsblur.com"

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewClient(token string) *Client {
	return &Client{
		baseURL:    DefaultBaseURL,
		token:      token,
		httpClient: &http.Client{},
	}
}

func (c *Client) get(ctx context.Context, path string, params url.Values) (json.RawMessage, error) {
	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Cookie", "newsblur_sessionid="+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return json.RawMessage(body), nil
}

func (c *Client) post(ctx context.Context, path string, form url.Values) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Cookie", "newsblur_sessionid="+c.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return json.RawMessage(body), nil
}
```

**Step 2: Verify it compiles**

Run: `go build ./internal/newsblur/...`
Expected: no errors.

**Step 3: Commit**

```
git add internal/newsblur/client.go
git commit -m "Add NewsBlur HTTP client with get/post helpers"
```

---

### Task 3: Feed API Methods

**Files:**
- Create: `internal/newsblur/feeds.go`

**Step 1: Implement feed methods**

```go
package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

func (c *Client) Feeds(ctx context.Context, includeFavicons, flat, updateCounts bool) (json.RawMessage, error) {
	params := url.Values{}
	if includeFavicons {
		params.Set("include_favicons", "true")
	}
	if flat {
		params.Set("flat", "true")
	}
	if updateCounts {
		params.Set("update_counts", "true")
	}
	return c.get(ctx, "/reader/feeds", params)
}

func (c *Client) SearchFeed(ctx context.Context, address string) (json.RawMessage, error) {
	params := url.Values{"address": {address}}
	return c.get(ctx, "/rss_feeds/search_feed", params)
}

func (c *Client) FeedStats(ctx context.Context, feedID int) (json.RawMessage, error) {
	return c.get(ctx, fmt.Sprintf("/rss_feeds/statistics/%d", feedID), nil)
}

func (c *Client) FeedAutocomplete(ctx context.Context, term string) (json.RawMessage, error) {
	params := url.Values{"term": {term}}
	return c.get(ctx, "/rss_feeds/feed_autocomplete", params)
}
```

**Step 2: Verify it compiles**

Run: `go build ./internal/newsblur/...`

**Step 3: Commit**

```
git add internal/newsblur/feeds.go
git commit -m "Add feed API methods"
```

---

### Task 4: Story API Methods

**Files:**
- Create: `internal/newsblur/stories.go`

**Step 1: Implement story methods**

```go
package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func (c *Client) StoriesFeed(ctx context.Context, feedID int, page int, order, readFilter, query string) (json.RawMessage, error) {
	params := url.Values{}
	if page > 0 {
		params.Set("page", fmt.Sprintf("%d", page))
	}
	if order != "" {
		params.Set("order", order)
	}
	if readFilter != "" {
		params.Set("read_filter", readFilter)
	}
	if query != "" {
		params.Set("query", query)
	}
	return c.get(ctx, fmt.Sprintf("/reader/feed/%d", feedID), params)
}

func (c *Client) StoriesRiver(ctx context.Context, feedIDs []int, page int) (json.RawMessage, error) {
	params := url.Values{}
	for _, id := range feedIDs {
		params.Add("feeds", fmt.Sprintf("%d", id))
	}
	if page > 0 {
		params.Set("page", fmt.Sprintf("%d", page))
	}
	return c.get(ctx, "/reader/river_stories", params)
}

func (c *Client) StoriesStarred(ctx context.Context, page int, tag, query string) (json.RawMessage, error) {
	params := url.Values{}
	if page > 0 {
		params.Set("page", fmt.Sprintf("%d", page))
	}
	if tag != "" {
		params.Set("tag", tag)
	}
	if query != "" {
		params.Set("query", query)
	}
	return c.get(ctx, "/reader/starred_stories", params)
}

func (c *Client) UnreadStoryHashes(ctx context.Context, feedIDs []int) (json.RawMessage, error) {
	params := url.Values{}
	for _, id := range feedIDs {
		params.Add("feed_id", fmt.Sprintf("%d", id))
	}
	return c.get(ctx, "/reader/unread_story_hashes", params)
}

func (c *Client) OriginalText(ctx context.Context, storyHash string) (json.RawMessage, error) {
	params := url.Values{"story_hash": {storyHash}}
	return c.get(ctx, "/rss_feeds/original_text", params)
}

// StoryHashes converts a string slice to a comma-separated feed_id params format
// used by multiple endpoints.
func joinInts(ids []int) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = fmt.Sprintf("%d", id)
	}
	return strings.Join(s, ",")
}
```

**Step 2: Verify it compiles**

Run: `go build ./internal/newsblur/...`

**Step 3: Commit**

```
git add internal/newsblur/stories.go
git commit -m "Add story API methods"
```

---

### Task 5: Reader API Methods (mark read/unread/star)

**Files:**
- Create: `internal/newsblur/reader.go`

**Step 1: Implement reader methods**

```go
package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func (c *Client) MarkStoriesRead(ctx context.Context, storyHashes []string) (json.RawMessage, error) {
	form := url.Values{}
	for _, h := range storyHashes {
		form.Add("story_hash", h)
	}
	return c.post(ctx, "/reader/mark_story_hashes_as_read", form)
}

func (c *Client) MarkStoryUnread(ctx context.Context, storyHash string) (json.RawMessage, error) {
	form := url.Values{"story_hash": {storyHash}}
	return c.post(ctx, "/reader/mark_story_hash_as_unread", form)
}

func (c *Client) StarStory(ctx context.Context, storyHash string, userTags []string) (json.RawMessage, error) {
	form := url.Values{"story_hash": {storyHash}}
	if len(userTags) > 0 {
		form.Set("user_tags", strings.Join(userTags, ","))
	}
	return c.post(ctx, "/reader/mark_story_hash_as_starred", form)
}

func (c *Client) UnstarStory(ctx context.Context, storyHash string) (json.RawMessage, error) {
	form := url.Values{"story_hash": {storyHash}}
	return c.post(ctx, "/reader/mark_story_hash_as_unstarred", form)
}

func (c *Client) MarkFeedRead(ctx context.Context, feedID int) (json.RawMessage, error) {
	form := url.Values{"feed_id": {fmt.Sprintf("%d", feedID)}}
	return c.post(ctx, "/reader/mark_feed_as_read", form)
}

func (c *Client) MarkAllRead(ctx context.Context, days int) (json.RawMessage, error) {
	form := url.Values{}
	if days > 0 {
		form.Set("days", fmt.Sprintf("%d", days))
	}
	return c.post(ctx, "/reader/mark_all_as_read", form)
}
```

**Step 2: Verify it compiles**

Run: `go build ./internal/newsblur/...`

**Step 3: Commit**

```
git add internal/newsblur/reader.go
git commit -m "Add reader API methods (mark read/unread/star)"
```

---

### Task 6: Subscription and Folder API Methods

**Files:**
- Create: `internal/newsblur/subscriptions.go`
- Create: `internal/newsblur/folders.go`

**Step 1: Implement subscription methods**

```go
package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

func (c *Client) Subscribe(ctx context.Context, feedURL, folder string) (json.RawMessage, error) {
	form := url.Values{"url": {feedURL}}
	if folder != "" {
		form.Set("folder", folder)
	}
	return c.post(ctx, "/reader/add_url", form)
}

func (c *Client) Unsubscribe(ctx context.Context, feedID int, inFolder string) (json.RawMessage, error) {
	form := url.Values{"feed_id": {fmt.Sprintf("%d", feedID)}}
	if inFolder != "" {
		form.Set("in_folder", inFolder)
	}
	return c.post(ctx, "/reader/delete_feed", form)
}

func (c *Client) RenameFeed(ctx context.Context, feedID int, feedTitle string) (json.RawMessage, error) {
	form := url.Values{
		"feed_id":    {fmt.Sprintf("%d", feedID)},
		"feed_title": {feedTitle},
	}
	return c.post(ctx, "/reader/rename_feed", form)
}
```

**Step 2: Implement folder methods**

```go
package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

func (c *Client) CreateFolder(ctx context.Context, folderName, parentFolder string) (json.RawMessage, error) {
	form := url.Values{"folder": {folderName}}
	if parentFolder != "" {
		form.Set("parent_folder", parentFolder)
	}
	return c.post(ctx, "/reader/add_folder", form)
}

func (c *Client) RenameFolder(ctx context.Context, folderName, newFolderName, inFolder string) (json.RawMessage, error) {
	form := url.Values{
		"folder_name":     {folderName},
		"new_folder_name": {newFolderName},
	}
	if inFolder != "" {
		form.Set("in_folder", inFolder)
	}
	return c.post(ctx, "/reader/rename_folder", form)
}

func (c *Client) DeleteFolder(ctx context.Context, folderName string) (json.RawMessage, error) {
	form := url.Values{"folder_name": {folderName}}
	return c.post(ctx, "/reader/delete_folder", form)
}

func (c *Client) MoveFeed(ctx context.Context, feedID int, inFolder, toFolder string) (json.RawMessage, error) {
	form := url.Values{
		"feed_id":   {fmt.Sprintf("%d", feedID)},
		"in_folder": {inFolder},
		"to_folder": {toFolder},
	}
	return c.post(ctx, "/reader/move_feed_to_folder", form)
}

func (c *Client) MoveFolder(ctx context.Context, folderName, inFolder, toFolder string) (json.RawMessage, error) {
	form := url.Values{
		"folder_name": {folderName},
		"in_folder":   {inFolder},
		"to_folder":   {toFolder},
	}
	return c.post(ctx, "/reader/move_folder_to_folder", form)
}
```

**Step 3: Verify it compiles**

Run: `go build ./internal/newsblur/...`

**Step 4: Commit**

```
git add internal/newsblur/subscriptions.go internal/newsblur/folders.go
git commit -m "Add subscription and folder API methods"
```

---

### Task 7: OPML Import/Export API Methods

**Files:**
- Create: `internal/newsblur/import_export.go`

**Step 1: Implement import/export methods**

```go
package newsblur

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

func (c *Client) OPMLExport(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/import/opml_export", nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Cookie", "newsblur_sessionid="+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}

func (c *Client) OPMLImport(ctx context.Context, opmlContent string) (json.RawMessage, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	part, err := w.CreateFormFile("file", "import.opml")
	if err != nil {
		return nil, fmt.Errorf("creating form file: %w", err)
	}
	if _, err := part.Write([]byte(opmlContent)); err != nil {
		return nil, fmt.Errorf("writing OPML content: %w", err)
	}
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/import/opml_upload", &buf)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Cookie", "newsblur_sessionid="+c.token)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return json.RawMessage(body), nil
}
```

**Step 2: Verify it compiles**

Run: `go build ./internal/newsblur/...`

**Step 3: Commit**

```
git add internal/newsblur/import_export.go
git commit -m "Add OPML import/export API methods"
```

---

### Task 8: Tool Registry and Feed Tools

**Files:**
- Create: `internal/tools/registry.go`
- Create: `internal/tools/feeds.go`

**Step 1: Create the registry**

```go
package tools

import (
	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/friedenberg/nebulous/internal/newsblur"
)

func RegisterAll(client *newsblur.Client) *command.App {
	app := command.NewApp("nebulous", "NewsBlur MCP server")
	app.Version = "0.1.0"

	registerFeedCommands(app, client)

	return app
}
```

**Step 2: Create feed tools**

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
	"github.com/friedenberg/nebulous/internal/newsblur"
)

func registerFeedCommands(app *command.App, client *newsblur.Client) {
	app.AddCommand(&command.Command{
		Name:        "feed_list",
		Title:       "List Feeds",
		Description: command.Description{Short: "List all subscribed feeds"},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    protocol.BoolPtr(true),
			DestructiveHint: protocol.BoolPtr(false),
			IdempotentHint:  protocol.BoolPtr(true),
			OpenWorldHint:   protocol.BoolPtr(true),
		},
		Params: []command.Param{
			{Name: "include_favicons", Type: command.Bool, Description: "Include favicons in response"},
			{Name: "flat", Type: command.Bool, Description: "Return flat list instead of nested folders"},
			{Name: "update_counts", Type: command.Bool, Description: "Force update unread counts"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var params struct {
				IncludeFavicons bool `json:"include_favicons"`
				Flat            bool `json:"flat"`
				UpdateCounts    bool `json:"update_counts"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return command.TextErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			result, err := client.Feeds(ctx, params.IncludeFavicons, params.Flat, params.UpdateCounts)
			if err != nil {
				return command.TextErrorResult(fmt.Sprintf("feed_list: %v", err)), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "feed_search",
		Title:       "Search Feeds",
		Description: command.Description{Short: "Search for a feed by URL"},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    protocol.BoolPtr(true),
			DestructiveHint: protocol.BoolPtr(false),
			IdempotentHint:  protocol.BoolPtr(true),
			OpenWorldHint:   protocol.BoolPtr(true),
		},
		Params: []command.Param{
			{Name: "address", Type: command.String, Description: "URL or domain to search for", Required: true},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var params struct {
				Address string `json:"address"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return command.TextErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			result, err := client.SearchFeed(ctx, params.Address)
			if err != nil {
				return command.TextErrorResult(fmt.Sprintf("feed_search: %v", err)), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "feed_stats",
		Title:       "Feed Statistics",
		Description: command.Description{Short: "Get statistics for a feed"},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    protocol.BoolPtr(true),
			DestructiveHint: protocol.BoolPtr(false),
			IdempotentHint:  protocol.BoolPtr(true),
			OpenWorldHint:   protocol.BoolPtr(true),
		},
		Params: []command.Param{
			{Name: "feed_id", Type: command.Int, Description: "Feed ID", Required: true},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var params struct {
				FeedID int `json:"feed_id"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return command.TextErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			result, err := client.FeedStats(ctx, params.FeedID)
			if err != nil {
				return command.TextErrorResult(fmt.Sprintf("feed_stats: %v", err)), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "feed_autocomplete",
		Title:       "Feed Autocomplete",
		Description: command.Description{Short: "Autocomplete feed search by term"},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    protocol.BoolPtr(true),
			DestructiveHint: protocol.BoolPtr(false),
			IdempotentHint:  protocol.BoolPtr(true),
			OpenWorldHint:   protocol.BoolPtr(true),
		},
		Params: []command.Param{
			{Name: "term", Type: command.String, Description: "Search term", Required: true},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var params struct {
				Term string `json:"term"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return command.TextErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			result, err := client.FeedAutocomplete(ctx, params.Term)
			if err != nil {
				return command.TextErrorResult(fmt.Sprintf("feed_autocomplete: %v", err)), nil
			}
			return command.TextResult(string(result)), nil
		},
	})
}
```

**Step 3: Verify it compiles**

Run: `go build ./internal/tools/...`

**Step 4: Commit**

```
git add internal/tools/registry.go internal/tools/feeds.go
git commit -m "Add tool registry and feed tools"
```

---

### Task 9: Story Tools

**Files:**
- Create: `internal/tools/stories.go`

**Step 1: Implement story tools**

Register 5 commands: `story_feed`, `story_river`, `story_starred`, `story_unread_hashes`, `story_original_text`. Follow the same pattern as feeds.go — each command unmarshals params, calls the matching `client.*` method, returns `command.TextResult(string(result))`.

Key param definitions:
- `story_feed`: `feed_id` (Int, required), `page` (Int), `order` (String), `read_filter` (String), `query` (String)
- `story_river`: `feed_ids` (Array, required), `page` (Int)
- `story_starred`: `page` (Int), `tag` (String), `query` (String)
- `story_unread_hashes`: `feed_ids` (Array)
- `story_original_text`: `story_hash` (String, required)

All are ReadOnly, Idempotent, OpenWorld.

For `story_river`, the `feed_ids` param arrives as `[]any` from JSON; convert each element to int via type assertion.

**Step 2: Verify it compiles**

Run: `go build ./internal/tools/...`

**Step 3: Update registry.go**

Add `registerStoryCommands(app, client)` call in `RegisterAll`.

**Step 4: Commit**

```
git add internal/tools/stories.go internal/tools/registry.go
git commit -m "Add story tools"
```

---

### Task 10: Reader Tools (mark read/unread/star)

**Files:**
- Create: `internal/tools/reader.go`

**Step 1: Implement reader tools**

Register 6 commands: `mark_read`, `mark_unread`, `star`, `unstar`, `mark_feed_read`, `mark_all_read`.

Key annotations: all are `ReadOnlyHint: false`, `DestructiveHint: false`, `IdempotentHint: true`, `OpenWorldHint: true`.

Key param definitions:
- `mark_read`: `story_hashes` (Array, required)
- `mark_unread`: `story_hash` (String, required)
- `star`: `story_hash` (String, required), `user_tags` (Array)
- `unstar`: `story_hash` (String, required)
- `mark_feed_read`: `feed_id` (Int, required)
- `mark_all_read`: `days` (Int)

**Step 2: Update registry.go**

Add `registerReaderCommands(app, client)`.

**Step 3: Verify it compiles**

Run: `go build ./internal/tools/...`

**Step 4: Commit**

```
git add internal/tools/reader.go internal/tools/registry.go
git commit -m "Add reader tools (mark read/unread/star)"
```

---

### Task 11: Subscription and Folder Tools

**Files:**
- Create: `internal/tools/subscriptions.go`
- Create: `internal/tools/folders.go`

**Step 1: Implement subscription tools**

Register 3 commands: `subscribe`, `unsubscribe`, `rename_feed`.

- `subscribe`: `url` (String, required), `folder` (String). Not ReadOnly, not Destructive.
- `unsubscribe`: `feed_id` (Int, required), `in_folder` (String). Not ReadOnly, DestructiveHint true.
- `rename_feed`: `feed_id` (Int, required), `feed_title` (String, required). Not ReadOnly.

**Step 2: Implement folder tools**

Register 5 commands: `folder_create`, `folder_rename`, `folder_delete`, `move_feed`, `move_folder`.

- `folder_create`: `folder_name` (String, required), `parent_folder` (String). Not ReadOnly.
- `folder_rename`: `folder_name` (String, required), `new_folder_name` (String, required), `in_folder` (String). Not ReadOnly.
- `folder_delete`: `folder_name` (String, required). DestructiveHint true.
- `move_feed`: `feed_id` (Int, required), `in_folder` (String, required), `to_folder` (String, required). Not ReadOnly.
- `move_folder`: `folder_name` (String, required), `in_folder` (String, required), `to_folder` (String, required). Not ReadOnly.

**Step 3: Update registry.go**

Add `registerSubscriptionCommands(app, client)` and `registerFolderCommands(app, client)`.

**Step 4: Verify it compiles**

Run: `go build ./internal/tools/...`

**Step 5: Commit**

```
git add internal/tools/subscriptions.go internal/tools/folders.go internal/tools/registry.go
git commit -m "Add subscription and folder tools"
```

---

### Task 12: Import/Export Tools

**Files:**
- Create: `internal/tools/import_export.go`

**Step 1: Implement import/export tools**

Register 2 commands: `opml_export`, `opml_import`.

- `opml_export`: no required params. ReadOnly. Returns `command.TextResult(opml)` (the raw OPML XML string).
- `opml_import`: `opml_content` (String, required — raw OPML XML). Not ReadOnly, not Idempotent.

**Step 2: Update registry.go**

Add `registerImportExportCommands(app, client)`.

**Step 3: Verify it compiles**

Run: `go build ./internal/tools/...`

**Step 4: Commit**

```
git add internal/tools/import_export.go internal/tools/registry.go
git commit -m "Add OPML import/export tools"
```

---

### Task 13: Wire Up main.go

**Files:**
- Modify: `cmd/nebulous/main.go`

**Step 1: Implement main.go**

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/server"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/transport"
	"github.com/friedenberg/nebulous/internal/newsblur"
	"github.com/friedenberg/nebulous/internal/tools"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "nebulous — a NewsBlur MCP server\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  nebulous [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Environment:\n")
		fmt.Fprintf(os.Stderr, "  NEWSBLUR_TOKEN  NewsBlur session cookie (required)\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	token := os.Getenv("NEWSBLUR_TOKEN")
	if token == "" {
		log.Fatal("NEWSBLUR_TOKEN environment variable is required")
	}

	client := newsblur.NewClient(token)
	app := tools.RegisterAll(client)

	if flag.NArg() >= 1 && flag.Arg(0) == "generate-plugin" {
		if err := app.HandleGeneratePlugin(flag.Args()[1:], os.Stdout); err != nil {
			log.Fatalf("generating plugin: %v", err)
		}
		return
	}

	if flag.NArg() >= 1 && flag.Arg(0) == "hook" {
		if err := app.HandleHook(os.Stdin, os.Stdout); err != nil {
			log.Fatalf("handling hook: %v", err)
		}
		return
	}

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
		Instructions:  "NewsBlur MCP server. Provides tools for reading feeds, managing stories, subscriptions, folders, and OPML import/export.",
		Tools:         registry,
	})
	if err != nil {
		log.Fatalf("creating server: %v", err)
	}

	if err := srv.Run(ctx); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
```

**Step 2: Verify full build**

Run: `go build ./cmd/nebulous`
Expected: binary builds without errors.

**Step 3: Smoke test**

Run: `NEWSBLUR_TOKEN=fake ./nebulous` and send a malformed JSON-RPC to stdin to confirm it starts and responds (will get auth error from NewsBlur, but the MCP server loop should be running).

**Step 4: Commit**

```
git add cmd/nebulous/main.go
git commit -m "Wire up main.go with MCP server loop"
```

---

### Task 14: Nix Build Verification

**Step 1: Run nix flake check**

Run: `nix flake check --show-trace` in the nebulous directory.
Expected: may need to update `vendorHash` in flake.nix.

**Step 2: Build with nix**

Run: `nix build --show-trace`

If vendorHash is wrong, nix will output the expected hash. Update `flake.nix` with the correct hash and rebuild.

**Step 3: Verify generate-plugin ran**

Run: `ls -la result/share/` or check for plugin.json in the output.

**Step 4: Commit any flake.nix fixes**

```
git add flake.nix flake.lock
git commit -m "Fix vendorHash for nix build"
```

---

### Task 15: Live Verification Against NewsBlur

**Step 1: Get a real token**

Ask the user for their `NEWSBLUR_TOKEN` value (or have them set it in their env).

**Step 2: Test feed_list**

Run the binary with a real token, send a `tools/call` JSON-RPC request for `feed_list`, verify the response contains actual feed data from their NewsBlur account.

**Step 3: Test a write operation**

Pick a safe write (e.g., `star` then `unstar` the same story) to verify the round-trip.

**Step 4: Note verification in commit message**

If any fixes are needed, commit them noting what was verified.
