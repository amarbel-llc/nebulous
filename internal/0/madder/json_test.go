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
			name:  "single record from stdin",
			input: `{"id":"blake2b256-abc123","size":12,"source":"-","store":"nebulous"}` + "\n",
			want:  "blake2b256-abc123",
		},
		{
			name:  "single record without trailing newline",
			input: `{"id":"blake2b256-def456","size":42,"source":"/tmp/blob","store":"nebulous"}`,
			want:  "blake2b256-def456",
		},
		{
			name:    "empty stdout",
			input:   "",
			wantErr: "no records emitted",
		},
		{
			name:    "whitespace-only stdout",
			input:   "   \n  \n",
			wantErr: "no records emitted",
		},
		{
			name:    "missing id field",
			input:   `{"size":12,"source":"-"}` + "\n",
			wantErr: "missing id",
		},
		{
			name:    "malformed json",
			input:   `not a json object` + "\n",
			wantErr: "decode madder record",
		},
		{
			name: "multiple records rejected",
			input: `{"id":"blake2b256-aaa","size":1,"source":"-","store":"nebulous"}` + "\n" +
				`{"id":"blake2b256-bbb","size":1,"source":"-","store":"nebulous"}` + "\n",
			wantErr: "expected 1 record, got 2",
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
