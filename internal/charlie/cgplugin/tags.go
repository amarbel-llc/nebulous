package cgplugin

import (
	"strings"
	"unicode"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// checkTag is THE definition of a writable tag; every tag this plugin sends
// to NewsBlur goes through it. It is deliberately one small function so the
// rule can be swapped without touching the mutation code.
//
// Current rule (a stand-in, per the operator: "tags cannot contain commas
// or spaces"): non-empty, no comma, no whitespace. It is NOT yet dodder's
// full tag grammar (`^%?[-a-z0-9_]+$` after lowercasing and trimming); which
// rule the NewsBlur tags should follow is still to be decided with the
// operator, and tags already in the account that break even this rule (an
// empty tag, a tag with a trailing space) are left alone, never rewritten
// here.
func checkTag(tag string) error {
	switch {
	case tag == "":
		return errors.BadRequestf("newsblur plugin: a tag must not be empty")
	case strings.ContainsRune(tag, ','):
		return errors.BadRequestf("newsblur plugin: tag %q must not contain a comma", tag)
	case strings.IndexFunc(tag, unicode.IsSpace) >= 0:
		return errors.BadRequestf("newsblur plugin: tag %q must not contain whitespace", tag)
	}
	return nil
}

// tagSet validates every tag and returns them as a set: duplicates dropped,
// first-occurrence order kept (so a set round-trips stably). nil in, nil out.
func tagSet(tags []string) ([]string, error) {
	if tags == nil {
		return nil, nil
	}
	out := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		if err := checkTag(tag); err != nil {
			return nil, err
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out, nil
}

// applyTagEdit returns current minus remove plus add: current's order is
// kept, tags already present are not repeated, new tags are appended in the
// order given. current is taken as-is (including any tag checkTag would
// reject: an existing odd tag is preserved, not silently dropped).
func applyTagEdit(current, add, remove []string) []string {
	drop := make(map[string]struct{}, len(remove))
	for _, tag := range remove {
		drop[tag] = struct{}{}
	}
	out := make([]string, 0, len(current)+len(add))
	have := make(map[string]struct{}, len(current)+len(add))
	for _, tag := range current {
		if _, gone := drop[tag]; gone {
			continue
		}
		if _, dup := have[tag]; dup {
			continue
		}
		have[tag] = struct{}{}
		out = append(out, tag)
	}
	for _, tag := range add {
		if _, dup := have[tag]; dup {
			continue
		}
		have[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

// sameTagSet reports whether a and b hold the same tags, ignoring order.
func sameTagSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	in := make(map[string]struct{}, len(a))
	for _, tag := range a {
		in[tag] = struct{}{}
	}
	for _, tag := range b {
		if _, ok := in[tag]; !ok {
			return false
		}
	}
	return true
}
