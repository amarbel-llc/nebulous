package main

import (
	"context"
	"strings"
	"testing"
)

const envAccount = `[[accounts]]
name = "newsblur"
password_source = "env"
password_key = "NEBULOUS_TEST_TOKEN"
`

func TestNewsblurTokenSourceReadsTheNamedCredential(t *testing.T) {
	t.Setenv("NEBULOUS_TEST_TOKEN", "  s3cret\n")

	source, err := newsblurTokenSource(envAccount)
	if err != nil || source == nil {
		t.Fatalf("newsblurTokenSource = %v, %v; want a source", source != nil, err)
	}
	got, err := source(context.Background())
	if err != nil || got != "s3cret" {
		t.Fatalf("source() = %q, %v; want s3cret (whitespace trimmed)", got, err)
	}
}

func TestNewsblurTokenSourceStaysReadOnlyWithoutAnAccount(t *testing.T) {
	for _, toml := range []string{"", "  \n", "other = 1\n"} {
		source, err := newsblurTokenSource(toml)
		if source != nil || err != nil {
			t.Errorf("newsblurTokenSource(%q) = %v, %v; want nil, nil", toml, source != nil, err)
		}
	}
}

func TestNewsblurTokenSourceRejectsBadAccounts(t *testing.T) {
	cases := []struct {
		name, toml, want string
	}{
		{"two accounts", envAccount + envAccount, "at most one account"},
		{"no name", "[[accounts]]\npassword_source = \"env\"\npassword_key = \"X\"\n", "needs a name"},
		{"url", "[[accounts]]\nname = \"a\"\nurl = \"https://example.com\"\npassword_source = \"env\"\npassword_key = \"X\"\n", "url is not used"},
		{"no credential", "[[accounts]]\nname = \"a\"\n", "set password_source"},
		{"source without key", "[[accounts]]\nname = \"a\"\npassword_source = \"piggy\"\n", "must be set together"},
		{"unknown source", "[[accounts]]\nname = \"a\"\npassword_source = \"vault\"\npassword_key = \"X\"\n", "unknown password_source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source, err := newsblurTokenSource(tc.toml)
			if source != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("newsblurTokenSource = %v, %v; want an error containing %q", source != nil, err, tc.want)
			}
		})
	}
}

func TestNewsblurTokenSourceReportsAMissingPiggyOnUse(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	source, err := newsblurTokenSource("[[accounts]]\nname = \"a\"\npassword_source = \"piggy\"\npassword_key = \"newsblur-api-token\"\n")
	if err != nil || source == nil {
		t.Fatalf("decoding must not touch piggy: %v, source=%v", err, source != nil)
	}
	if _, err := source(context.Background()); err == nil || !strings.Contains(err.Error(), "piggy") {
		t.Fatalf("source() = %v; want an error naming piggy", err)
	}
}
