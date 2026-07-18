package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
	"github.com/friedenberg/nebulous/internal/alfa/newsblur"
)

func registerSubscriptionCommands(app *command.App, client *newsblur.Client, ml *mutationLock) {
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
			_, err := ml.call(func() (json.RawMessage, error) {
				return client.Subscribe(ctx, p.URL, p.Folder)
			})
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(fmt.Sprintf("Subscribed to %s.", p.URL)), nil
		},
	})
}
