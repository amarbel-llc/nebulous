package madder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// parseWriteOutput consumes `madder write -format=json` NDJSON stdout
// and returns the markl-id from the single emitted record. Nebulous
// writes one blob per invocation, so any count other than one record
// is a protocol violation.
//
// Per madder's JSON output (landed in madder v0.0.2), each record is
// one JSON object of the form:
//
//	{"id": "<markl-id>", "size": N, "source": "-", "store": "<store-id>"}
//
// ID is the only field this package cares about; size/source/store
// are tolerated but unused.
func parseWriteOutput(r io.Reader) (string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read madder stdout: %w", err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", errors.New("no records emitted")
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var id string
	seen := 0
	for {
		var rec struct {
			ID string `json:"id"`
		}
		if err := dec.Decode(&rec); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", fmt.Errorf("decode madder record: %w", err)
		}
		if rec.ID == "" {
			return "", errors.New("madder record missing id")
		}
		id = rec.ID
		seen++
	}
	switch seen {
	case 0:
		return "", errors.New("no records emitted")
	case 1:
		return id, nil
	default:
		return "", fmt.Errorf("expected 1 record, got %d", seen)
	}
}
