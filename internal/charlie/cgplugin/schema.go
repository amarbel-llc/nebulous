package cgplugin

import (
	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

var _ cg.BodyDescriber = Plugin{}

// DescribeBodies describes the create/update payloads of every writable
// newsblur node type -- the schema-discovery surface behind the mcp
// server's describe_node_types tool, mirroring caldav's schema.go.
func (Plugin) DescribeBodies() []cg.NodeTypeBody {
	return []cg.NodeTypeBody{
		{
			Tag: typeStory,
			// Deliberately NOT ServerAssignedIdentity: cutting-garden
			// refuses a caller-named id for such a type, which would break
			// the create-by-hash (star an existing story) mode below. A
			// temp-id create into the stories root reaches CreateChild
			// regardless of the flag.
			Accepts: []string{
				"application/json (mutate_nodes create op with a story/{hash} id: {\"user_tags\":[...]} to star a story NewsBlur already has)",
				"application/json (mutate_nodes create op with a temp id in the stories root: " +
					"{\"url\":\"...\",\"title\":\"...\",\"content\":\"<html>\",\"notes\":\"...\",\"user_tags\":[...]} saves an external URL " +
					"that never came from a feed as a new starred story; url and title are required, content is optional (omitted, NewsBlur fetches the page itself and may find nothing); " +
					"optional \"feed_id\"/\"rss_url\" file it under an existing feed, else it lands under feed 0; saving a URL again updates that story in place)",
				"application/json (mutate_nodes patch op: {\"read\":bool} to mark read/unread; " +
					"{\"add_tags\":[...],\"remove_tags\":[...]} to edit the tag set, starting from the story's live NewsBlur tags; " +
					"{\"user_tags\":[...],\"force\":true} to REPLACE the whole set, an empty array clears it -- refused without force, " +
					"and not combinable with add_tags/remove_tags; tags written must not be empty or contain commas or whitespace; remove_tags is not checked, so a malformed tag already on a story can be removed)",
			},
			Example: storyCreateBody{UserTags: []string{"cooking", "blogs"}},
		},
		{
			// ServerAssignedIdentity routes a mutate_nodes create op (a temp-id create) through
			// CreateChild (create_child.go, cutting-garden#143): the
			// caller passes the feeds root as the container and
			// NewsBlur assigns the feed_id, rather than the caller
			// naming a story/feed-shaped target URI up front.
			Tag: typeFeed,
			Accepts: []string{
				"application/json (mutate_nodes create op in the feeds root: {\"url\":\"...\",\"folder\":\"...\"} to subscribe)",
				"application/json (mutate_nodes patch op: {\"title\":\"...\"} and/or {\"folder\":\"...\",\"in_folder\":\"...\"} to rename/move)",
			},
			Example:                subscribeCreateBody{URL: "https://example.com/feed", Folder: "Blogs"},
			ServerAssignedIdentity: true,
		},
		{
			Tag: typeTag,
			Accepts: []string{
				"application/json (mutate_nodes patch op: {\"name\":\"new-name\"} renames this USER tag on every story that carries it; " +
					"refused if a tag with the new name already exists unless {\"force\":true} (which merges them); " +
					"feed-assigned story tags cannot be renamed)",
				"a mutate_nodes delete op removes this user tag from every story that carries it; the stories stay starred",
			},
			Example: map[string]any{"name": "new-name"},
		},
		{
			Tag: typeFolder,
			Accepts: []string{
				"application/json (mutate_nodes create op: an empty body -- the folder's own name and parent are the target URI itself, see splitFolderPath)",
				"application/json (mutate_nodes patch op: {\"name\":\"...\"} and/or {\"to_folder\":\"...\"} to rename/move)",
			},
			Example: map[string]any{"name": "NewName"},
		},
	}
}
