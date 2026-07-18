package cgplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

// subscribeFakeClient wraps fakeClient with a canned Subscribe response,
// since fakeClient.record always returns "{}" and CreateChild needs a
// realistic {code,message,feed:{id}} body to parse.
type subscribeFakeClient struct {
	fakeClient
	subscribeResp json.RawMessage
	subscribeErr  error
}

func (f *subscribeFakeClient) Subscribe(_ context.Context, feedURL, folder string) (json.RawMessage, error) {
	f.calls = append(f.calls, "Subscribe:"+feedURL+":"+folder)
	if f.subscribeErr != nil {
		return nil, f.subscribeErr
	}
	return f.subscribeResp, nil
}

func TestCreateChildSubscribesAndReturnsFeedURI(t *testing.T) {
	fi := newFakeIndex()
	fc := &subscribeFakeClient{
		subscribeResp: json.RawMessage(`{"code":1,"message":"","feed":{"id":42,"feed_title":"Example"}}`),
	}
	index = fi
	client = fc
	t.Cleanup(func() { index = nil; client = nil })

	body := bytes.NewReader([]byte(`{"url":"https://example.com/feed","folder":"Blogs"}`))
	created, err := Plugin{}.CreateChild(context.Background(), mustURL(t, "newsblur://feeds"), body, typeFeed)
	if err != nil {
		t.Fatalf("CreateChild: %v", err)
	}
	if created == nil || created.String() != "newsblur://feed/42" {
		t.Errorf("created = %v, want newsblur://feed/42", created)
	}
	if len(fc.calls) != 1 || fc.calls[0] != "Subscribe:https://example.com/feed:Blogs" {
		t.Errorf("calls = %v, want [Subscribe:https://example.com/feed:Blogs]", fc.calls)
	}
}

func TestCreateChildPropagatesLogicalFailure(t *testing.T) {
	fi := newFakeIndex()
	fc := &subscribeFakeClient{
		subscribeResp: json.RawMessage(`{"code":-1,"message":"This address does not point to an RSS feed."}`),
	}
	index = fi
	client = fc
	t.Cleanup(func() { index = nil; client = nil })

	body := bytes.NewReader([]byte(`{"url":"not-a-feed"}`))
	_, err := Plugin{}.CreateChild(context.Background(), mustURL(t, "newsblur://feeds"), body, typeFeed)
	if err == nil {
		t.Fatal("CreateChild: expected an error for code:-1, got nil")
	}
}

func TestCreateChildRejectsWrongContainer(t *testing.T) {
	fi := newFakeIndex()
	fc := &subscribeFakeClient{}
	index = fi
	client = fc
	t.Cleanup(func() { index = nil; client = nil })

	body := bytes.NewReader([]byte(`{"url":"https://example.com/feed"}`))
	_, err := Plugin{}.CreateChild(context.Background(), mustURL(t, "newsblur://stories"), body, typeFeed)
	if err == nil {
		t.Fatal("CreateChild against newsblur://stories: expected an error, got nil")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none (should reject before calling the client)", fc.calls)
	}
}

func TestCreateChildRejectsWrongType(t *testing.T) {
	fi := newFakeIndex()
	fc := &subscribeFakeClient{}
	index = fi
	client = fc
	t.Cleanup(func() { index = nil; client = nil })

	body := bytes.NewReader([]byte(`{"url":"https://example.com/feed"}`))
	_, err := Plugin{}.CreateChild(context.Background(), mustURL(t, "newsblur://feeds"), body, typeStory)
	if err == nil {
		t.Fatal("CreateChild with typeStory: expected an error, got nil")
	}
}

func TestCreateChildRequiresURL(t *testing.T) {
	fi := newFakeIndex()
	fc := &subscribeFakeClient{}
	index = fi
	client = fc
	t.Cleanup(func() { index = nil; client = nil })

	body := bytes.NewReader([]byte(`{"folder":"Blogs"}`))
	_, err := Plugin{}.CreateChild(context.Background(), mustURL(t, "newsblur://feeds"), body, typeFeed)
	if err == nil {
		t.Fatal("CreateChild with no url: expected an error, got nil")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none (should reject before calling the client)", fc.calls)
	}
}

func TestCreateChildErrorsWhenNotInitialized(t *testing.T) {
	client = nil
	index = nil

	body := bytes.NewReader([]byte(`{"url":"https://example.com/feed"}`))
	if _, err := (Plugin{}).CreateChild(context.Background(), mustURL(t, "newsblur://feeds"), body, typeFeed); err == nil {
		t.Error("CreateChild with no client: expected an error, got nil")
	}
}
