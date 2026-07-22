package cgplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var _ cg.NodeMutator = Plugin{}

// Caller-fault vs plugin-fault (RFC 0013 §Errors, cutting-garden#185): the
// wire transport (cutting-garden's own traversal_serve server.Handle)
// reclassifies a returned error as CodeInvalidParams (-32602) ONLY when
// dewey's errors.Is400BadRequest(err) is true; every other error defaults
// to CodeInternalError (-32603). This package never imports
// pkgs/traversal_serve or constructs an RPCError directly -- classification
// is domain knowledge cgplugin has (a caller mistake vs a NewsBlur API
// call failing), translation into a wire code is the transport's job, and
// the two meeting only at dewey's shared tagging convention is what keeps
// this package servable both wire (traversal-serve) and, in principle,
// linked in-process without answering differently for identical input.
// errors.BadRequestf marks a genuine caller mistake (bad URI, a value the
// caller supplied that this plugin cannot use, patching/deleting something
// that does not exist); plain fmt.Errorf is left alone for everything this
// plugin itself cannot fulfill (an unreachable/failing NewsBlur API call,
// "not initialized") -- a uniform mapping of every failure to -32603 is
// exactly the non-conformant anti-pattern RFC 0013 §Errors calls out by
// name, discovered when two independent implementations (this plugin's
// upstream SDK's own reference host among them) both fell into it.

// Client is the write-capable NewsBlur surface this plugin's mutations
// use, satisfied by *newsblur.Client. Declared as an interface (like
// Index in plugin.go) so mutate_test.go can exercise it against a fake
// without a live newsblur.Client.
type Client interface {
	StarStory(ctx context.Context, hash string, tags []string) (json.RawMessage, error)
	SetStoryUserTags(ctx context.Context, hash string, tags []string) (json.RawMessage, error)
	UnstarStory(ctx context.Context, hash string) (json.RawMessage, error)
	MarkStoriesRead(ctx context.Context, hashes []string) (json.RawMessage, error)
	MarkStoryUnread(ctx context.Context, hash string) (json.RawMessage, error)
	RenameFeed(ctx context.Context, feedID int, title string) (json.RawMessage, error)
	MoveFeed(ctx context.Context, feedID int, inFolder, toFolder string) (json.RawMessage, error)
	Unsubscribe(ctx context.Context, feedID int, inFolder string) (json.RawMessage, error)
	CreateFolder(ctx context.Context, folderName, parentFolder string) (json.RawMessage, error)
	RenameFolder(ctx context.Context, folderName, newFolderName, inFolder string) (json.RawMessage, error)
	DeleteFolder(ctx context.Context, folderName, inFolder string) (json.RawMessage, error)
	MoveFolder(ctx context.Context, folderName, inFolder, toFolder string) (json.RawMessage, error)
	Subscribe(ctx context.Context, feedURL, folder string) (json.RawMessage, error)
}

var _ Client = (*newsblur.Client)(nil)

// client is the write-capable NewsBlur surface this plugin's mutations
// call directly, injected at startup alongside SetIndex (plugin.go).
// Story read/unread, star/unstar, and user_tags all optimistically patch
// their cached entries in place (newsblur.Client's MarkStoriesRead/
// MarkStoryUnread/StarStory/UnstarStory/SetStoryUserTags each do this on
// success -- see internal/alfa/newsblur/cache_patch.go), so a read
// immediately after one of those mutations *usually* already reflects the
// change -- but the patch is best-effort: a failure is swallowed at the
// call site (nothing surfaces it), and even a successful patch's manifest
// write can be lost to a concurrently-running `nebulous fetch` process
// (nebulous#42, Record/RecordBatch never reload-before-merge --
// pre-existing, this plugin's patch calls just add more surface area to
// it, not a new class of the same bug). user_tags's cache patch matters
// more than the others: cmd/nebulous/main.go's fetch only ever fetches a
// starred story hash ONCE (an immutable-content assumption user_tags
// breaks, since it's re-settable after the initial star), so without an
// optimistic patch a tag change would stay invisible PERMANENTLY, not
// just until the next fetch -- confirmed live, cutting-garden#180 /
// nebulous#53. Feed rename/move do not patch the cache at all yet and
// still lag until the next `nebulous fetch` -- the same fetch-cadence lag
// accepted elsewhere in this package (see facets.go's own comment on
// FacetVersion), though feed metadata IS refetched every cycle (unlike
// starred stories), so that lag is bounded, not permanent. The existence
// checks below (StoryMetadata/FeedMetadata) read the local index too, so
// a story/feed that changed very recently through another path may not
// be visible here yet either -- an accepted best-effort check, not a
// strict live lookup.
var client Client

