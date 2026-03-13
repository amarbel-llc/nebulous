package tools

import (
	"context"
	"encoding/json"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
	"github.com/friedenberg/nebulous/internal/newsblur"
)

func registerSubscriptionCommands(app *command.App, client *newsblur.Client) {
	mutationAnnotations := &protocol.ToolAnnotations{
		ReadOnlyHint:    protocol.BoolPtr(false),
		DestructiveHint: protocol.BoolPtr(false),
		IdempotentHint:  protocol.BoolPtr(false),
		OpenWorldHint:   protocol.BoolPtr(true),
	}

	app.AddCommand(&command.Command{
		Name: "subscribe",
		Description: command.Description{
			Short: "Subscribe to a new feed by URL",
		},
		Annotations: mutationAnnotations,
		Params: []command.Param{
			{Name: "url", Type: command.String, Required: true, Description: "Feed URL to subscribe to"},
			{Name: "folder", Type: command.String, Description: "Folder to place the subscription in"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				URL    string `json:"url"`
				Folder string `json:"folder"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.Subscribe(ctx, p.URL, p.Folder)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "unsubscribe",
		Description: command.Description{
			Short: "Unsubscribe from a feed",
		},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    protocol.BoolPtr(false),
			DestructiveHint: protocol.BoolPtr(true),
			IdempotentHint:  protocol.BoolPtr(false),
			OpenWorldHint:   protocol.BoolPtr(true),
		},
		Params: []command.Param{
			{Name: "feed_id", Type: command.Int, Required: true, Description: "Feed ID to unsubscribe from"},
			{Name: "in_folder", Type: command.String, Description: "Folder the feed is in"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				FeedID   int    `json:"feed_id"`
				InFolder string `json:"in_folder"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.Unsubscribe(ctx, p.FeedID, p.InFolder)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "rename_feed",
		Description: command.Description{
			Short: "Rename a feed",
		},
		Annotations: mutationAnnotations,
		Params: []command.Param{
			{Name: "feed_id", Type: command.Int, Required: true, Description: "Feed ID to rename"},
			{Name: "feed_title", Type: command.String, Required: true, Description: "New title for the feed"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				FeedID    int    `json:"feed_id"`
				FeedTitle string `json:"feed_title"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.RenameFeed(ctx, p.FeedID, p.FeedTitle)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})
}
