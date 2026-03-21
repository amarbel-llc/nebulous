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
	var storyStr *storyStore
	if client != nil {
		feedIdx = newFeedIndex(client)
		storyStr = newStoryStore(client)
	}

	registerFeedCommands(app, feedIdx)
	registerStoryQueryCommand(app, storyStr)
	registerReaderCommands(app, client)
	registerSubscriptionCommands(app, client)
	registerFolderCommands(app, client)
	registerImportExportCommands(app, client)

	var resources server.ResourceProvider
	if feedIdx != nil {
		registry := server.NewResourceRegistry()
		registerResources(registry, feedIdx, storyStr)
		resProvider := newFeedResourceProvider(registry, feedIdx, storyStr, client)
		resources = resProvider
		registerResourceToolCommands(app, resProvider)
	}

	return app, resources
}
