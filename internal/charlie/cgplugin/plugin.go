// Package cgplugin exposes nebulous's local NewsBlur index as a
// cutting-garden scheme plugin: a structured tree under newsblur://
// (feeds / stories / tags, with a per-story content + original leaf).
//
// It implements the cutting-garden plugin SDK's RootProvider (no-arg
// entry points + traversal) and LeafReader (per-leaf body fetch),
// registered via MustRegisterScheme. The NewsBlur API sync pipeline and
// word search remain standalone nebulous concerns — this plugin is a
// read-only view over the same local cache the MCP server serves.
package cgplugin

import (
	"context"
	"encoding/json"

	cg "github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/friedenberg/nebulous/internal/bravo/tools"
)

// Index is the read-only NewsBlur index surface the plugin traverses,
// satisfied by *tools.ReadIndex. It is declared as an interface so the
// traversal and leaf logic can be exercised against a fake in tests.
type Index interface {
	Feeds(ctx context.Context) ([]tools.FeedRef, error)
	Stories() ([]tools.StoryRef, error)
	FeedStories(feedID int) ([]tools.StoryRef, error)
	StoriesByTag(tag string) ([]tools.StoryRef, error)
	Tags() ([]string, error)
	FeedMetadata(ctx context.Context, id string) (tools.FeedMetadataView, json.RawMessage, bool)
	StoryContent(hash string) (tools.StoryContentView, []byte, bool)
	StoryOriginal(hash string) ([]byte, bool)
	StoryMetadata(hash string) (tools.StoryMetadataView, []byte, bool)
}

var _ Index = (*tools.ReadIndex)(nil)

// Plugin is the cutting-garden scheme plugin for newsblur://. It is a
// zero-size identity; the read index it serves is injected via SetIndex
// by the composition root (cmd/nebulous-cg), mirroring the caldav
// plugin's SetConfiguredAccounts.
type Plugin struct{}

// Schemes reports the URI scheme this plugin handles.
func (Plugin) Schemes() []string { return []string{Scheme} }

// TypeTag satisfies the Plugin interface's capture-receipt identity.
// This plugin is read-only traversal (no capture in Stage 1), so the tag
// is never emitted into a receipt; it follows the
// cutting_garden-capture_receipt-<segment>-v1 convention for when a
// chrest-driven capture surface lands.
func (Plugin) TypeTag() string {
	return "cutting_garden-capture_receipt-newsblur-v1"
}

// index is the read-only NewsBlur index this plugin serves, injected at
// startup. It lives in package state so Plugin stays a zero-size value
// (like the plugin registry and caldav's configuredAccounts).
var index Index

// SetIndex injects the local NewsBlur read index. The composition root
// calls it once at startup, before any command resolves roots.
func SetIndex(ri Index) { index = ri }

var (
	_ cg.RootProvider = Plugin{}
	_ cg.LeafReader   = Plugin{}
)
