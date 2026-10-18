package cgplugin

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	deweyerrors "code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

func patchStoryBody(t *testing.T, body string) ([]string, error) {
	t.Helper()
	return Plugin{}.PatchNode(context.Background(), mustURL(t, "newsblur://story/"+sampleHash), bytes.NewReader([]byte(body)))
}

func TestCheckTagRule(t *testing.T) {
	for _, ok := range []string{"a", "zz-nyc", "list-to_read", "UPPER", "ünïcode", "proj-mephisto"} {
		if err := checkTag(ok); err != nil {
			t.Errorf("checkTag(%q) = %v, want accepted", ok, err)
		}
	}
	for _, bad := range []string{"", "a,b", "a b", " lead", "trail ", "tab\there", "new\nline", "project ▸ 2022"} {
		err := checkTag(bad)
		if err == nil || !deweyerrors.Is400BadRequest(err) {
			t.Errorf("checkTag(%q) = %v, want a bad-request rejection", bad, err)
		}
	}
}

func TestTagSetDedupesKeepingFirstOrder(t *testing.T) {
	got, err := tagSet([]string{"b", "a", "b", "c", "a"})
	if err != nil || !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Errorf("tagSet = %v, %v; want [b a c]", got, err)
	}
	if got, err := tagSet(nil); got != nil || err != nil {
		t.Errorf("tagSet(nil) = %v, %v; want nil, nil", got, err)
	}
	if _, err := tagSet([]string{"ok", "a,b"}); err == nil {
		t.Error("tagSet accepted a tag with a comma")
	}
}

func TestApplyTagEdit(t *testing.T) {
	for name, tc := range map[string]struct{ cur, add, rem, want []string }{
		"add new":                 {[]string{"x"}, []string{"y"}, nil, []string{"x", "y"}},
		"add existing is a no-op": {[]string{"x"}, []string{"x"}, nil, []string{"x"}},
		"remove":                  {[]string{"x", "y"}, nil, []string{"x"}, []string{"y"}},
		"remove absent":           {[]string{"x"}, nil, []string{"z"}, []string{"x"}},
		"both":                    {[]string{"x", "y"}, []string{"z"}, []string{"x"}, []string{"y", "z"}},
		"odd existing preserved":  {[]string{"", "a b"}, []string{"n"}, nil, []string{"", "a b", "n"}},
	} {
		if got := applyTagEdit(tc.cur, tc.add, tc.rem); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: applyTagEdit = %v, want %v", name, got, tc.want)
		}
	}
}

func TestReplaceTagsRefusedWithoutForce(t *testing.T) {
	_, fc := setupMutateTest(t)
	for _, body := range []string{`{"user_tags":["a"]}`, `{"user_tags":[]}`} {
		applied, err := patchStoryBody(t, body)
		if err == nil || !deweyerrors.Is400BadRequest(err) {
			t.Errorf("%s: err = %v, want a bad request", body, err)
		}
		if err != nil && !strings.Contains(err.Error(), "force") {
			t.Errorf("%s: error %q should tell the caller about force", body, err)
		}
		if applied != nil {
			t.Errorf("%s: applied = %v, want nil", body, applied)
		}
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none: a refused replace must not touch NewsBlur", fc.calls)
	}
}

func TestReplaceTagsValidatesAndDedupes(t *testing.T) {
	_, fc := setupMutateTest(t)

	applied, err := patchStoryBody(t, `{"user_tags":["a","b","a"],"force":true}`)
	if err != nil {
		t.Fatal(err)
	}
	assertApplied(t, applied, []string{"user_tags"})
	if len(fc.calls) != 1 || fc.calls[0] != "SetStoryUserTags:"+sampleHash+":a,b" {
		t.Errorf("calls = %v, want one write of the de-duplicated set a,b", fc.calls)
	}

	fc.calls = nil
	for _, bad := range []string{`{"user_tags":["a,b"],"force":true}`, `{"user_tags":["a b"],"force":true}`, `{"user_tags":[""],"force":true}`} {
		if _, err := patchStoryBody(t, bad); err == nil || !deweyerrors.Is400BadRequest(err) {
			t.Errorf("%s: err = %v, want a bad request", bad, err)
		}
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none for invalid tags", fc.calls)
	}
}

func TestInvalidTagDoesNotHalfApplyAReadChange(t *testing.T) {
	_, fc := setupMutateTest(t)
	if _, err := patchStoryBody(t, `{"read":true,"user_tags":["a,b"],"force":true}`); err == nil {
		t.Fatal("expected an error")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none: validation must run before any write", fc.calls)
	}
}

func TestAddAndRemoveTagsStartFromLiveTags(t *testing.T) {
	_, fc := setupMutateTest(t)
	fc.live = map[string][]string{sampleHash: {"keep", "drop"}}

	applied, err := patchStoryBody(t, `{"add_tags":["new"],"remove_tags":["drop"]}`)
	if err != nil {
		t.Fatal(err)
	}
	assertApplied(t, applied, []string{"add_tags", "remove_tags"})
	want := []string{"LiveStoryUserTags:" + sampleHash, "SetStoryUserTags:" + sampleHash + ":keep,new"}
	if !reflect.DeepEqual(fc.calls, want) {
		t.Errorf("calls = %v, want %v", fc.calls, want)
	}
}

