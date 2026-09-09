package tools

import (
	"strings"
	"testing"
	"unicode/utf8"

	"code.linenisgreat.com/purse-first/libs/go-mcp/command"
)

func TestFirstSentence(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Search feeds by word. Returns summaries.", "Search feeds by word"},
		{"Query stories and/or words. Then more.", "Query stories and/or words"},
		{"feed/{id}/stories then story/{hash}. Next.", "feed/{id}/stories then story/{hash}"},
		{"Mark stories as read by their hashes", "Mark stories as read by their hashes"},
		{"Trailing period.", "Trailing period"},
		{"  padded?  next", "padded"},
		{"", ""},
	}
	for _, c := range cases {
		if got := firstSentence(c.in); got != c.want {
			t.Errorf("firstSentence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Mirrors the generate-plugin path exactly (RegisterAll(nil)): every man page
// nebulous ships must get a NAME line within the fleet ceiling, and the full
// description must survive into DESCRIPTION.
func TestSplitManDescriptionsRegisteredTools(t *testing.T) {
	app, _ := RegisterAll(nil)
	if err := SplitManDescriptions(app); err != nil {
		t.Fatal(err)
	}

	for name, cmd := range app.VisibleCommands() {
		if n := utf8.RuneCountInString(cmd.Description.Short); n > maxManNameSummaryLen {
			t.Errorf("%s: NAME summary %d chars > %d: %q", name, n, maxManNameSummaryLen, cmd.Description.Short)
		}
		if cmd.Description.Long == "" {
			t.Errorf("%s: DESCRIPTION is empty", name)
		}
	}

	feedQuery, ok := app.GetCommand("feed_query")
	if !ok {
		t.Fatal("feed_query not registered")
	}
	if got, want := feedQuery.Description.Short, "Search feeds by word"; got != want {
		t.Errorf("feed_query NAME summary = %q, want %q", got, want)
	}
	if !strings.Contains(feedQuery.Description.Long, "Pipeline: feed_query(words)") {
		t.Errorf("feed_query DESCRIPTION lost the pipeline walkthrough: %q", feedQuery.Description.Long)
	}
}

func TestSplitManDescriptionsRejectsOverlongSummary(t *testing.T) {
	app := command.NewApp("demo", "demo app")
	app.AddCommand(&command.Command{
		Name:        "long",
		Description: command.Description{Short: strings.Repeat("a", maxManNameSummaryLen+1)},
	})
	err := SplitManDescriptions(app)
	if err == nil {
		t.Fatal("expected an error for a summary over the ceiling")
	}
	if !strings.Contains(err.Error(), "demo-long") {
		t.Errorf("error should name the page: %v", err)
	}
}
