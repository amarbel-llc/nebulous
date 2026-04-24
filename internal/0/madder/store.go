// Package madder wraps the `madder` CLI (github.com/amarbel-llc/madder) as a
// content-addressed blob store. Nebulous shells out to madder for blob
// read/write/has operations; madder owns on-disk storage under its own XDG
// tree (opaque to nebulous).
//
// The Bin var holds the path to the madder binary. In Nix builds it is
// overridden at link time via ldflags so the flake input is the source of
// truth for the paired madder version; dev builds fall back to PATH lookup.
package madder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Bin is the path to the madder executable. Set at build time via:
//
//	-ldflags "-X github.com/friedenberg/nebulous/internal/0/madder.Bin=<path>"
//
// When unset, resolves via $PATH.
var Bin = "madder"

// Store wraps `madder` invocations. Its lifetime context is used for every
// exec call, so cancelling the context terminates in-flight madder processes.
type Store struct {
	ctx     context.Context
	storeId string
	// workDir is the CWD every madder invocation runs from. Madder's
	// `ResolveFileOrBlobStoreId` tries to open positional args as files
	// before falling back to store-id parsing, so the CWD must be free of
	// files named "nebulous" (our storeId) — see
	// https://github.com/amarbel-llc/madder/issues/22.
	workDir string
}

// NewStore returns a Store bound to ctx. The blob-store id defaults to
// "nebulous" so nebulous's blobs live in a dedicated store within madder's
// shared tree and don't collide with other madder consumers.
func NewStore(ctx context.Context) *Store {
	workDir := filepath.Join(cacheHome(), "nebulous", "madder-cwd")
	_ = os.MkdirAll(workDir, 0o755)
	return &Store{ctx: ctx, storeId: "nebulous", workDir: workDir}
}

// cacheHome returns the nebulous cache root under XDG convention.
func cacheHome() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return x
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cache")
	}
	return os.TempDir()
}

// Init ensures the nebulous blob store is present inside madder's tree. It
// probes with `madder has` against the store; if that fails to find the
// store (as opposed to a missing blob) it runs `madder init` with flags
// matching madder's bats contract. Safe to call more than once — a
// subsequent init on an existing store is reported as an error and
// swallowed.
func (s *Store) Init() error {
	// `madder init -encryption none <storeId>` is the non-interactive init shape
	// used by madder's own test suite; without -encryption, init fails resolving
	// a default hash type.
	cmd := s.command(
		"init",
		"-encryption", "none",
		s.storeId,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	// Re-init of an existing store is reported as an error (madder
	// surfaces the underlying os.ErrExist as "file exists"). Treat that
	// as success so Init is idempotent.
	msg := stderr.String()
	for _, pat := range []string{"file exists", "already exists", "already initialized"} {
		if strings.Contains(msg, pat) {
			return nil
		}
	}
	return fmt.Errorf("madder init %s: %w: %s", s.storeId, err, strings.TrimSpace(msg))
}

// Read streams the blob identified by id to dst. Returns (false, nil) if the
// blob is absent; (false, err) on other I/O failures. Madder's CLI does not
// distinguish "missing" from "errored" via exit code, so any nonzero exit is
// reported as a cache miss.
func (s *Store) Read(id string, dst io.Writer) (bool, error) {
	// First positional arg switches the active store; subsequent args are
	// markl-ids.
	cmd := s.command("cat", s.storeId, id)
	cmd.Stdout = dst
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("madder cat %s: %w: %s", id, err, strings.TrimSpace(stderr.String()))
	}
	return true, nil
}

// Write consumes src and returns the markl-id madder assigned to the blob.
func (s *Store) Write(src io.Reader) (string, error) {
	// -format=json is explicit so this works regardless of whether stdout
	// is a pipe or a tty. The store-id positional switches to the nebulous
	// store; subsequent args are paths, with "-" indicating stdin.
	cmd := s.command("write", "-format=json", s.storeId, "-")
	cmd.Stdin = src
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("madder write: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	id, err := parseWriteOutput(&stdout)
	if err != nil {
		return "", fmt.Errorf("madder write: %w (stdout=%q)", err, stdout.String())
	}
	return id, nil
}

// Has reports whether the store holds a blob for id. Exit code 0 = found;
// any other exit = not-found (per madder's CLI contract).
func (s *Store) Has(id string) (bool, error) {
	cmd := s.command("has", id)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("madder has %s: %w: %s", id, err, strings.TrimSpace(stderr.String()))
	}
	return true, nil
}

func (s *Store) command(args ...string) *exec.Cmd {
	cmd := exec.CommandContext(s.ctx, Bin, args...)
	cmd.Dir = s.workDir
	// MADDER_CEILING_DIRECTORIES bounds the .madder config walk so
	// ancestor directories (e.g. $HOME/.madder from an unrelated madder
	// consumer) can't inject config into our invocations.
	cmd.Env = append(os.Environ(), "MADDER_CEILING_DIRECTORIES="+s.workDir)
	return cmd
}