// mutateMu serializes this plugin's writes. Package-private, mirrors how
// internal/bravo/tools keeps its own mutation lock unexported.
var mutateMu sync.Mutex

// SetClient injects the write-capable NewsBlur client. The composition
// root calls it once at startup, alongside SetIndex.
func SetClient(c Client) { client = c }

// strictUnmarshal decodes a caller-supplied CREATE body strictly: a field
// json.Unmarshal would otherwise silently drop is instead a rejected,
// actionable error. Used by createStory/CreateChild, which have no
// partial-application concept to fall back on -- a create either fully
// succeeds or errors, so an unrecognized field can only mean a caller
// mistake worth surfacing immediately.
//
// PATCH bodies (storyPatchBody/feedPatchBody/folderPatchBody) do NOT use
// this anymore -- see PatchNode's own doc comment for why tolerance was
// restored there (cutting-garden#182).
func strictUnmarshal(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// storyCreateBody is CreateNode's payload for a story node -- starring it.
// A genuine creation payload, not a read-shape echo: nebulous's local
// index only ever holds starred stories (see facets.go's own comment,
// "every story in this store is starred by construction"), so an
// unstarred story has no "current state" a full body could echo.
type storyCreateBody struct {
	UserTags []string `json:"user_tags,omitempty"`
}

// storyPatchBody is PatchNode's payload for a story node. Read is a plain
// *bool (absent vs present is all it needs). UserTags is a *[]string, not
// []string, because it needs a THIRD state a bare slice can't express:
// absent (nil pointer, leave tags untouched) vs present-and-empty
// (non-nil pointer to a zero-length slice, clear all tags) vs
// present-and-populated (REPLACES the existing set -- verified live
// against a real account, nebulous#50: re-starring with a different tag
// set drops the old one rather than merging). A plain []string field
// can't distinguish the first two: json.Unmarshal leaves an absent
// field's slice nil, indistinguishable from an explicit empty array
// decoding to nil too -- confirmed empirically before relying on this,
// since encoding/json's exact absent-vs-null-vs-empty-array behavior
// for a slice-typed pointer field is easy to get backwards from memory.
// Starred is deliberately absent -- unstarring goes through DeleteNode
// instead, since a not-starred story isn't an addressable node this
// plugin's local index can represent at all.
type storyPatchBody struct {
	Read     *bool     `json:"read,omitempty"`
	UserTags *[]string `json:"user_tags,omitempty"`
}

// feedPatchBody is PatchNode's payload for a feed node. Title and Folder
// may be present alone or together; a single PatchNode call therefore
// may dispatch to RenameFeed, MoveFeed, or both. InFolder is required
// alongside Folder (see patchFeed's doc comment) -- NewsBlur's
// move-feed-to-folder call is keyed by the feed's CURRENT folder, and
// the local index that would otherwise supply it can lag behind live
// NewsBlur state by up to a full fetch cycle; silently trusting it here
// risks a folder-tree move that doesn't match what the caller intended.
// The retired bespoke move_feed MCP tool (formerly internal/bravo/tools/
// folders.go, nebulous#40) required this explicitly for the same reason.
type feedPatchBody struct {
	Title    *string `json:"title,omitempty"`
	Folder   *string `json:"folder,omitempty"`
	InFolder *string `json:"in_folder,omitempty"`
}

// folderPatchBody is PatchNode's payload for a folder node. Name renames
// the folder in place (keeping its parent); ToFolder moves it under a
// new parent (keeping its name). Both may fire from one call: a rename
// is applied first (against the folder's current parent), then a move
// (against the now-renamed name) -- mirroring feedPatchBody's
// rename-then-move order.
type folderPatchBody struct {
	Name     *string `json:"name,omitempty"`
	ToFolder *string `json:"to_folder,omitempty"`
}

// folderPathSeparator is NewsBlur's own nested-folder join convention
// (verified against samuelclay/NewsBlur's UserSubscriptionFolders.
// flatten_folders, whose own docstring documents nested paths like
// "Parent - Child - Grandchild"). nebulous's existing FeedRef.Folder
// field (internal/bravo/tools/feed_index.go, via the
// flat_folders_with_feeds API field) already uses this exact
// convention, so folder nodes reuse it rather than inventing a
// slash-hierarchical URI shape NewsBlur itself doesn't speak.
const folderPathSeparator = " - "

// splitFolderPath splits a folder's full path into its own (leaf) name
// and its parent's full path -- parentPath is empty for a top-level
// folder. A folder literally named containing " - " is ambiguous with
// nesting; that is NewsBlur's own join convention's pre-existing
// limitation (flatten_folders has the same ambiguity), not something
// resolvable client-side. A folder name containing "/" is a second,
// separate limitation: nodeURL/pathSegments (url.go) split the whole
// URI on "/", so such a name would produce more than the two segments
// CreateNode/PatchNode/DeleteNode's folder cases match on, and the node
// falls through to their "unsupported" error -- the same class of gap
// nodeURL's own doc comment already flags for tags ("tags containing a
// slash are not supported (rare)").
func splitFolderPath(path string) (ownName, parentPath string) {
	if i := strings.LastIndex(path, folderPathSeparator); i >= 0 {
		return path[i+len(folderPathSeparator):], path[:i]
	}
	return path, ""
}

// CreateNode supports story/{hash} (star) and folder/{path} (create a
// folder). Feeds are not creatable this way -- subscribe assigns
// feed_id server-side, so the caller cannot name the target URI up
// front; see CreateChild (create_child.go) instead.
func (Plugin) CreateNode(ctx context.Context, node *url.URL, body io.Reader, typ string) error {
	if node == nil {
		return errors.BadRequestf("newsblur plugin: CreateNode requires a node URI")
	}
	if index == nil || client == nil {
		return fmt.Errorf("newsblur plugin: not initialized")
	}

	segs := pathSegments(node)
	switch {
	case len(segs) == 2 && segs[0] == "story":
		return createStory(ctx, segs[1], body, typ)
	case len(segs) == 2 && segs[0] == "folder":
		return createFolder(ctx, segs[1], typ)
	default:
		return errors.BadRequestf("newsblur plugin: CreateNode only supports story/{hash} and folder/{path}, got %s", node)
	}
}

func createStory(ctx context.Context, hash string, body io.Reader, typ string) error {
	if typ != "" && typ != typeStory {
		return errors.BadRequestf("newsblur plugin: CreateNode: unexpected type %q for story/{hash} (want %q)", typ, typeStory)
	}
	if _, _, ok := index.StoryMetadata(hash); ok {
		return errors.BadRequestf("newsblur plugin: story %s already exists", hash)
	}

	var payload storyCreateBody
	if body != nil {
		raw, err := io.ReadAll(body)
		if err != nil {
			return fmt.Errorf("newsblur plugin: reading CreateNode body: %w", err)
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := strictUnmarshal(raw, &payload); err != nil {
				return errors.BadRequestf("newsblur plugin: invalid CreateNode body: %w", err)
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

// createFolder needs no body: both the new folder's own name and its
// parent are already encoded in the target URI (split via
// splitFolderPath). Unlike createStory, there is no local existence
// check -- nebulous's index tracks feeds' folder assignments, not
// folder existence itself (see typeFolder's own comment in
// traversal.go on the current no-ListRoots scope).
func createFolder(ctx context.Context, path string, typ string) error {
	if typ != "" && typ != typeFolder {
		return errors.BadRequestf("newsblur plugin: CreateNode: unexpected type %q for folder/{path} (want %q)", typ, typeFolder)
	}
	if path == "" {
		return errors.BadRequestf("newsblur plugin: CreateNode: folder path must not be empty")
	}
	ownName, parentPath := splitFolderPath(path)

	mutateMu.Lock()
	defer mutateMu.Unlock()
	if _, err := client.CreateFolder(ctx, ownName, parentPath); err != nil {
		return fmt.Errorf("newsblur plugin: creating folder %q: %w", path, err)
	}
	return nil
}

// PutNode is not supported by this plugin in this pass: none of
// nebulous's current mutations need full-replace semantics -- the ones
// that resemble an update (mark_read/unread, rename_feed, move_feed) fit
// PatchNode's partial-field semantics instead. Required by the
// NodeMutator interface regardless.
func (Plugin) PutNode(ctx context.Context, node *url.URL, body io.Reader) error {
	return errors.BadRequestf("newsblur plugin: PutNode is not supported; use PatchNode for partial updates")
}

// PatchNode supports story/{hash} (read state, user_tags), feed/{id}
// (title, folder), and folder/{path} (name, to_folder). Absent fields are
// left untouched. A field this plugin doesn't recognize is tolerated, not
// rejected -- a newer caller naming a field an older nebulous build
// doesn't know about must still succeed -- but the applied return reports
// exactly which recognized fields actually changed, so a caller can tell
// a body that landed from one that was silently ignored
// (cutting-garden#182, following directly from #180: tolerating unknown
// fields with a bare error return is what let a patch call the backend
// zero times and still report plain success). A field this plugin DOES
// recognize but with an unusable value (e.g. {"read":"yes"}) is a
// different case -- a caller bug, not forward-compatibility -- and still
// errors; only unrecognized KEYS are tolerated, never a bad value for a
// known one.
//
// applied is non-nil on every successful call: empty when nothing in the
// body was recognized (the caller-visible signal that the patch changed
// nothing, RFC 0013 §Mutation), populated with exactly the field names
// that landed otherwise. Order is unspecified.
func (Plugin) PatchNode(ctx context.Context, node *url.URL, body io.Reader) ([]string, error) {
	if node == nil {
		return nil, errors.BadRequestf("newsblur plugin: PatchNode requires a node URI")
	}
	if index == nil || client == nil {
		return nil, fmt.Errorf("newsblur plugin: not initialized")
	}
	if body == nil {
		return nil, errors.BadRequestf("newsblur plugin: PatchNode requires a body")
	}
	segs := pathSegments(node)
	if len(segs) != 2 {
		return nil, errors.BadRequestf("newsblur plugin: PatchNode only supports story/{hash}, feed/{id}, and folder/{path}, got %s", node)
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("newsblur plugin: reading PatchNode body: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.BadRequestf("newsblur plugin: PatchNode body must not be empty")
	}

	switch segs[0] {
	case "story":
		return patchStory(ctx, segs[1], raw)
	case "feed":
		return patchFeed(ctx, segs[1], raw)
	case "folder":
		return patchFolder(ctx, segs[1], raw)
	default:
		return nil, errors.BadRequestf("newsblur plugin: PatchNode only supports story/{hash}, feed/{id}, and folder/{path}, got %s", node)
	}
}

func patchStory(ctx context.Context, hash string, raw []byte) ([]string, error) {
	if _, _, ok := index.StoryMetadata(hash); !ok {
		return nil, errors.BadRequestf("newsblur plugin: story %s not found", hash)
	}
	// Plain json.Unmarshal, not strictUnmarshal: an unrecognized field is
	// tolerated (dropped silently, exactly like any other absent field),
	// while a recognized field with the wrong JSON type still fails here
	// -- json.Unmarshal's ordinary type-checking, unaffected by whether
	// unknown fields are disallowed, is exactly the "tolerate unknown
	// keys, reject bad values for known ones" split cutting-garden#182
	// wants.
	var patch storyPatchBody
	if err := json.Unmarshal(raw, &patch); err != nil {
		return nil, errors.BadRequestf("newsblur plugin: invalid story patch body: %w", err)
	}
	if patch.Read == nil && patch.UserTags == nil {
		return []string{}, nil
	}

	mutateMu.Lock()
	defer mutateMu.Unlock()

	var applied []string

	if patch.Read != nil {
		if *patch.Read {
			if _, err := client.MarkStoriesRead(ctx, []string{hash}); err != nil {
				return nil, fmt.Errorf("newsblur plugin: marking %s read: %w", hash, err)
			}
		} else if _, err := client.MarkStoryUnread(ctx, hash); err != nil {
			return nil, fmt.Errorf("newsblur plugin: marking %s unread: %w", hash, err)
		}
		applied = append(applied, "read")
	}

	// REPLACES the story's tags, including clearing them all when UserTags
	// is present-but-empty -- see storyPatchBody's doc comment for how the
	// *[]string distinguishes that from UserTags being absent.
	if patch.UserTags != nil {
		if _, err := client.SetStoryUserTags(ctx, hash, *patch.UserTags); err != nil {
			return nil, fmt.Errorf("newsblur plugin: setting tags for %s: %w", hash, err)
		}
		applied = append(applied, "user_tags")
	}

	return applied, nil
}

// patchFeed requires InFolder alongside Folder rather than deriving the
// feed's current folder from the local index: FeedMetadata reads a
// snapshot that only refreshes on the next `nebulous fetch` (up to a
// full fetch cycle behind live NewsBlur state -- see feedPatchBody's own
// doc comment), and MoveFeed's in_folder argument is load-bearing, not
// informational -- a stale value risks a folder-tree move that silently
// doesn't match the caller's intent, with no error surfaced (NewsBlur's
// API treats any 200 as success regardless of body).
func patchFeed(ctx context.Context, id string, raw []byte) ([]string, error) {
	if _, _, ok := index.FeedMetadata(ctx, id); !ok {
		return nil, errors.BadRequestf("newsblur plugin: feed %s not found", id)
	}
	// Plain json.Unmarshal -- see patchStory's identical comment.
	var patch feedPatchBody
	if err := json.Unmarshal(raw, &patch); err != nil {
		return nil, errors.BadRequestf("newsblur plugin: invalid feed patch body: %w", err)
	}
	if patch.Folder != nil && patch.InFolder == nil {
		return nil, errors.BadRequestf("newsblur plugin: patching feed %s: \"folder\" requires \"in_folder\" (the feed's current folder) alongside it", id)
	}
	if patch.Title == nil && patch.Folder == nil {
		return []string{}, nil
	}

	feedID, err := strconv.Atoi(id)
	if err != nil {
		return nil, errors.BadRequestf("newsblur plugin: invalid feed id %q: %w", id, err)
	}

	mutateMu.Lock()
	defer mutateMu.Unlock()

	var applied []string
	if patch.Title != nil {
		if _, err := client.RenameFeed(ctx, feedID, *patch.Title); err != nil {
			return nil, fmt.Errorf("newsblur plugin: renaming feed %s: %w", id, err)
		}
		applied = append(applied, "title")
	}
	if patch.Folder != nil {
		if _, err := client.MoveFeed(ctx, feedID, *patch.InFolder, *patch.Folder); err != nil {
			return nil, fmt.Errorf("newsblur plugin: moving feed %s: %w", id, err)
		}
		applied = append(applied, "folder")
	}
	return applied, nil
}

// patchFolder supports rename (name) and move (to_folder). Like
// createFolder, there is no local existence check -- nebulous tracks no
// folder index of its own (see typeFolder's comment in traversal.go).
func patchFolder(ctx context.Context, path string, raw []byte) ([]string, error) {
	if path == "" {
		return nil, errors.BadRequestf("newsblur plugin: PatchNode: folder path must not be empty")
	}
	// Plain json.Unmarshal -- see patchStory's identical comment.
	var patch folderPatchBody
	if err := json.Unmarshal(raw, &patch); err != nil {
		return nil, errors.BadRequestf("newsblur plugin: invalid folder patch body: %w", err)
	}
	if patch.Name == nil && patch.ToFolder == nil {
		return []string{}, nil
	}

	ownName, parentPath := splitFolderPath(path)

	mutateMu.Lock()
	defer mutateMu.Unlock()

	var applied []string
	if patch.Name != nil {
		if _, err := client.RenameFolder(ctx, ownName, *patch.Name, parentPath); err != nil {
			return nil, fmt.Errorf("newsblur plugin: renaming folder %q: %w", path, err)
		}
		applied = append(applied, "name")
		ownName = *patch.Name
	}
	if patch.ToFolder != nil {
		if _, err := client.MoveFolder(ctx, ownName, parentPath, *patch.ToFolder); err != nil {
			return nil, fmt.Errorf("newsblur plugin: moving folder %q: %w", path, err)
		}
		applied = append(applied, "to_folder")
	}
	return applied, nil
}

// DeleteNode supports story/{hash} (unstar), feed/{id} (unsubscribe),
// and folder/{path} (delete).
func (Plugin) DeleteNode(ctx context.Context, node *url.URL) error {
	if node == nil {
		return errors.BadRequestf("newsblur plugin: DeleteNode requires a node URI")
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
	case len(segs) == 2 && segs[0] == "folder":
		return deleteFolder(ctx, segs[1])
	default:
		return errors.BadRequestf("newsblur plugin: DeleteNode only supports story/{hash}, feed/{id}, and folder/{path}, got %s", node)
	}
}

func deleteStory(ctx context.Context, hash string) error {
	if _, _, ok := index.StoryMetadata(hash); !ok {
		return errors.BadRequestf("newsblur plugin: story %s not found", hash)
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
		return errors.BadRequestf("newsblur plugin: feed %s not found", id)
	}
	feedID, err := strconv.Atoi(id)
	if err != nil {
		return errors.BadRequestf("newsblur plugin: invalid feed id %q: %w", id, err)
	}
	mutateMu.Lock()
	defer mutateMu.Unlock()
	if _, err := client.Unsubscribe(ctx, feedID, view.Folder); err != nil {
		return fmt.Errorf("newsblur plugin: unsubscribing from feed %s: %w", id, err)
	}
	return nil
}

// deleteFolder needs no local existence check for the same reason
// createFolder/patchFolder don't -- see typeFolder's comment in
// traversal.go.
func deleteFolder(ctx context.Context, path string) error {
	if path == "" {
		return errors.BadRequestf("newsblur plugin: DeleteNode: folder path must not be empty")
	}
	ownName, parentPath := splitFolderPath(path)
	mutateMu.Lock()
	defer mutateMu.Unlock()
	if _, err := client.DeleteFolder(ctx, ownName, parentPath); err != nil {
		return fmt.Errorf("newsblur plugin: deleting folder %q: %w", path, err)
	}
	return nil
}
