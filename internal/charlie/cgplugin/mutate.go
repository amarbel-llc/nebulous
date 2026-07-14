package cgplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"sync"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/friedenberg/nebulous/internal/alfa/newsblur"
)

var _ cg.NodeMutator = Plugin{}

// Client is the write-capable NewsBlur surface this plugin's mutations
// use, satisfied by *newsblur.Client. Declared as an interface (like
// Index in plugin.go) so mutate_test.go can exercise it against a fake
// without a live newsblur.Client.
type Client interface {
	StarStory(ctx context.Context, hash string, tags []string) (json.RawMessage, error)
	UnstarStory(ctx context.Context, hash string) (json.RawMessage, error)
	MarkStoriesRead(ctx context.Context, hashes []string) (json.RawMessage, error)
	MarkStoryUnread(ctx context.Context, hash string) (json.RawMessage, error)
	RenameFeed(ctx context.Context, feedID int, title string) (json.RawMessage, error)
	MoveFeed(ctx context.Context, feedID int, inFolder, toFolder string) (json.RawMessage, error)
	Unsubscribe(ctx context.Context, feedID int, inFolder string) (json.RawMessage, error)
}

var _ Client = (*newsblur.Client)(nil)

// client is the write-capable NewsBlur surface this plugin's mutations
// call directly, injected at startup alongside SetIndex (plugin.go).
// Mutations do not touch the local manifest or force a reindex:
// FacetVersion's manifest-mtime and per-feed NT tokens only reflect a
// mutation after the next scheduled `nebulous fetch` -- the same
// fetch-cadence lag already accepted elsewhere in this package (see
// facets.go's own comment on FacetVersion), not a new class of
// staleness. The existence checks below (StoryMetadata/FeedMetadata)
// read that same lagging local index, so a story/feed that changed very
// recently through another path may not be visible here yet either --
// an accepted best-effort check, not a strict live lookup.
var client Client

// mutateMu serializes this plugin's writes. Package-private, mirrors how
// internal/bravo/tools keeps its own mutation lock unexported.
var mutateMu sync.Mutex

// SetClient injects the write-capable NewsBlur client. The composition
// root calls it once at startup, alongside SetIndex.
func SetClient(c Client) { client = c }

// storyCreateBody is CreateNode's payload for a story node -- starring it.
// A genuine creation payload, not a read-shape echo: nebulous's local
// index only ever holds starred stories (see facets.go's own comment,
// "every story in this store is starred by construction"), so an
// unstarred story has no "current state" a full body could echo.
type storyCreateBody struct {
	UserTags []string `json:"user_tags,omitempty"`
}

// storyPatchBody is PatchNode's payload for a story node. Only Read is
// patchable in this pass; Starred is deliberately absent -- unstarring
// goes through DeleteNode instead, since a not-starred story isn't an
// addressable node this plugin's local index can represent at all.
type storyPatchBody struct {
	Read *bool `json:"read,omitempty"`
}

// feedPatchBody is PatchNode's payload for a feed node. Title and Folder
// may be present alone or together; a single PatchNode call therefore
// may dispatch to RenameFeed, MoveFeed, or both. InFolder is required
// alongside Folder (see patchFeed's doc comment) -- NewsBlur's
// move-feed-to-folder call is keyed by the feed's CURRENT folder, and
// the local index that would otherwise supply it can lag behind live
// NewsBlur state by up to a full fetch cycle; silently trusting it here
// risks a folder-tree move that doesn't match what the caller intended.
// The pre-existing bespoke move_feed MCP tool (internal/bravo/tools/
// folders.go) already requires this explicitly for the same reason.
type feedPatchBody struct {
	Title    *string `json:"title,omitempty"`
	Folder   *string `json:"folder,omitempty"`
	InFolder *string `json:"in_folder,omitempty"`
}

