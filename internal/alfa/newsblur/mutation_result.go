package newsblur

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// MutationError is NewsBlur reporting a failed write inside an HTTP 200
// response. Its ajax endpoints answer 200 with {"code": -1, "message": ...}
// for failures such as "Could not save story, not found", so the status code
// alone cannot tell success from failure (tag-mutation review, finding 1).
type MutationError struct {
	// Failures holds every failed item of the response: one for a plain
	// {"code": -1} body, one per failed story for list bodies and for the
	// unstar endpoint's "messages" array.
	Failures []MutationFailure
}

// MutationFailure is one failed item in a NewsBlur mutation response.
type MutationFailure struct {
	Code      int
	Message   string
	StoryHash string
}

func (e *MutationError) Error() string {
	parts := make([]string, 0, len(e.Failures))
	for _, f := range e.Failures {
		part := fmt.Sprintf("code %d", f.Code)
		if f.Message != "" {
			part = f.Message
		}
		if f.StoryHash != "" {
			part += " (" + f.StoryHash + ")"
		}
		parts = append(parts, part)
	}
	return "newsblur rejected the request: " + strings.Join(parts, "; ")
}

// mutationFailure inspects a successful-HTTP response body and returns a
// *MutationError when NewsBlur reported a failure inside it, else nil.
//
// Recognised shapes (from NewsBlur's reader views):
//   - {"code": -1, "message": ...[, "story_hash": ...]}: a single failure;
//   - [ {...}, {...} ]: the multi-story star response, one item per story;
//   - {"code": 1, ..., "messages": [ {"code": -1, ...} ]}: the unstar
//     response, whose top-level code is always 1 and whose per-story
//     failures live only in "messages".
//
// Only a negative code is a failure. A body that is not JSON, or has no
// code at all, is not treated as a failure: endpoints with other response
// conventions keep their previous behaviour.
func mutationFailure(body []byte) error {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil
	}

	var failures []MutationFailure
	switch body[0] {
	case '{':
		failures = objectFailures(body)
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(body, &items); err != nil {
			return nil
		}
		for _, item := range items {
			failures = append(failures, objectFailures(item)...)
		}
	default:
		return nil
	}
	if len(failures) == 0 {
		return nil
	}
	return &MutationError{Failures: failures}
}

type mutationItem struct {
	Code      *int              `json:"code"`
	Message   string            `json:"message"`
	StoryHash string            `json:"story_hash"`
	Messages  []json.RawMessage `json:"messages"`
}

func objectFailures(raw []byte) []MutationFailure {
	var item mutationItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil // not the expected shape (e.g. a non-integer "code")
	}
	var failures []MutationFailure
	if item.Code != nil && *item.Code < 0 {
		failures = append(failures, MutationFailure{Code: *item.Code, Message: item.Message, StoryHash: item.StoryHash})
	}
	for _, m := range item.Messages {
		var inner mutationItem
		if err := json.Unmarshal(m, &inner); err != nil {
			continue
		}
		if inner.Code != nil && *inner.Code < 0 {
			failures = append(failures, MutationFailure{Code: *inner.Code, Message: inner.Message, StoryHash: inner.StoryHash})
		}
	}
	return failures
}
