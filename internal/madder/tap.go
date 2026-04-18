package madder

import (
	"errors"
	"fmt"
	"io"
	"strings"

	tap "github.com/amarbel-llc/bob/packages/tap-dancer/go"
)

// parseWriteOutput consumes `madder write` TAP stdout and returns the
// markl-id from its sole test point. Madder emits one `ok N - <markl-id>
// <path>` line per written blob; nebulous writes one blob per invocation, so
// any count other than one test point is a protocol violation.
func parseWriteOutput(r io.Reader) (string, error) {
	reader := tap.NewReader(r)
	var id string
	seen := 0
	for {
		ev, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("tap-dancer read: %w", err)
		}
		if ev.Type != tap.EventTestPoint || ev.TestPoint == nil {
			continue
		}
		if !ev.TestPoint.OK {
			return "", fmt.Errorf("write not ok: %q", ev.TestPoint.Description)
		}
		fields := strings.Fields(ev.TestPoint.Description)
		if len(fields) == 0 {
			return "", fmt.Errorf("write description was empty")
		}
		id = fields[0]
		seen++
	}
	switch seen {
	case 0:
		return "", errors.New("no test points emitted")
	case 1:
		return id, nil
	default:
		return "", fmt.Errorf("expected 1 test point, got %d", seen)
	}
}
