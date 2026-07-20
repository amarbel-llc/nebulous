package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
	"code.linenisgreat.com/purse-first/libs/go-mcp/command"
	"code.linenisgreat.com/purse-first/libs/go-mcp/protocol"
)

func registerReaderCommands(app *command.App, client *newsblur.Client, ml *mutationLock) {
	idempotentMutationAnnotations := &protocol.ToolAnnotations{
		ReadOnlyHint:    protocol.BoolPtr(false),
		DestructiveHint: protocol.BoolPtr(false),
		IdempotentHint:  protocol.BoolPtr(true),
		OpenWorldHint:   protocol.BoolPtr(true),
	}

	mutationAnnotations := &protocol.ToolAnnotations{
		ReadOnlyHint:    protocol.BoolPtr(false),
		DestructiveHint: protocol.BoolPtr(false),
		IdempotentHint:  protocol.BoolPtr(false),
		OpenWorldHint:   protocol.BoolPtr(true),
	}

	app.AddCommand(&command.Command{
		Name: "mark_read",
		Description: command.Description{
			Short: "Mark stories as read by their hashes",
		},
		Annotations: idempotentMutationAnnotations,
		Params: []command.Param{
			{Name: "story_hashes", Type: command.Array, Required: true, Description: "List of story hashes to mark as read"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				StoryHashes []any `json:"story_hashes"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			hashes, err := toStringSlice(p.StoryHashes)
			if err != nil {
				return command.TextErrorResult("story_hashes: " + err.Error()), nil
			}
			_, err = ml.call(func() (json.RawMessage, error) {
				return client.MarkStoriesRead(ctx, hashes)
			})
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(fmt.Sprintf("Marked %d stories as read.", len(hashes))), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "mark_feed_read",
		Description: command.Description{
			Short: "Mark all stories in a feed as read",
		},
		Annotations: mutationAnnotations,
		Params: []command.Param{
			{Name: "feed_id", Type: command.Int, Required: true, Description: "Feed ID to mark all stories as read"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				FeedID int `json:"feed_id"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			_, err := ml.call(func() (json.RawMessage, error) {
				return client.MarkFeedRead(ctx, p.FeedID)
			})
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(fmt.Sprintf("Marked feed %d as read.", p.FeedID)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "mark_all_read",
		Description: command.Description{
			Short: "Mark all stories as read, optionally limited to recent days",
		},
		Annotations: mutationAnnotations,
		Params: []command.Param{
			{Name: "days", Type: command.Int, Description: "Only mark stories from the last N days as read"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				Days int `json:"days"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			_, err := ml.call(func() (json.RawMessage, error) {
				return client.MarkAllRead(ctx, p.Days)
			})
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			if p.Days > 0 {
				return command.TextResult(fmt.Sprintf("Marked all stories from the last %d days as read.", p.Days)), nil
			}
			return command.TextResult("Marked all stories as read."), nil
		},
	})
}

func toStringSlice(raw []any) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("expected string, got %T", v)
		}
		out = append(out, s)
	}
	return out, nil
}
