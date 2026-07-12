package cgplugin

import cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"

// init registers the newsblur plugin in the cutting-garden scheme
// registry. The plugin implements none of capture/restore/diff, so it
// registers via MustRegisterScheme (RFC 0005, RFC 0009 §3) rather than a
// direction registry; it is discovered by `list`/`mcp`/`health` and
// probed for RootProvider/LeafReader by type assertion.
func init() {
	cg.MustRegisterScheme(Plugin{})
}
