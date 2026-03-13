package tools

import (
	"context"
	"encoding/json"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
	"github.com/friedenberg/nebulous/internal/newsblur"
)

func registerImportExportCommands(app *command.App, client *newsblur.Client) {
	app.AddCommand(&command.Command{
		Name: "opml_export",
		Description: command.Description{
			Short: "Export all subscriptions as OPML",
		},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    protocol.BoolPtr(true),
			DestructiveHint: protocol.BoolPtr(false),
			IdempotentHint:  protocol.BoolPtr(true),
			OpenWorldHint:   protocol.BoolPtr(true),
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			result, err := client.OPMLExport(ctx)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(result), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "opml_import",
		Description: command.Description{
			Short: "Import subscriptions from OPML content",
		},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    protocol.BoolPtr(false),
			DestructiveHint: protocol.BoolPtr(false),
			IdempotentHint:  protocol.BoolPtr(false),
			OpenWorldHint:   protocol.BoolPtr(true),
		},
		Params: []command.Param{
			{Name: "opml_content", Type: command.String, Required: true, Description: "OPML XML content to import"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				OPMLContent string `json:"opml_content"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.OPMLImport(ctx, p.OPMLContent)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})
}
