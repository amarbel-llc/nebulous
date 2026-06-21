package cgplugin

import (
	"net/url"
	"strings"
)

// Scheme is the URI scheme this plugin handles.
const Scheme = "newsblur"

// nodeURL builds a newsblur:// URL from logical path segments. The first
// segment lands in Host, the remainder in Path, so it round-trips
// through pathSegments. Story hashes (feed_id:guid) and feed ids pass
// through unescaped; tags containing a slash are not supported (rare).
func nodeURL(segments ...string) *url.URL {
	u := &url.URL{Scheme: Scheme}
	if len(segments) > 0 {
		u.Host = segments[0]
	}
	if len(segments) > 1 {
		u.Path = "/" + strings.Join(segments[1:], "/")
	}
	return u
}

// pathSegments decomposes a newsblur:// URL into its logical path
// segments (Host followed by the non-empty Path elements).
func pathSegments(u *url.URL) []string {
	segs := make([]string, 0, 3)
	if u.Host != "" {
		segs = append(segs, u.Host)
	}
	for _, p := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		if p != "" {
			segs = append(segs, p)
		}
	}
	return segs
}
