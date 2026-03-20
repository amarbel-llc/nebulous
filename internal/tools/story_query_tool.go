package tools

import (
	"context"
	"encoding/json"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
)

type storyQuerySummary struct {
	Hash      string   `json:"hash"`
	Title     string   `json:"title"`
	Authors   string   `json:"authors,omitempty"`
	FeedID    int      `json:"feed_id"`
	Date      string   `json:"date"`
	UserTags  []string `json:"user_tags,omitempty"`
	Permalink string   `json:"permalink"`
}

func registerStoryQueryCommand(app *command.App, store *storyStore) {
	app.AddCommand(&command.Command{
		Name: "story_query",
		Description: command.Description{
			Short: "Query stories with structured filters and/or word search. Returns compact summaries sorted by date descending. Start with nebulous://stories/facets to see available years, tags, and feeds. Pipeline: stories/facets → story_query(filters) → story/{hash} (metadata) → story/{hash}/content (text) → story/{hash}/original (full article). Fan out story/{hash} reads to subagents in parallel.",
		},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    protocol.BoolPtr(true),
			DestructiveHint: protocol.BoolPtr(false),
			IdempotentHint:  protocol.BoolPtr(true),
			OpenWorldHint:   protocol.BoolPtr(false),
		},
		Params: []command.Param{
			{Name: "words", Type: command.Array, Description: "Words to search for (OR-union, then AND with other filters)"},
			{Name: "year", Type: command.Int, Description: "Filter by year (e.g. 2024)"},
			{Name: "month", Type: command.Int, Description: "Filter by month (1-12, requires year)"},
			{Name: "tag", Type: command.String, Description: "Filter by user tag (e.g. zz-nyc, interests, news)"},
			{Name: "feed_id", Type: command.Int, Description: "Filter by feed ID"},
			{Name: "starred", Type: command.Bool, Description: "Filter by starred status"},
			{Name: "read", Type: command.Bool, Description: "Filter by read status"},
			{Name: "offset", Type: command.Int, Description: "Skip first N results (default 0)"},
			{Name: "limit", Type: command.Int, Description: "Max results to return (default 100)"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			if store == nil {
				return command.TextErrorResult("story store not available (no client)"), nil
			}

			if err := store.ensureBuilt(); err != nil {
				return command.TextErrorResult("building story store: " + err.Error()), nil
			}

			var p struct {
				Words   []string `json:"words"`
				Year    *int     `json:"year"`
				Month   *int     `json:"month"`
				Tag     *string  `json:"tag"`
				FeedID  *int     `json:"feed_id"`
				Starred *bool    `json:"starred"`
				Read    *bool    `json:"read"`
				Offset  int      `json:"offset"`
				Limit   int      `json:"limit"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}

			q := storyQuery{
				Words:   p.Words,
				Year:    p.Year,
				Month:   p.Month,
				Tag:     p.Tag,
				FeedID:  p.FeedID,
				Starred: p.Starred,
				Read:    p.Read,
				Offset:  p.Offset,
				Limit:   p.Limit,
			}

			results := store.query(q)

			summaries := make([]storyQuerySummary, len(results))
			for i, rec := range results {
				summaries[i] = storyQuerySummary{
					Hash:      rec.Hash,
					Title:     rec.Title,
					Authors:   rec.Authors,
					FeedID:    rec.FeedID,
					Date:      rec.Date.Format("2006-01-02 15:04:05"),
					UserTags:  rec.UserTags,
					Permalink: rec.Permalink,
				}
			}

			resp := struct {
				Total   int                 `json:"total"`
				Offset  int                 `json:"offset"`
				Limit   int                 `json:"limit"`
				Results []storyQuerySummary `json:"results"`
			}{
				Total:   len(summaries),
				Offset:  p.Offset,
				Limit:   q.Limit,
				Results: summaries,
			}

			return command.JSONResult(resp), nil
		},
	})
}
