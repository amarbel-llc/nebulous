package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// defaultTokenPassEntry is the piggy pass entry holding the NewsBlur
	// session cookie, unless NEBULOUS_TOKEN_PASS_KEY names another.
	defaultTokenPassEntry = "newsblur-api-token"

	piggyLookupTimeout = 30 * time.Second
)

// tokenPassEntry is the piggy pass entry to read the NewsBlur token from:
// NEBULOUS_TOKEN_PASS_KEY when set, else defaultTokenPassEntry. Which store the
// entry is looked up in is piggy's own business (PIGGY_STORE_DIR).
func tokenPassEntry() string {
	if entry := os.Getenv("NEBULOUS_TOKEN_PASS_KEY"); entry != "" {
		return entry
	}
	return defaultTokenPassEntry
}

// readPiggyPass returns the stdout of `piggy pass show <entry>`. A variable so
// tests can stand in for piggy.
var readPiggyPass = func(ctx context.Context, entry string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, piggyLookupTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "piggy", "pass", "show", entry)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// piggy's own diagnostics (a locked agent, a missing entry) say what to
		// fix and never carry the secret; stdout, which may, is not included.
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// resolveNewsblurToken returns the NewsBlur session cookie: NEWSBLUR_TOKEN
// when set (the systemd EnvironmentFile and justfile path), otherwise the piggy
// pass entry, fetched by this process itself so a session that launches
// nebulous never carries the secret in its own env or argv.
func resolveNewsblurToken(ctx context.Context) (string, error) {
	if token := os.Getenv("NEWSBLUR_TOKEN"); token != "" {
		return token, nil
	}

	entry := tokenPassEntry()
	raw, err := readPiggyPass(ctx, entry)
	if err != nil {
		return "", fmt.Errorf("NEWSBLUR_TOKEN is unset and `piggy pass show %s` failed: %w", entry, err)
	}

	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("NEWSBLUR_TOKEN is unset and piggy pass entry %s is empty", entry)
	}
	return token, nil
}
