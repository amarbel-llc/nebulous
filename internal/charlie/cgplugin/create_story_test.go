package cgplugin

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
	deweyerrors "code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

func setupSaveStoryTest(t *testing.T) *fakeClient {
	t.Helper()
	_, fc := setupMutateTest(t)
	fc.saved = newsblur.SavedStory{Hash: "0:45788b", FeedID: 0}
	return fc
}

func createStoryChild(t *testing.T, container, body, typ string) (string, error) {
	t.Helper()
	created, err := Plugin{}.CreateChild(context.Background(), mustURL(t, container), bytes.NewReader([]byte(body)), typ)
	if err != nil {
		return "", err
	}
	return created.String(), nil
}

func TestCreateChildSavesExternalStoryAndReturnsItsURI(t *testing.T) {
	fc := setupSaveStoryTest(t)

	got, err := createStoryChild(t, "newsblur://stories",
		`{"url":"https://example.com/a","title":"A title","content":"<p>x</p>","notes":"n","user_tags":["poc","two"]}`,
		typeStory)
	if err != nil {
		t.Fatalf("CreateChild: %v", err)
	}
	if got != "newsblur://story/0:45788b" {
		t.Errorf("created = %q, want newsblur://story/0:45788b", got)
	}
	want := "SaveStory:https://example.com/a|A title|<p>x</p>|n|poc,two|0|"
	if len(fc.calls) != 1 || fc.calls[0] != want {
		t.Errorf("calls = %v, want [%s]", fc.calls, want)
	}
}

func TestCreateChildSavesExternalStoryAttachedToAFeed(t *testing.T) {
	fc := setupSaveStoryTest(t)

	if _, err := createStoryChild(t, "newsblur://stories",
		`{"url":"https://example.com/a","title":"A title","feed_id":123,"rss_url":"https://example.com/feed"}`,
		typeStory); err != nil {
		t.Fatalf("CreateChild: %v", err)
	}
	want := "SaveStory:https://example.com/a|A title||||123|https://example.com/feed"
	if len(fc.calls) != 1 || fc.calls[0] != want {
		t.Errorf("calls = %v, want [%s]", fc.calls, want)
	}
}

func TestCreateChildStoryRejectsBadBodiesBeforeCallingNewsBlur(t *testing.T) {
	cases := map[string]string{
		"missing url":   `{"title":"A title"}`,
		"missing title": `{"url":"https://example.com/a"}`,
		"empty body":    ``,
		"unknown field": `{"url":"https://example.com/a","title":"t","bogus":1}`,
		"bad tag":       `{"url":"https://example.com/a","title":"t","user_tags":["a b"]}`,
		"negative feed": `{"url":"https://example.com/a","title":"t","feed_id":-1}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			fc := setupSaveStoryTest(t)
			_, err := createStoryChild(t, "newsblur://stories", body, typeStory)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !deweyerrors.Is400BadRequest(err) {
				t.Errorf("error %v is not a caller-fault BadRequest", err)
			}
			if len(fc.calls) != 0 {
				t.Errorf("calls = %v, want none (reject before calling NewsBlur)", fc.calls)
			}
		})
	}
}

func TestCreateChildStoryRejectsWrongContainerOrType(t *testing.T) {
	fc := setupSaveStoryTest(t)
	body := `{"url":"https://example.com/a","title":"t"}`

	if _, err := createStoryChild(t, "newsblur://stories", body, typeFeed); err == nil {
		t.Error("stories root with feed type: expected an error")
	}
	if _, err := createStoryChild(t, "newsblur://tags", body, typeStory); err == nil {
		t.Error("tags root: expected an error")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
	}
}

func TestCreateChildStoryPropagatesNewsBlurFailureAsServerFault(t *testing.T) {
	fc := setupSaveStoryTest(t)
	fc.err = errors.New("newsblur rejected the request: Not authenticated")

	_, err := createStoryChild(t, "newsblur://stories", `{"url":"https://example.com/a","title":"t"}`, typeStory)
	if err == nil {
		t.Fatal("expected an error")
	}
	if deweyerrors.Is400BadRequest(err) {
		t.Errorf("a NewsBlur failure %v must not be classified as a caller fault", err)
	}
}

func TestCreateChildStoryNeedsAHashInTheResult(t *testing.T) {
	fc := setupSaveStoryTest(t)
	fc.saved = newsblur.SavedStory{}

	if _, err := createStoryChild(t, "newsblur://stories", `{"url":"https://example.com/a","title":"t"}`, typeStory); err == nil {
		t.Fatal("expected an error when NewsBlur returns no hash")
	}
}

func TestStoryBodyAcceptsBothCreateShapes(t *testing.T) {
	var story *struct{ accepts []string }
	for _, b := range (Plugin{}).DescribeBodies() {
		if b.Tag == typeStory {
			story = &struct{ accepts []string }{b.Accepts}
			if b.ServerAssignedIdentity {
				t.Error("story ServerAssignedIdentity is true: that would make cutting-garden refuse create-by-hash (star an existing story)")
			}
		}
	}
	if story == nil {
		t.Fatal("no story body described")
	}
	joined := ""
	for _, a := range story.accepts {
		joined += a + "\n"
	}
	for _, want := range []string{"stories root", "\"url\""} {
		if !bytes.Contains([]byte(joined), []byte(want)) {
			t.Errorf("story Accepts does not mention %q:\n%s", want, joined)
		}
	}
}
