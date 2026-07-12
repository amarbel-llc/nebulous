// Package capture drives the `cutting-garden capture` CLI (which drives
// chrest under the hood, RFC 0002/0003 — already fully wired inside
// cutting-garden itself) to preserve a story's full-page content
// independent of NewsBlur.
//
// This package deliberately shells out rather than linking
// cutting-garden's capture orchestration in-process: the subprocess form
// IS the canonical, versioned protocol boundary (RFC 0002 §Subprocess vs
// In-Process Plugins) — reimplementing that orchestration in Go here
// would duplicate real logic (store resolution, receipt writing, chrest
// bring-up/fallback) for no benefit, unlike internal/0/madder's
// shell-out, which was pure per-call subprocess overhead with an
// available in-process alternative.
package capture

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

const stderrTailBytes = 4096

// Bin is the path to the cutting-garden executable. Resolved via PATH by
// default; overridable at build time via
// -ldflags "-X .../capture.Bin=<path>" if a future Nix build wants to
// pin it the way nix-cache pins madder, mirroring
// internal/0/madder.Store's historical Bin var — not done yet since
// nebulous doesn't build/package cutting-garden itself.
var Bin = "cutting-garden"

// Client drives cutting-garden capture invocations.
type Client struct{}

// NewClient returns a Client. cutting-garden (and, transitively, chrest)
// must be resolvable on PATH.
func NewClient() *Client { return &Client{} }

// Capture runs `cutting-garden capture <storeId> web:<permalink>` with
// CUTTING_GARDEN_WEB_FORMAT=format, then reads the receipt id straight
// off stdout: a piped (non-TTY) `cutting-garden capture` — exactly
// exec.Command's case — defaults to tap-ndjson, and the "receipt
// store=..." phase's diagnostic already carries receipt_id
// machine-readably (cutting-garden's
// internal/capture_render_ndjson.reportReceipt). No second process or
// file read is needed to recover it.
func (c *Client) Capture(ctx context.Context, storeId, permalink, format string) (receiptID string, err error) {
	bin, err := exec.LookPath(Bin)
	if err != nil {
		return "", errors.Wrapf(err, "capture: %s not found on PATH; enter the devshell or install cutting-garden", Bin)
	}

	target := "web:" + permalink
	cmd := exec.CommandContext(ctx, bin, "capture", storeId, target)
	cmd.Env = append(os.Environ(), "CUTTING_GARDEN_WEB_FORMAT="+format)
	var stdout bytes.Buffer
	stderrTail := newTailBuffer(stderrTailBytes)
	cmd.Stdout = &stdout
	cmd.Stderr = stderrTail

	if runErr := cmd.Run(); runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", errors.Wrapf(ctxErr, "capture: %s canceled", target)
		}
		return "", errors.ErrorWithStackf(
			"capture: cutting-garden capture %s %s failed (%v)\nstderr-tail: %s",
			storeId, target, runErr, stderrTail.String(),
		)
	}

	receiptID, hasFailures, ok, parseErr := parseReceiptFromStdout(stdout.Bytes())
	if parseErr != nil {
		return "", errors.Wrapf(parseErr, "capture: cutting-garden capture %s %s: read stdout", storeId, target)
	}
	// Checked before !ok: a failures-only capture (no clean receipt
	// phase at all) is more useful reported as "wrote a failures
	// receipt" than the generic "no receipt phase" message below.
	if hasFailures {
		return "", errors.ErrorWithStackf(
			"capture: cutting-garden capture %s %s wrote a failures receipt (want a clean capture)",
			storeId, target,
		)
	}
	if !ok {
		return "", errors.ErrorWithStackf(
			"capture: cutting-garden capture %s %s exited 0 but no receipt phase was found on stdout",
			storeId, target,
		)
	}
	return receiptID, nil
}

// tapNDJSONTest is the subset of one tap-ndjson "test" record
// (amarbel-llc/tap doc/tap-ndjson.7.scd) this package needs — what
// cutting-garden's capture command emits per completed phase on a
// piped stdout.
type tapNDJSONTest struct {
	Type        string         `json:"type"`
	OK          bool           `json:"ok"`
	Description string         `json:"description"`
	Diagnostic  map[string]any `json:"diagnostic"`
}

// parseReceiptFromStdout scans cutting-garden's tap-ndjson stdout for
// the "receipt store=..." phase's receipt_id, and reports whether a
// "failures store=..." phase was also emitted (cutting-garden wrote a
// failure receipt alongside — or instead of — the success one, not a
// clean capture). ok=false when no successful receipt phase was found.
// err is non-nil only when the scan itself failed (e.g. a line past
// the buffer cap) — a real parsing failure to surface, distinct from
// "cutting-garden simply didn't emit one."
func parseReceiptFromStdout(stdout []byte) (receiptID string, hasFailures, ok bool, err error) {
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var rec tapNDJSONTest
		if unmarshalErr := json.Unmarshal(scanner.Bytes(), &rec); unmarshalErr != nil {
			continue
		}
		if rec.Type != "test" {
			continue
		}
		switch {
		case rec.OK && strings.HasPrefix(rec.Description, "receipt store="):
			if id, isStr := rec.Diagnostic["receipt_id"].(string); isStr && id != "" {
				receiptID = id
				ok = true
			}
		case strings.HasPrefix(rec.Description, "failures store="):
			hasFailures = true
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return "", false, false, scanErr
	}
	return receiptID, hasFailures, ok, nil
}

// tailBuffer keeps only the last cap bytes written through it — enough
// to surface a capture failure's stderr tail without buffering an
// unbounded stream.
type tailBuffer struct {
	buf []byte
	cap int
}

func newTailBuffer(cap int) *tailBuffer { return &tailBuffer{cap: cap} }

func (w *tailBuffer) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.cap {
		w.buf = w.buf[len(w.buf)-w.cap:]
	}
	return len(p), nil
}

func (w *tailBuffer) String() string { return strings.TrimSpace(string(w.buf)) }
