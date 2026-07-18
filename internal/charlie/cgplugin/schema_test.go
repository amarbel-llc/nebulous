package cgplugin

import (
	"encoding/json"
	"testing"
)

// TestDescribeBodies_DescribesWritableTypes checks the schema-discovery
// payload: every writable type (story, feed, folder) is described, with
// at least two Accepts entries each, and typeFeed alone declares
// ServerAssignedIdentity (cutting-garden#143) since it is the only type
// created via CreateChild rather than CreateNode.
func TestDescribeBodies_DescribesWritableTypes(t *testing.T) {
	bodies := Plugin{}.DescribeBodies()

	byTag := map[string]describedBody{}
	for _, b := range bodies {
		byTag[b.Tag] = describedBody{accepts: len(b.Accepts), serverAssigned: b.ServerAssignedIdentity, example: b.Example}
	}

	for _, tag := range []string{typeStory, typeFeed, typeFolder} {
		entry, ok := byTag[tag]
		if !ok {
			t.Errorf("DescribeBodies must describe %q; got %+v", tag, bodies)
			continue
		}
		if entry.accepts < 2 {
			t.Errorf("%s accepts = %d entries, want at least 2 (create + patch)", tag, entry.accepts)
		}
	}

	if !byTag[typeFeed].serverAssigned {
		t.Errorf("%s must declare ServerAssignedIdentity (created via CreateChild)", typeFeed)
	}
	for _, tag := range []string{typeStory, typeFolder} {
		if byTag[tag].serverAssigned {
			t.Errorf("%s must NOT declare ServerAssignedIdentity (created via CreateNode)", tag)
		}
	}
}

// describedBody is a minimal local projection of the DescribeBodies
// fields this test cares about.
type describedBody struct {
	accepts        int
	serverAssigned bool
	example        any
}

// TestDescribeBodies_ExamplesAreValidPayloads confirms each type's
// Example marshals to JSON that its own body-parsing logic actually
// accepts, so the schema's example is a genuinely usable payload, not
// just a JSON-shaped decoration.
func TestDescribeBodies_ExamplesAreValidPayloads(t *testing.T) {
	for _, b := range (Plugin{}).DescribeBodies() {
		raw, err := json.Marshal(b.Example)
		if err != nil {
			t.Fatalf("%s: marshal example: %v", b.Tag, err)
		}
		switch b.Tag {
		case typeStory:
			var body storyCreateBody
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("%s: example does not parse as storyCreateBody: %v", b.Tag, err)
			}
			if len(body.UserTags) == 0 {
				t.Errorf("%s: example has no user_tags", b.Tag)
			}
		case typeFeed:
			var body subscribeCreateBody
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("%s: example does not parse as subscribeCreateBody: %v", b.Tag, err)
			}
			if body.URL == "" {
				t.Errorf("%s: example has no url", b.Tag)
			}
		case typeFolder:
			var body folderPatchBody
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("%s: example does not parse as folderPatchBody: %v", b.Tag, err)
			}
			if body.Name == nil {
				t.Errorf("%s: example has no name", b.Tag)
			}
		}
	}
}