func TestAddTagsAlreadyPresentWritesNothing(t *testing.T) {
	_, fc := setupMutateTest(t)
	fc.live = map[string][]string{sampleHash: {"x", "y"}}

	applied, err := patchStoryBody(t, `{"add_tags":["y"],"remove_tags":["absent"]}`)
	if err != nil {
		t.Fatal(err)
	}
	assertApplied(t, applied, []string{"add_tags", "remove_tags"})
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "SetStoryUserTags:") {
			t.Errorf("calls = %v, want no write: the edit changes nothing", fc.calls)
		}
	}
}

func TestAddTagsToUntaggedStory(t *testing.T) {
	_, fc := setupMutateTest(t) // live[sampleHash] is nil: an untagged story
	if _, err := patchStoryBody(t, `{"add_tags":["first"]}`); err != nil {
		t.Fatal(err)
	}
	if got := fc.calls[len(fc.calls)-1]; got != "SetStoryUserTags:"+sampleHash+":first" {
		t.Errorf("last call = %q, want a write of the single tag", got)
	}
}

// A malformed tag already on a story (comma-joined, trailing space, empty)
// must be removable, or it could never be cleaned up; adding one still isn't.
func TestMalformedTagsCanBeRemovedAndSplit(t *testing.T) {
	_, fc := setupMutateTest(t)
	fc.live = map[string][]string{sampleHash: {"proj-mephisto,zz-dsp", "keep", "padded ", ""}}

	body := `{"add_tags":["proj-mephisto","zz-dsp"],"remove_tags":["proj-mephisto,zz-dsp","padded ",""]}`
	if _, err := patchStoryBody(t, body); err != nil {
		t.Fatal(err)
	}
	want := []string{"LiveStoryUserTags:" + sampleHash, "SetStoryUserTags:" + sampleHash + ":keep,proj-mephisto,zz-dsp"}
	if !reflect.DeepEqual(fc.calls, want) {
		t.Errorf("calls = %v, want %v", fc.calls, want)
	}
}

func TestRemovingTheLastTagIsRefused(t *testing.T) {
	_, fc := setupMutateTest(t)
	fc.live = map[string][]string{sampleHash: {"only"}}

	_, err := patchStoryBody(t, `{"remove_tags":["only"]}`)
	if err == nil || !deweyerrors.Is400BadRequest(err) {
		t.Fatalf("err = %v, want a bad request", err)
	}
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "SetStoryUserTags:") {
			t.Errorf("calls = %v, want no write", fc.calls)
		}
	}
}

func TestTagEditBodyErrors(t *testing.T) {
	_, fc := setupMutateTest(t)
	for name, body := range map[string]string{
		"replace combined with add":    `{"user_tags":["a"],"force":true,"add_tags":["b"]}`,
		"replace combined with remove": `{"user_tags":["a"],"force":true,"remove_tags":["b"]}`,
		"same tag added and removed":   `{"add_tags":["a"],"remove_tags":["a"]}`,
		"invalid added tag":            `{"add_tags":["a b"]}`,
	} {
		if _, err := patchStoryBody(t, body); err == nil || !deweyerrors.Is400BadRequest(err) {
			t.Errorf("%s: err = %v, want a bad request", name, err)
		}
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
	}
}

func TestLiveTagReadFailureAbortsWithoutWriting(t *testing.T) {
	_, fc := setupMutateTest(t)
	fc.liveErr = errors.New("newsblur unreachable")

	_, err := patchStoryBody(t, `{"add_tags":["x"]}`)
	if err == nil || deweyerrors.Is400BadRequest(err) {
		t.Fatalf("err = %v, want a non-bad-request failure", err)
	}
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "SetStoryUserTags:") {
			t.Errorf("calls = %v, want no write after a failed live read", fc.calls)
		}
	}
}

func TestBulkSweepReplaceWithoutForceFailsPerItem(t *testing.T) {
	_, fc := setupMutateTest(t)
	storyURI := mustURL(t, "newsblur://story/"+sampleHash)

	result, err := Plugin{}.BulkMutate(context.Background(), cg.BulkRequest{
		Atomicity: cg.BulkBestEffort,
		Ops:       []cg.BulkOp{{Kind: cg.BulkPatch, URI: storyURI, Body: []byte(`{"user_tags":["a"]}`)}},
	})
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}
	if len(result.Failed) != 1 || len(result.AppliedNodes) != 0 {
		t.Errorf("Failed = %v, AppliedNodes = %v; want the unforced replace to fail", result.Failed, result.AppliedNodes)
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
	}
}

func TestCreateStoryValidatesTags(t *testing.T) {
	_, fc := setupMutateTest(t)
	err := Plugin{}.CreateNode(context.Background(), mustURL(t, "newsblur://story/123:new"),
		bytes.NewReader([]byte(`{"user_tags":["fine","not fine"]}`)), typeStory)
	if err == nil || !deweyerrors.Is400BadRequest(err) {
		t.Errorf("err = %v, want a bad request for a tag with a space", err)
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none", fc.calls)
	}
}
