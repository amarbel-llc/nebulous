package madder

import (
	"strings"
	"testing"
)

func TestParseWriteOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name: "single ok with markl-id and path",
			input: strings.Join([]string{
				"TAP version 14",
				"# Output: 1 - blake2b256-abc123 /tmp/blob",
				"ok 1 - blake2b256-abc123 /tmp/blob",
				"1..1",
				"",
			}, "\n"),
			want: "blake2b256-abc123",
		},
		{
			name: "single ok writing from stdin",
			input: strings.Join([]string{
				"TAP version 14",
				"# Output: 1 - blake2b256-def456 -",
				"ok 1 - blake2b256-def456 -",
				"1..1",
				"",
			}, "\n"),
			want: "blake2b256-def456",
		},
		{
			name: "not ok surfaces error",
			input: strings.Join([]string{
				"TAP version 14",
				"# Output: 1 - /tmp/bad",
				"not ok 1 - /tmp/bad",
				"  ---",
				"  severity: fail",
				"  ...",
				"1..1",
				"",
			}, "\n"),
			wantErr: "not ok",
		},
		{
			name:    "no test points",
			input:   "TAP version 14\n1..0\n",
			wantErr: "no test points",
		},
		{
			name: "multiple test points rejected",
			input: strings.Join([]string{
				"TAP version 14",
				"# Output: 1 - blake2b256-aaa /a",
				"ok 1 - blake2b256-aaa /a",
				"# Output: 2 - blake2b256-bbb /b",
				"ok 2 - blake2b256-bbb /b",
				"1..2",
				"",
			}, "\n"),
			wantErr: "expected 1 test point, got 2",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseWriteOutput(strings.NewReader(tc.input))
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (got=%q)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
