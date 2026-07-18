package tools

import (
	"encoding/json"
	"sync"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/server"
	"github.com/friedenberg/nebulous/internal/alfa/newsblur"
)

// mutationLock serializes API mutations to prevent concurrent writes
// that cause context cancellation errors from NewsBlur.
type mutationLock struct {
	mu sync.Mutex
}

// call executes fn while holding the lock. The lock is scoped to fn only,
// so response marshaling and other post-call work runs unlocked.
func (m *mutationLock) call(fn func() (json.RawMessage, error)) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fn()
}

func RegisterAll(client *newsblur.Client) (*command.App, server.ResourceProvider) {
	app := command.NewApp("nebulous", "NewsBlur MCP server")
	app.Version = "0.1.0"
	app.MCPArgs = []string{"serve", "mcp"}

	var feedIdx *feedIndex
	var storyStr *storyStore
	if client != nil {
		feedIdx = newFeedIndex(client)
		storyStr = newStoryStore(client)
	}

	ml := &mutationLock{}

	registerFeedCommands(app, feedIdx)
	registerStoryQueryCommand(app, storyStr)
	registerReaderCommands(app, client, ml)
	registerImportExportCommands(app, client, ml)

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
