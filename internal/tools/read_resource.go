package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/server"
)

func registerReadResourceCommand(app *command.App, resources server.ResourceProvider) {
	app.AddCommand(&command.Command{
		Name: "read_resource",
		Description: command.Description{
			Short: "Read a nebulous:// resource by URI. Workaround for subagents that cannot access MCP resources directly. Accepts any URI from ListResources or ListResourceTemplates.",
		},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    protocol.BoolPtr(true),
			DestructiveHint: protocol.BoolPtr(false),
			IdempotentHint:  protocol.BoolPtr(true),
			OpenWorldHint:   protocol.BoolPtr(false),
		},
		Params: []command.Param{
			{Name: "uri", Type: command.String, Required: true, Description: "Resource URI (e.g. nebulous://river, nebulous://river/1, nebulous://story/{hash})"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				URI string `json:"uri"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}

			if p.URI == "" {
				return command.TextErrorResult("uri is required"), nil
			}

			if !strings.HasPrefix(p.URI, "nebulous://") {
				return command.TextErrorResult("uri must start with nebulous://"), nil
			}

			result, err := resources.ReadResource(ctx, p.URI)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			if len(result.Contents) == 0 {
				return command.TextResult("no content"), nil
			}

			return command.TextResult(result.Contents[0].Text), nil
		},
	})
}
