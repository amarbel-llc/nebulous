package tools

import (
	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/friedenberg/nebulous/internal/newsblur"
)

func RegisterAll(client *newsblur.Client) *command.App {
	app := command.NewApp("nebulous", "NewsBlur MCP server")
	app.Version = "0.1.0"

	registerFeedCommands(app, client)

	return app
}
