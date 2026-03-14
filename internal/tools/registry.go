package tools

import (
	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/server"
	"github.com/friedenberg/nebulous/internal/newsblur"
)

func RegisterAll(client *newsblur.Client) (*command.App, server.ResourceProvider) {
	app := command.NewApp("nebulous", "NewsBlur MCP server")
	app.Version = "0.1.0"

	var feedIdx *feedIndex
	var savedIdx *savedStoryIndex
	if client != nil {
		feedIdx = newFeedIndex(client)
		savedIdx = newSavedStoryIndex(client)
	}

	registerFeedCommands(app, client, feedIdx)
	registerStoryCommands(app, client)
	registerReaderCommands(app, client)
	registerSubscriptionCommands(app, client)
	registerFolderCommands(app, client)
	registerImportExportCommands(app, client)
	registerSavedStoryCommands(app, savedIdx)

	var resources server.ResourceProvider
	if feedIdx != nil {
		registry := server.NewResourceRegistry()
		registerResources(registry, feedIdx, savedIdx)
		resources = newFeedResourceProvider(registry, feedIdx, savedIdx, client)
	}

	return app, resources
}
