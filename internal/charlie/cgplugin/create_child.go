package cgplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

var _ cg.ContainerCreator = Plugin{}

// subscribeCreateBody is CreateChild's payload for a feed node: NewsBlur
// assigns the feed_id server-side (POST /reader/add_url), so the caller
// cannot name the target URI up front the way CreateNode requires --
// this is the reason CreateChild (cutting-garden#143) exists at all.
type subscribeCreateBody struct {
	URL    string `json:"url"`
	Folder string `json:"folder,omitempty"`
}

// subscribeResponse is the shape of NewsBlur's raw /reader/add_url
// response, verified against the server's own source
// (samuelclay/NewsBlur apps/reader/views.py's add_url view, which
// returns dict(code=code, message=message, feed=feed) with a Feed model
// instance serialized via its own canonical() method -- see
// utils/json_functions.py's encoder, which checks for a "canonical"
// method before falling back to other type handling). code == 1 is
// success; any other value (the view only ever sets -1 on failure, but
// this treats anything other than 1 as failure defensively) means no
// feed was created, with message carrying the reason. NewsBlur returns
// HTTP 200 either way -- the same "any 200 is not success" caveat
// patchFeed's doc comment documents elsewhere in this package -- so this
// must be parsed rather than trusting a successful POST.
type subscribeResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Feed    *struct {
		ID int `json:"id"`
	} `json:"feed"`
}

// CreateChild supports exactly one container/type pair: the feeds root
// (newsblur://feeds), creating a typeFeed child by subscribing to a URL.
// It is the ContainerCreator counterpart to CreateNode's story/{hash}
// and folder/{path} cases (mutate.go), for the one write nebulous has
// that assigns identity server-side.
func (Plugin) CreateChild(ctx context.Context, container *url.URL, body io.Reader, typ string) (*url.URL, error) {
	if container == nil {
		return nil, fmt.Errorf("newsblur plugin: CreateChild requires a container URI")
	}
	if client == nil {
		return nil, fmt.Errorf("newsblur plugin: not initialized")
	}

	segs := pathSegments(container)
	if len(segs) != 1 || segs[0] != "feeds" {
		return nil, fmt.Errorf("newsblur plugin: CreateChild only supports the feeds root (newsblur://feeds), got %s", container)
	}
	if typ != "" && typ != typeFeed {
		return nil, fmt.Errorf("newsblur plugin: CreateChild: unexpected type %q for the feeds root (want %q)", typ, typeFeed)
	}

	var payload subscribeCreateBody
	if body != nil {
		raw, err := io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("newsblur plugin: reading CreateChild body: %w", err)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			return nil, fmt.Errorf("newsblur plugin: CreateChild requires a body with a \"url\" field")
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, fmt.Errorf("newsblur plugin: invalid CreateChild body: %w", err)
		}
	}
	if payload.URL == "" {
		return nil, fmt.Errorf("newsblur plugin: CreateChild: body's \"url\" field is required")
	}

	mutateMu.Lock()
	raw, err := client.Subscribe(ctx, payload.URL, payload.Folder)
	mutateMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("newsblur plugin: subscribing to %s: %w", payload.URL, err)
	}

	var resp subscribeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("newsblur plugin: parsing subscribe response: %w", err)
	}
	if resp.Code != 1 {
		msg := resp.Message
		if msg == "" {
			msg = "subscribe failed"
		}
		return nil, fmt.Errorf("newsblur plugin: subscribing to %s: %s", payload.URL, msg)
	}
	if resp.Feed == nil || resp.Feed.ID == 0 {
		return nil, fmt.Errorf("newsblur plugin: subscribe response for %s carries no feed id", payload.URL)
	}

	return nodeURL("feed", strconv.Itoa(resp.Feed.ID)), nil
}
