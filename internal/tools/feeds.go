package tools

import (
	"context"
	"encoding/json"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/output"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
	"github.com/friedenberg/nebulous/internal/newsblur"
)

func registerFeedCommands(app *command.App, client *newsblur.Client) {
	defaults := output.StandardDefaults()

	readOnlyAnnotations := &protocol.ToolAnnotations{
		ReadOnlyHint:    protocol.BoolPtr(true),
		DestructiveHint: protocol.BoolPtr(false),
		IdempotentHint:  protocol.BoolPtr(true),
		OpenWorldHint:   protocol.BoolPtr(true),
	}

	app.AddCommand(&command.Command{
		Name: "feed_list",
		Description: command.Description{
			Short: "List all subscribed feeds with folder structure",
		},
		Annotations: readOnlyAnnotations,
		Params: []command.Param{
			{Name: "include_favicons", Type: command.Bool, Description: "Include favicon data in response"},
			{Name: "flat", Type: command.Bool, Description: "Return flat list without folder hierarchy"},
			{Name: "update_counts", Type: command.Bool, Description: "Force update of unread counts"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				IncludeFavicons bool `json:"include_favicons"`
				Flat            bool `json:"flat"`
				UpdateCounts    bool `json:"update_counts"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.Feeds(ctx, p.IncludeFavicons, p.Flat, p.UpdateCounts)
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
		Name: "feed_search",
		Description: command.Description{
			Short: "Search for a feed by URL or domain",
		},
		Annotations: readOnlyAnnotations,
		Params: []command.Param{
			{Name: "address", Type: command.String, Required: true, Description: "URL or domain to search for feeds"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				Address string `json:"address"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.SearchFeed(ctx, p.Address)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "feed_stats",
		Description: command.Description{
			Short: "Get statistics for a specific feed",
		},
		Annotations: readOnlyAnnotations,
		Params: []command.Param{
			{Name: "feed_id", Type: command.Int, Required: true, Description: "Feed ID to get statistics for"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				FeedID int `json:"feed_id"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.FeedStats(ctx, p.FeedID)
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
		Name: "feed_autocomplete",
		Description: command.Description{
			Short: "Autocomplete feed names or URLs",
		},
		Annotations: readOnlyAnnotations,
		Params: []command.Param{
			{Name: "term", Type: command.String, Required: true, Description: "Search term to autocomplete"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				Term string `json:"term"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.FeedAutocomplete(ctx, p.Term)
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
}
