package wordindex

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func buildSample(t *testing.T, path string) {
	t.Helper()
	b := NewBuilder()
	b.Add("1:aaa", []string{"alpha", "beta", "alpha"})
	b.Add("1:bbb", []string{"beta", "gamma"})
	b.Add("2:ccc", []string{"alpha", "gamma", "delta"})
	if err := b.WriteFile(path, Meta{Tokenizer: "t1", CorpusDigest: "d"}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx", "words.bbolt")
	buildSample(t, path)

	ix, err := Open(path, "t1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()

	m := ix.Meta()
	if m.Stories != 3 || m.Words != 4 || m.CorpusDigest != "d" || m.Format != FormatVersion {
		t.Errorf("meta = %+v", m)
	}

	for word, want := range map[string][]string{
		"alpha":   {"1:aaa", "2:ccc"}, // duplicate within one story counted once
		"beta":    {"1:aaa", "1:bbb"},
		"gamma":   {"1:bbb", "2:ccc"},
		"delta":   {"2:ccc"},
		"missing": nil,
	} {
		got, err := ix.Lookup(word)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", word, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Lookup(%q) = %v, want %v", word, got, want)
		}
	}
}

// The finished file must be readable by other service users: os.CreateTemp
// makes 0600 and bolt.Open's mode does not widen an existing file.
func TestWrittenFileIsWorldReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "words.bbolt")
	buildSample(t, path)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != indexFileMode {
		t.Errorf("index file mode = %o, want %o", got, indexFileMode)
	}
}

// One absurdly long token (a base64 blob in an article) must not fail the
// whole build: bbolt rejects keys over MaxKeySize ("key too large").
func TestOversizedWordIsSkippedNotFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "words.bbolt")
	b := NewBuilder()
	b.Add("1:aaa", []string{"alpha", strings.Repeat("x", 40000)})
	b.Add("1:bbb", []string{"alpha"})
	if err := b.WriteFile(path, Meta{Tokenizer: "t1"}); err != nil {
		t.Fatalf("WriteFile with an oversized word: %v", err)
	}
	if b.Skipped() != 1 {
		t.Errorf("Skipped = %d, want 1", b.Skipped())
	}
	ix, err := Open(path, "t1")
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if got, _ := ix.Lookup("alpha"); len(got) != 2 {
		t.Errorf("alpha = %v, want both stories", got)
	}
}

func TestOpenRejectsOtherTokenizer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "words.bbolt")
	buildSample(t, path)
	if _, err := Open(path, "t2"); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("Open with other tokenizer: err = %v, want ErrIncompatible", err)
	}
}

func TestOpenRejectsGarbageAndMissing(t *testing.T) {
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage.bbolt")
	if err := os.WriteFile(garbage, []byte("definitely not a bbolt file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(garbage, "t1"); err == nil {
		t.Error("Open(garbage) succeeded")
	}
	if _, err := Open(filepath.Join(dir, "missing.bbolt"), "t1"); err == nil {
		t.Error("Open(missing) succeeded")
	}
}

// A reader holding the old file keeps a consistent view across a swap.
func TestRenameSwapKeepsOpenReaderConsistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "words.bbolt")
	buildSample(t, path)
	old, err := Open(path, "t1")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()

	b := NewBuilder()
	b.Add("9:zzz", []string{"omega"})
	if err := b.WriteFile(path, Meta{Tokenizer: "t1"}); err != nil {
		t.Fatalf("second WriteFile: %v", err)
	}

	if got, _ := old.Lookup("alpha"); len(got) != 2 {
		t.Errorf("old reader lost its data after swap: %v", got)
	}
	fresh, err := Open(path, "t1")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if got, _ := fresh.Lookup("omega"); len(got) != 1 {
		t.Errorf("new reader missing new data: %v", got)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestPostingsRoundTrip(t *testing.T) {
	in := []uint32{0, 1, 5, 6, 100000, 4000000000}
	got, err := decodePostings(encodePostings(in))
	if err != nil || !reflect.DeepEqual(got, in) {
		t.Fatalf("round trip = %v, %v; want %v", got, err, in)
	}
	if _, err := decodePostings([]byte{0x80}); err == nil {
		t.Error("truncated varint decoded without error")
	}
}
