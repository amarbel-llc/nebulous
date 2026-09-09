package tools

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"code.linenisgreat.com/purse-first/libs/go-mcp/command"
)

// maxManNameSummaryLen is the fleet ceiling on a man page's NAME-line
// description ("name - description", as lexgrog/whatis extract it): spinclass
// renders every page's NAME line into a system-prompt index, so it has to
// stay a single short clause.
const maxManNameSummaryLen = 72

// SplitManDescriptions rewrites every visible command's Description for
// artifact generation so the man page NAME line carries only a one-line
// summary while the full MCP tool description lands in DESCRIPTION.
//
// go-mcp renders Description.Short as BOTH the MCP tool description and the
// NAME line, and Description.Long as DESCRIPTION. nebulous's tool
// descriptions are deliberately long (they walk agents through the resource
// pipeline), so this runs only on the generate-plugin path -- never on the
// App `serve mcp` registers, whose tools/list descriptions stay verbatim --
// and moves each full description into Long before replacing Short with its
// first sentence. It refuses, rather than emitting an over-long NAME line,
// when any derived summary is empty, multi-line, or longer than
// maxManNameSummaryLen: `nix build` runs generate-plugin in postInstall, so a
// future tool cannot regress the index silently.
func SplitManDescriptions(app *command.App) error {
	if err := checkManNameSummary(app.Name, app.Description.Short); err != nil {
		return err
	}
	for name, cmd := range app.VisibleCommands() {
		if cmd.Description.Long == "" {
			cmd.Description.Long = cmd.Description.Short
		}
		cmd.Description.Short = firstSentence(cmd.Description.Short)
		if err := checkManNameSummary(app.Name+"-"+name, cmd.Description.Short); err != nil {
			return err
		}
	}
	return nil
}

// firstSentence returns s up to, but excluding, the first '.', '!' or '?'
// that ends the string or is followed by whitespace, so "and/or" and
// "feed/{id}" survive. Abbreviations such as "e.g." are not special-cased:
// keep a description's first sentence free of them.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '.', '!', '?':
			if i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t' || s[i+1] == '\n' {
				return strings.TrimSpace(s[:i])
			}
		}
	}
	return s
}

func checkManNameSummary(page, summary string) error {
	n := utf8.RuneCountInString(summary)
	switch {
	case summary == "":
		return fmt.Errorf("man page %s: NAME summary is empty", page)
	case strings.ContainsAny(summary, "\n\r"):
		return fmt.Errorf("man page %s: NAME summary spans multiple lines: %q", page, summary)
	case n > maxManNameSummaryLen:
		return fmt.Errorf(
			"man page %s: NAME summary is %d chars, max %d: %q",
			page, n, maxManNameSummaryLen, summary,
		)
	}
	return nil
}
