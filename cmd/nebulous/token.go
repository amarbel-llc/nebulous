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
	// newsblurTokenPassEntry is the piggy pass entry holding the NewsBlur
	// session cookie.
	newsblurTokenPassEntry = "newsblur-api-token"

	piggyLookupTimeout = 30 * time.Second
)

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
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
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

	raw, err := readPiggyPass(ctx, newsblurTokenPassEntry)
	if err != nil {
		return "", fmt.Errorf("NEWSBLUR_TOKEN is unset and `piggy pass show %s` failed: %w", newsblurTokenPassEntry, err)
	}

	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("NEWSBLUR_TOKEN is unset and piggy pass entry %s is empty", newsblurTokenPassEntry)
	}
	return token, nil
}
