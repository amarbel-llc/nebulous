package policy

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAll_validFile(t *testing.T) {
	policies, err := LoadAll(filepath.Join("testdata", "valid.toml"))
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(policies))
	}
	if policies[0].ID != "starred-default" {
		t.Errorf("policies[0].ID: got %q, want starred-default", policies[0].ID)
	}
	if len(policies[0].Captures) != 2 {
		t.Errorf("policies[0].Captures: got %d, want 2", len(policies[0].Captures))
	}
	if policies[0].Isolation != "fresh" {
		t.Errorf("policies[0].Isolation: got %q, want fresh", policies[0].Isolation)
	}
	if policies[1].ID != "screenshot-only" {
		t.Errorf("policies[1].ID: got %q", policies[1].ID)
	}
	if policies[1].Isolation != "fresh" {
		t.Errorf("policies[1].Isolation default: got %q, want fresh", policies[1].Isolation)
	}
}

func TestLoadAll_rejectsMissingID(t *testing.T) {
	_, err := LoadAll(filepath.Join("testdata", "bad-missing-id.toml"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Errorf("error should mention `id is required`, got %v", err)
	}
}

func TestLoadAll_rejectsUnknownFormat(t *testing.T) {
	_, err := LoadAll(filepath.Join("testdata", "bad-unknown-format.toml"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "format") {
		t.Errorf("error should mention format, got %v", err)
	}
}

func TestExpandURL_happyPath(t *testing.T) {
	ctx := TemplateContext{Story: Story{
		Permalink: "https://example.com/article",
		Hash:      "deadbeef",
		Title:     "An Article",
	}}
	got, err := ExpandURL("{{.Story.Permalink}}", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/article" {
		t.Errorf("got %q", got)
	}
}

func TestExpandURL_unknownFieldErrors(t *testing.T) {
	ctx := TemplateContext{Story: Story{Permalink: "x"}}
	_, err := ExpandURL("{{.Story.Prmalink}}", ctx)
	if err == nil {
		t.Fatal("expected error on typo'd field, got nil")
	}
}
