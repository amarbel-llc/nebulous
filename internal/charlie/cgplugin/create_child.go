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
	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
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

// CreateChild supports two container/type pairs, both writes where NewsBlur
// assigns the identity server-side: the feeds root (newsblur://feeds),
// creating a typeFeed child by subscribing to a URL, and the stories root
// (newsblur://stories), creating a typeStory child by saving an external
// URL. It is the ContainerCreator counterpart to CreateNode's story/{hash}
// (star an existing story) and folder/{path} cases (mutate.go).
func (Plugin) CreateChild(ctx context.Context, container *url.URL, body io.Reader, typ string) (*url.URL, error) {
	if container == nil {
		return nil, errors.BadRequestf("newsblur plugin: CreateChild requires a container URI")
	}
	if client == nil {
		return nil, fmt.Errorf("newsblur plugin: not initialized")
	}

	segs := pathSegments(container)
	switch {
	case len(segs) == 1 && segs[0] == "feeds":
		return subscribeChild(ctx, body, typ)
	case len(segs) == 1 && segs[0] == "stories":
		return saveStoryChild(ctx, body, typ)
	default:
		return nil, errors.BadRequestf("newsblur plugin: CreateChild only supports the feeds root (newsblur://feeds) and the stories root (newsblur://stories), got %s", container)
	}
}

func subscribeChild(ctx context.Context, body io.Reader, typ string) (*url.URL, error) {
	if typ != "" && typ != typeFeed {
		return nil, errors.BadRequestf("newsblur plugin: CreateChild: unexpected type %q for the feeds root (want %q)", typ, typeFeed)
	}

	var payload subscribeCreateBody
	if body != nil {
		raw, err := io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("newsblur plugin: reading CreateChild body: %w", err)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			return nil, errors.BadRequestf("newsblur plugin: CreateChild requires a body with a \"url\" field")
		}
		if err := strictUnmarshal(raw, &payload); err != nil {
			return nil, errors.BadRequestf("newsblur plugin: invalid CreateChild body: %w", err)
		}
	}
	if payload.URL == "" {
		return nil, errors.BadRequestf("newsblur plugin: CreateChild: body's \"url\" field is required")
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

// saveStoryBody is CreateChild's payload for a story node under the stories
// root: an external URL to save as a starred story that never came from a
// feed (POST /api/save_story). Title is required by NewsBlur. Content is
// optional; left out, NewsBlur fetches and extracts the page itself, which
// can come back empty. FeedID/RSSURL optionally file the story under an
// existing feed; without them it lands under feed 0.
type saveStoryBody struct {
	URL      string   `json:"url"`
	Title    string   `json:"title"`
	Content  string   `json:"content,omitempty"`
	Notes    string   `json:"notes,omitempty"`
	UserTags []string `json:"user_tags,omitempty"`
	FeedID   int      `json:"feed_id,omitempty"`
	RSSURL   string   `json:"rss_url,omitempty"`
}

// saveStoryChild saves an external URL as a starred story and returns the
// story/{hash} URI NewsBlur assigned. Saving an already-saved URL updates
// that story in place and returns the same URI.
func saveStoryChild(ctx context.Context, body io.Reader, typ string) (*url.URL, error) {
	if typ != "" && typ != typeStory {
		return nil, errors.BadRequestf("newsblur plugin: CreateChild: unexpected type %q for the stories root (want %q)", typ, typeStory)
	}

	var payload saveStoryBody
	if body != nil {
		raw, err := io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("newsblur plugin: reading CreateChild body: %w", err)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			return nil, errors.BadRequestf("newsblur plugin: CreateChild requires a body with \"url\" and \"title\" fields")
		}
		if err := strictUnmarshal(raw, &payload); err != nil {
			return nil, errors.BadRequestf("newsblur plugin: invalid CreateChild body: %w", err)
		}
	}
	if payload.URL == "" {
		return nil, errors.BadRequestf("newsblur plugin: CreateChild: body's \"url\" field is required")
	}
	if payload.Title == "" {
		return nil, errors.BadRequestf("newsblur plugin: CreateChild: body's \"title\" field is required")
	}
	if payload.FeedID < 0 {
		return nil, errors.BadRequestf("newsblur plugin: CreateChild: body's \"feed_id\" must not be negative, got %d", payload.FeedID)
	}
	tags, err := tagSet(payload.UserTags)
	if err != nil {
		return nil, err
	}

	mutateMu.Lock()
	saved, err := client.SaveStory(ctx, newsblur.SaveStoryInput{
		URL:     payload.URL,
		Title:   payload.Title,
		Content: payload.Content,
		Notes:   payload.Notes,
		Tags:    tags,
		FeedID:  payload.FeedID,
		RSSURL:  payload.RSSURL,
	})
	mutateMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("newsblur plugin: saving %s: %w", payload.URL, err)
	}
	if saved.Hash == "" {
		return nil, fmt.Errorf("newsblur plugin: saving %s: NewsBlur returned no story hash", payload.URL)
	}

	return nodeURL("story", saved.Hash), nil
}