// CreateNode supports exactly one node shape: story/{hash}, which stars
// the story. Feeds, tags, and folders are not creatable through this
// plugin in this pass (subscribe assigns feed_id server-side, so the
// caller cannot name the target URI up front the way CreateNode requires;
// folders have no URI shape in this plugin at all).
func (Plugin) CreateNode(ctx context.Context, node *url.URL, body io.Reader, typ string) error {
	if node == nil {
		return fmt.Errorf("newsblur plugin: CreateNode requires a node URI")
	}
	if index == nil || client == nil {
		return fmt.Errorf("newsblur plugin: not initialized")
	}

	segs := pathSegments(node)
	if len(segs) != 2 || segs[0] != "story" {
		return fmt.Errorf("newsblur plugin: CreateNode only supports story/{hash}, got %s", node)
	}
	if typ != "" && typ != typeStory {
		return fmt.Errorf("newsblur plugin: CreateNode: unexpected type %q for story/{hash} (want %q)", typ, typeStory)
	}
	hash := segs[1]

	if _, _, ok := index.StoryMetadata(hash); ok {
		return fmt.Errorf("newsblur plugin: story %s already exists", hash)
	}

	var payload storyCreateBody
	if body != nil {
		raw, err := io.ReadAll(body)
		if err != nil {
			return fmt.Errorf("newsblur plugin: reading CreateNode body: %w", err)
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &payload); err != nil {
				return fmt.Errorf("newsblur plugin: invalid CreateNode body: %w", err)
			}
		}
	}

	mutateMu.Lock()
	defer mutateMu.Unlock()
	if _, err := client.StarStory(ctx, hash, payload.UserTags); err != nil {
		return fmt.Errorf("newsblur plugin: starring %s: %w", hash, err)
	}
	return nil
}

// PutNode is not supported by this plugin in this pass: none of
// nebulous's current mutations need full-replace semantics -- the ones
// that resemble an update (mark_read/unread, rename_feed, move_feed) fit
// PatchNode's partial-field semantics instead. Required by the
// NodeMutator interface regardless.
func (Plugin) PutNode(ctx context.Context, node *url.URL, body io.Reader) error {
	return fmt.Errorf("newsblur plugin: PutNode is not supported; use PatchNode for partial updates")
}

// PatchNode supports story/{hash} (read state) and feed/{id} (title,
// folder). Absent fields are left untouched; a valid body with no
// recognized fields present succeeds as a no-op.
func (Plugin) PatchNode(ctx context.Context, node *url.URL, body io.Reader) error {
	if node == nil {
		return fmt.Errorf("newsblur plugin: PatchNode requires a node URI")
	}
	if index == nil || client == nil {
		return fmt.Errorf("newsblur plugin: not initialized")
	}
	if body == nil {
		return fmt.Errorf("newsblur plugin: PatchNode requires a body")
	}
	segs := pathSegments(node)
	if len(segs) != 2 || (segs[0] != "story" && segs[0] != "feed") {
		return fmt.Errorf("newsblur plugin: PatchNode only supports story/{hash} and feed/{id}, got %s", node)
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("newsblur plugin: reading PatchNode body: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return fmt.Errorf("newsblur plugin: PatchNode body must not be empty")
	}

	if segs[0] == "story" {
		return patchStory(ctx, segs[1], raw)
	}
	return patchFeed(ctx, segs[1], raw)
}

func patchStory(ctx context.Context, hash string, raw []byte) error {
	if _, _, ok := index.StoryMetadata(hash); !ok {
		return fmt.Errorf("newsblur plugin: story %s not found", hash)
	}
	var patch storyPatchBody
	if err := json.Unmarshal(raw, &patch); err != nil {
		return fmt.Errorf("newsblur plugin: invalid story patch body: %w", err)
	}
	if patch.Read == nil {
		return nil
	}

	mutateMu.Lock()
	defer mutateMu.Unlock()
	if *patch.Read {
		if _, err := client.MarkStoriesRead(ctx, []string{hash}); err != nil {
			return fmt.Errorf("newsblur plugin: marking %s read: %w", hash, err)
		}
		return nil
	}
	if _, err := client.MarkStoryUnread(ctx, hash); err != nil {
		return fmt.Errorf("newsblur plugin: marking %s unread: %w", hash, err)
	}
	return nil
}

// patchFeed requires InFolder alongside Folder rather than deriving the
// feed's current folder from the local index: FeedMetadata reads a
// snapshot that only refreshes on the next `nebulous fetch` (up to a
// full fetch cycle behind live NewsBlur state -- see feedPatchBody's own
// doc comment), and MoveFeed's in_folder argument is load-bearing, not
// informational -- a stale value risks a folder-tree move that silently
// doesn't match the caller's intent, with no error surfaced (NewsBlur's
// API treats any 200 as success regardless of body).
func patchFeed(ctx context.Context, id string, raw []byte) error {
	if _, _, ok := index.FeedMetadata(ctx, id); !ok {
		return fmt.Errorf("newsblur plugin: feed %s not found", id)
	}
	var patch feedPatchBody
	if err := json.Unmarshal(raw, &patch); err != nil {
		return fmt.Errorf("newsblur plugin: invalid feed patch body: %w", err)
	}
	if patch.Folder != nil && patch.InFolder == nil {
		return fmt.Errorf("newsblur plugin: patching feed %s: \"folder\" requires \"in_folder\" (the feed's current folder) alongside it", id)
	}
	if patch.Title == nil && patch.Folder == nil {
		return nil
	}

	feedID, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("newsblur plugin: invalid feed id %q: %w", id, err)
	}

	mutateMu.Lock()
	defer mutateMu.Unlock()
	if patch.Title != nil {
		if _, err := client.RenameFeed(ctx, feedID, *patch.Title); err != nil {
			return fmt.Errorf("newsblur plugin: renaming feed %s: %w", id, err)
		}
	}
	if patch.Folder != nil {
		if _, err := client.MoveFeed(ctx, feedID, *patch.InFolder, *patch.Folder); err != nil {
			return fmt.Errorf("newsblur plugin: moving feed %s: %w", id, err)
		}
	}
	return nil
}

