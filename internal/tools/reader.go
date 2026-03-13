package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
	"github.com/friedenberg/nebulous/internal/newsblur"
)

func registerReaderCommands(app *command.App, client *newsblur.Client) {
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
			result, err := client.MarkStoriesRead(ctx, hashes)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "mark_unread",
		Description: command.Description{
			Short: "Mark a story as unread",
		},
		Annotations: idempotentMutationAnnotations,
		Params: []command.Param{
			{Name: "story_hash", Type: command.String, Required: true, Description: "Story hash to mark as unread"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				StoryHash string `json:"story_hash"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.MarkStoryUnread(ctx, p.StoryHash)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "star",
		Description: command.Description{
			Short: "Star a story with optional tags",
		},
		Annotations: mutationAnnotations,
		Params: []command.Param{
			{Name: "story_hash", Type: command.String, Required: true, Description: "Story hash to star"},
			{Name: "user_tags", Type: command.Array, Description: "Tags to apply to the starred story"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				StoryHash string `json:"story_hash"`
				UserTags  []any  `json:"user_tags"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			var tags []string
			if len(p.UserTags) > 0 {
				var err error
				tags, err = toStringSlice(p.UserTags)
				if err != nil {
					return command.TextErrorResult("user_tags: " + err.Error()), nil
				}
			}
			result, err := client.StarStory(ctx, p.StoryHash, tags)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "unstar",
		Description: command.Description{
			Short: "Remove star from a story",
		},
		Annotations: mutationAnnotations,
		Params: []command.Param{
			{Name: "story_hash", Type: command.String, Required: true, Description: "Story hash to unstar"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				StoryHash string `json:"story_hash"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.UnstarStory(ctx, p.StoryHash)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
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
			result, err := client.MarkFeedRead(ctx, p.FeedID)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
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
			result, err := client.MarkAllRead(ctx, p.Days)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
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
