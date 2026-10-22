package main

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func stubPiggy(t *testing.T, out string, err error) *[]string {
	t.Helper()
	var entries []string
	prev := readPiggyPass
	readPiggyPass = func(_ context.Context, entry string) ([]byte, error) {
		entries = append(entries, entry)
		return []byte(out), err
	}
	t.Cleanup(func() { readPiggyPass = prev })
	return &entries
}

func TestResolveNewsblurTokenPrefersEnv(t *testing.T) {
	t.Setenv("NEWSBLUR_TOKEN", "from-env")
	entries := stubPiggy(t, "from-piggy\n", nil)

	got, err := resolveNewsblurToken(context.Background())
	if err != nil || got != "from-env" {
		t.Fatalf("got %q, %v; want from-env", got, err)
	}
	if len(*entries) != 0 {
		t.Fatalf("piggy was consulted despite NEWSBLUR_TOKEN: %v", *entries)
	}
}

func TestResolveNewsblurTokenFallsBackToPiggy(t *testing.T) {
	t.Setenv("NEWSBLUR_TOKEN", "")
	entries := stubPiggy(t, "from-piggy\n", nil)

	got, err := resolveNewsblurToken(context.Background())
	if err != nil || got != "from-piggy" {
		t.Fatalf("got %q, %v; want from-piggy (trailing newline trimmed)", got, err)
	}
	if len(*entries) != 1 || (*entries)[0] != newsblurTokenPassEntry {
		t.Fatalf("piggy entries = %v, want [%s]", *entries, newsblurTokenPassEntry)
	}
}

func TestResolveNewsblurTokenErrors(t *testing.T) {
	cases := []struct {
		name    string
		out     string
		err     error
		wantMsg string
	}{
		{"piggy missing", "", &exec.Error{Name: "piggy", Err: exec.ErrNotFound}, "not found"},
		{"piggy fails", "", errors.New("agent locked"), "failed"},
		{"empty entry", "  \n", nil, "is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NEWSBLUR_TOKEN", "")
			stubPiggy(t, tc.out, tc.err)

			got, err := resolveNewsblurToken(context.Background())
			if err == nil || got != "" {
				t.Fatalf("got %q, %v; want an error and no token", got, err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error %q does not mention %q", err, tc.wantMsg)
			}
		})
	}
}