// DeleteNode supports story/{hash} (unstar) and feed/{id} (unsubscribe).
func (Plugin) DeleteNode(ctx context.Context, node *url.URL) error {
	if node == nil {
		return fmt.Errorf("newsblur plugin: DeleteNode requires a node URI")
	}
	if index == nil || client == nil {
		return fmt.Errorf("newsblur plugin: not initialized")
	}

	segs := pathSegments(node)
	switch {
	case len(segs) == 2 && segs[0] == "story":
		return deleteStory(ctx, segs[1])
	case len(segs) == 2 && segs[0] == "feed":
		return deleteFeed(ctx, segs[1])
	default:
		return fmt.Errorf("newsblur plugin: DeleteNode only supports story/{hash} and feed/{id}, got %s", node)
	}
}

func deleteStory(ctx context.Context, hash string) error {
	if _, _, ok := index.StoryMetadata(hash); !ok {
		return fmt.Errorf("newsblur plugin: story %s not found", hash)
	}
	mutateMu.Lock()
	defer mutateMu.Unlock()
	if _, err := client.UnstarStory(ctx, hash); err != nil {
		return fmt.Errorf("newsblur plugin: unstarring %s: %w", hash, err)
	}
	return nil
}

// deleteFeed sources Unsubscribe's in_folder from the local index's
// (potentially stale, see feedPatchBody's doc comment) view of the
// feed's folder -- unlike patchFeed's move case, DeleteNode's interface
// takes no body, so there is no way for a caller to supply an explicit
// current folder here. Best-effort; a residual staleness risk versus
// patchFeed's fix, accepted for lack of a better option within
// DeleteNode's fixed signature.
func deleteFeed(ctx context.Context, id string) error {
	view, _, ok := index.FeedMetadata(ctx, id)
	if !ok {
		return fmt.Errorf("newsblur plugin: feed %s not found", id)
	}
	feedID, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("newsblur plugin: invalid feed id %q: %w", id, err)
	}
	mutateMu.Lock()
	defer mutateMu.Unlock()
	if _, err := client.Unsubscribe(ctx, feedID, view.Folder); err != nil {
		return fmt.Errorf("newsblur plugin: unsubscribing from feed %s: %w", id, err)
	}
	return nil
}
