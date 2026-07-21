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
			Accepts: []string{
				"application/json (create_node: {\"user_tags\":[...]} to star the story)",
				"application/json (patch_node: {\"read\":bool} to mark read/unread; " +
					"{\"user_tags\":[...]} to REPLACE the tag set, an empty array clears it)",
			},
			Example: storyCreateBody{UserTags: []string{"cooking", "blogs"}},
		},
		{
			// ServerAssignedIdentity routes create_node through
			// CreateChild (create_child.go, cutting-garden#143): the
			// caller passes the feeds root as the container and
			// NewsBlur assigns the feed_id, rather than the caller
			// naming a story/feed-shaped target URI up front.
			Tag: typeFeed,
			Accepts: []string{
				"application/json (create_node against the feeds root: {\"url\":\"...\",\"folder\":\"...\"} to subscribe)",
				"application/json (patch_node: {\"title\":\"...\"} and/or {\"folder\":\"...\",\"in_folder\":\"...\"} to rename/move)",
			},
			Example:                subscribeCreateBody{URL: "https://example.com/feed", Folder: "Blogs"},
			ServerAssignedIdentity: true,
		},
		{
			Tag: typeFolder,
			Accepts: []string{
				"application/json (create_node: an empty body -- the folder's own name and parent are the target URI itself, see splitFolderPath)",
				"application/json (patch_node: {\"name\":\"...\"} and/or {\"to_folder\":\"...\"} to rename/move)",
			},
			Example: map[string]any{"name": "NewName"},
		},
	}
}
