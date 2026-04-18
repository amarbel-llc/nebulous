package blobstore

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// ExternalCommandStore shells out to configured commands to read and write
// blobs. The contract matches amarbel-llc/maneater#8:
//
//	read-cmd <digest>  → stdout is blob content; exit 0 on success,
//	                     non-zero indicates not-found or error.
//	write-cmd          → stdin receives the blob content; stdout's last
//	                     non-empty line contains the digest as its first
//	                     whitespace-delimited token.
//
// This contract is wire-compatible with `madder cat` / `madder write -`,
// `aws s3 cp` wrappers, or any custom script following the same shape.
type ExternalCommandStore struct {
	readCmd  []string
	writeCmd []string
}

func NewExternalCommandStore(readCmd, writeCmd []string) (*ExternalCommandStore, error) {
	if len(readCmd) == 0 {
		return nil, errors.New("blobstore: read command must be non-empty")
	}
	if len(writeCmd) == 0 {
		return nil, errors.New("blobstore: write command must be non-empty")
	}
	return &ExternalCommandStore{
		readCmd:  append([]string(nil), readCmd...),
		writeCmd: append([]string(nil), writeCmd...),
	}, nil
}

func (s *ExternalCommandStore) Read(ctx context.Context, digest string, dst io.Writer) (bool, error) {
	args := append([]string(nil), s.readCmd[1:]...)
	args = append(args, digest)
	cmd := exec.CommandContext(ctx, s.readCmd[0], args...)
	cmd.Stdout = dst
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("blobstore: read %s: %w: %s", digest, err, strings.TrimSpace(stderr.String()))
	}
	return true, nil
}

func (s *ExternalCommandStore) Write(ctx context.Context, src io.Reader) (string, error) {
	cmd := exec.CommandContext(ctx, s.writeCmd[0], s.writeCmd[1:]...)
	cmd.Stdin = src
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("blobstore: write: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	digest, err := parseDigest(stdout.Bytes())
	if err != nil {
		return "", fmt.Errorf("blobstore: parse digest from write output: %w", err)
	}
	return digest, nil
}

func (s *ExternalCommandStore) Has(ctx context.Context, digest string) (bool, error) {
	return s.Read(ctx, digest, io.Discard)
}

// parseDigest extracts the digest from a write command's stdout. It returns
// the first whitespace-delimited token of the last non-empty line, matching
// the convention in maneater#8 (accommodates both plain output like
// "abc123\n" and TAP output like "ok - sha256:abc123 -\n").
func parseDigest(stdout []byte) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	var last string
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			last = line
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if last == "" {
		return "", errors.New("empty output")
	}
	fields := strings.Fields(last)
	return fields[0], nil
}
