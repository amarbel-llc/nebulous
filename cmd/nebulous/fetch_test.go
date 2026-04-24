package main

import (
	"reflect"
	"testing"
)

// TestNewlyIndexedHashes_filtersByCachePredicate verifies the helper
// used by the fetch → archive handoff: given the slice of hashes
// that were missing before a fetch and a predicate reporting
// current cache state, return the subset that landed during the
// run, preserving input order.
func TestNewlyIndexedHashes_filtersByCachePredicate(t *testing.T) {
	cases := []struct {
		name        string
		wasMissing  []string
		cachedNow   map[string]bool
		wantIndexed []string
	}{
		{
			name:        "all landed",
			wasMissing:  []string{"a", "b", "c"},
			cachedNow:   map[string]bool{"a": true, "b": true, "c": true},
			wantIndexed: []string{"a", "b", "c"},
		},
		{
			name:        "partial — order preserved",
			wasMissing:  []string{"a", "b", "c", "d"},
			cachedNow:   map[string]bool{"a": true, "c": true},
			wantIndexed: []string{"a", "c"},
		},
		{
			name:        "none landed",
			wasMissing:  []string{"a", "b"},
			cachedNow:   map[string]bool{},
			wantIndexed: []string{},
		},
		{
			name:        "empty input",
			wasMissing:  nil,
			cachedNow:   map[string]bool{"a": true},
			wantIndexed: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newlyIndexedHashes(tc.wasMissing, func(h string) bool {
				return tc.cachedNow[h]
			})
			if !reflect.DeepEqual(got, tc.wantIndexed) {
				// DeepEqual considers nil != []string{}, but both are
				// semantically "nothing landed" — normalize before
				// comparing so the nil-input case isn't a false fail.
				if len(got) == 0 && len(tc.wantIndexed) == 0 {
					return
				}
				t.Errorf("got %v, want %v", got, tc.wantIndexed)
			}
		})
	}
}
