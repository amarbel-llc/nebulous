package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/output"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
	"github.com/friedenberg/nebulous/internal/newsblur"
)

func registerStoryCommands(app *command.App, client *newsblur.Client) {
	defaults := output.StandardDefaults()

	readOnlyAnnotations := &protocol.ToolAnnotations{
		ReadOnlyHint:    protocol.BoolPtr(true),
		DestructiveHint: protocol.BoolPtr(false),
		IdempotentHint:  protocol.BoolPtr(true),
		OpenWorldHint:   protocol.BoolPtr(true),
	}

	app.AddCommand(&command.Command{
		Name: "story_feed",
		Description: command.Description{
			Short: "Get stories from a specific feed",
		},
		Annotations: readOnlyAnnotations,
		Params: []command.Param{
			{Name: "feed_id", Type: command.Int, Required: true, Description: "Feed ID to fetch stories from"},
			{Name: "page", Type: command.Int, Description: "Page number for pagination"},
			{Name: "order", Type: command.String, Description: "Sort order (newest or oldest)"},
			{Name: "read_filter", Type: command.String, Description: "Filter by read status (all, unread, read)"},
			{Name: "query", Type: command.String, Description: "Search query within the feed"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				FeedID     int    `json:"feed_id"`
				Page       int    `json:"page"`
				Order      string `json:"order"`
				ReadFilter string `json:"read_filter"`
				Query      string `json:"query"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.StoriesFeed(ctx, p.FeedID, p.Page, p.Order, p.ReadFilter, p.Query)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			limited := output.LimitText(string(result), defaults.MergeTextLimits(output.TextLimits{}))
			if limited.Truncated {
				return command.JSONResult(limited), nil
			}
			return command.TextResult(limited.Content), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "story_river",
		Description: command.Description{
			Short: "Get stories from multiple feeds as a river",
		},
		Annotations: readOnlyAnnotations,
		Params: []command.Param{
			{Name: "feed_ids", Type: command.Array, Required: true, Description: "List of feed IDs to fetch stories from"},
			{Name: "page", Type: command.Int, Description: "Page number for pagination"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				FeedIDs []any `json:"feed_ids"`
				Page    int   `json:"page"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			ids, err := toIntSlice(p.FeedIDs)
			if err != nil {
				return command.TextErrorResult("feed_ids: " + err.Error()), nil
			}
			result, err := client.StoriesRiver(ctx, ids, p.Page)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			limited := output.LimitText(string(result), defaults.MergeTextLimits(output.TextLimits{}))
			if limited.Truncated {
				return command.JSONResult(limited), nil
			}
			return command.TextResult(limited.Content), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "story_starred",
		Description: command.Description{
			Short: "Get starred stories. Returns full story objects (10 per page). For discovery, prefer starred_story_index_query — it returns compact summaries with hashes for all matching stories in one call. Use those hashes with nebulous://story/{hash} resources for targeted reads. Only use story_starred for browsing without keywords or when you need server-side query/tag filtering.",
		},
		Annotations: readOnlyAnnotations,
		Params: []command.Param{
			{Name: "page", Type: command.Int, Description: "Page number for pagination"},
			{Name: "tag", Type: command.String, Description: "Filter by star tag"},
			{Name: "query", Type: command.String, Description: "Search query within starred stories"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				Page  int    `json:"page"`
				Tag   string `json:"tag"`
				Query string `json:"query"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.StoriesStarred(ctx, p.Page, p.Tag, p.Query)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "story_unread_hashes",
		Description: command.Description{
			Short: "Get unread story hashes for feeds",
		},
		Annotations: readOnlyAnnotations,
		Params: []command.Param{
			{Name: "feed_ids", Type: command.Array, Description: "Feed IDs to get unread hashes for"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				FeedIDs []any `json:"feed_ids"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			ids, err := toIntSlice(p.FeedIDs)
			if err != nil {
				return command.TextErrorResult("feed_ids: " + err.Error()), nil
			}
			result, err := client.UnreadStoryHashes(ctx, ids)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "story_original_text",
		Description: command.Description{
			Short: "Fetch original article text from source URL by story hash. Use when story/{hash} shows has_content=false or story/{hash}/content was truncated. Makes an HTTP request — prefer story/{hash} and story/{hash}/content first.",
		},
		Annotations: readOnlyAnnotations,
		Params: []command.Param{
			{Name: "story_hash", Type: command.String, Required: true, Description: "Hash of the story to fetch original text for"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				StoryHash string `json:"story_hash"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.OriginalText(ctx, p.StoryHash)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})
}

func toIntSlice(raw []any) ([]int, error) {
	out := make([]int, 0, len(raw))
	for _, v := range raw {
		switch n := v.(type) {
		case float64:
			out = append(out, int(n))
		case json.Number:
			i, err := n.Int64()
			if err != nil {
				return nil, fmt.Errorf("expected integer, got %v", v)
			}
			out = append(out, int(i))
		case string:
			i, err := strconv.Atoi(n)
			if err != nil {
				return nil, fmt.Errorf("expected integer string, got %q", n)
			}
			out = append(out, i)
		default:
			return nil, fmt.Errorf("expected integer, got %T", v)
		}
	}
	return out, nil
}
