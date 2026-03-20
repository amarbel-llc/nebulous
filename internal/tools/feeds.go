package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
)

func registerFeedCommands(app *command.App, index *feedIndex) {
	readOnlyAnnotations := &protocol.ToolAnnotations{
		ReadOnlyHint:    protocol.BoolPtr(true),
		DestructiveHint: protocol.BoolPtr(false),
		IdempotentHint:  protocol.BoolPtr(true),
		OpenWorldHint:   protocol.BoolPtr(true),
	}

	app.AddCommand(&command.Command{
		Name: "feed_query",
		Description: command.Description{
			Short: "Search feeds by word. Returns compact feed summaries (id, title, folder, unread counts). Primary entry point for feed discovery — no pagination needed. Pipeline: feed_query(words) → nebulous://feed/{id} (full metadata) → nebulous://feed/{id}/stories (recent stories).",
		},
		Annotations: readOnlyAnnotations,
		Params: []command.Param{
			{Name: "words", Type: command.Array, Required: true, Description: "Words to search for (OR-union)"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			if index == nil {
				return command.TextErrorResult("feed index not available (no client)"), nil
			}
			var p struct {
				Words []string `json:"words"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			if len(p.Words) == 0 {
				return command.TextErrorResult("at least one word is required"), nil
			}
			if err := index.ensureBuilt(ctx); err != nil {
				return command.TextErrorResult("building feed index: " + err.Error()), nil
			}

			seen := make(map[string]bool)
			var results []feedSummary
			for _, word := range p.Words {
				for _, s := range index.words[strings.ToLower(word)] {
					id := s.ID.String()
					if !seen[id] {
						seen[id] = true
						results = append(results, s)
					}
				}
			}

			if results == nil {
				results = []feedSummary{}
			}

			return command.JSONResult(results), nil
		},
	})
}
