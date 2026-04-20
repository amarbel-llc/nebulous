package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestClassifyTarget(t *testing.T) {
	cases := []struct {
		in   string
		want targetKind
	}{
		{"https://example.com/", targetURL},
		{"http://example.com/a/b", targetURL},
		{"file:///tmp/x", targetURL},
		{"6327282:5d1cf5", targetStoryID},
		{"abc:def", targetStoryID},

		{"", targetInvalid},
		{":foo", targetInvalid},
		{"foo:", targetInvalid},
		{"plainword", targetInvalid},
		{"has space:inside", targetInvalid},
		{"trailing\tcolon:a", targetInvalid},
	}
	for _, c := range cases {
		if got := classifyTarget(c.in); got != c.want {
			t.Errorf("classifyTarget(%q): got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestResolveTargets_positionalMix(t *testing.T) {
	storyIDs, urls, err := resolveTargets(
		[]string{"6327282:5d1cf5", "https://example.com/", "abc:def"},
		strings.NewReader(""),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantStories := []string{"6327282:5d1cf5", "abc:def"}
	wantURLs := []string{"https://example.com/"}
	if !reflect.DeepEqual(storyIDs, wantStories) {
		t.Errorf("story IDs: got %v, want %v", storyIDs, wantStories)
	}
	if !reflect.DeepEqual(urls, wantURLs) {
		t.Errorf("URLs: got %v, want %v", urls, wantURLs)
	}
}

func TestResolveTargets_noPositionalIsError(t *testing.T) {
	_, _, err := resolveTargets(nil, strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for empty positional args")
	}
}

func TestResolveTargets_stdinSentinel(t *testing.T) {
	storyIDs, urls, err := resolveTargets(
		[]string{"-"},
		strings.NewReader("6327282:5d1cf5\n\nhttps://example.com/\n  \n"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(storyIDs, []string{"6327282:5d1cf5"}) {
		t.Errorf("story IDs: got %v", storyIDs)
	}
	if !reflect.DeepEqual(urls, []string{"https://example.com/"}) {
		t.Errorf("URLs: got %v", urls)
	}
}

func TestResolveTargets_stdinMixedWithOthersIsError(t *testing.T) {
	_, _, err := resolveTargets(
		[]string{"-", "https://example.com/"},
		strings.NewReader(""),
	)
	if err == nil {
		t.Fatal("expected error for `-` mixed with positional targets")
	}
}

func TestResolveTargets_emptyStdinIsNoop(t *testing.T) {
	storyIDs, urls, err := resolveTargets([]string{"-"}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(storyIDs) != 0 || len(urls) != 0 {
		t.Errorf("empty stdin should return empty slices, got stories=%v urls=%v", storyIDs, urls)
	}
}

func TestResolveTargets_unclassifiableIsError(t *testing.T) {
	_, _, err := resolveTargets([]string{"not-valid"}, strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for unclassifiable target")
	}
}
