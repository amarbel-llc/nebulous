package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
)

func registerSavedStoryCommands(app *command.App, index *savedStoryIndex) {
	readOnlyAnnotations := &protocol.ToolAnnotations{
		ReadOnlyHint:    protocol.BoolPtr(true),
		DestructiveHint: protocol.BoolPtr(false),
		IdempotentHint:  protocol.BoolPtr(true),
		OpenWorldHint:   protocol.BoolPtr(true),
	}

	app.AddCommand(&command.Command{
		Name: "starred_story_index_query",
		Description: command.Description{
			Short: "Search saved/starred stories by word. Returns compact summaries (hash, title, feed_id, date, permalink) — no full content, no pagination. This is the primary entry point for saved story discovery. Pipeline: starred_story_index_query(words) → story/{hash} (metadata ~200B) → story/{hash}/content (text ~4KB) → story/{hash}/original (full article, API call). Fan out story/{hash} reads to subagents in parallel.",
		},
		Annotations: readOnlyAnnotations,
		Params: []command.Param{
			{Name: "words", Type: command.Array, Required: true, Description: "Words to search for (OR-union)"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			if index == nil {
				return command.TextErrorResult("saved story index not available (no client)"), nil
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
			res := index.ensureBuilt()
			if res.words == nil {
				return command.TextErrorResult("building saved story index: " + res.warning), nil
			}

			seen := make(map[string]bool)
			var results []storySummary
			for _, word := range p.Words {
				for _, s := range res.words[strings.ToLower(word)] {
					if !seen[s.Hash] {
						seen[s.Hash] = true
						results = append(results, s)
					}
				}
			}

			if results == nil {
				results = []storySummary{}
			}

			resp := struct {
				Warning string         `json:"warning,omitempty"`
				Results []storySummary `json:"results"`
			}{
				Warning: res.warning,
				Results: results,
			}

			return command.JSONResult(resp), nil
		},
	})
}
