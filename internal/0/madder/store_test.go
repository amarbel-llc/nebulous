package madder

import (
	"bytes"
	"context"
	"testing"
)

// withTempXDG points the standard XDG_DATA_HOME env var at a fresh temp
// dir for the duration of the test, so Store never touches the real
// ~/.local/share/madder tree. Also runs from the temp dir itself so
// madder's cwd walk-up can't pick up an unrelated ancestor .madder/
// override.
func withTempXDG(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Chdir(dir)
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	withTempXDG(t)
	s := NewStore(context.Background())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return s
}

func TestStoreInitIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	if err := s.Init(); err != nil {
		t.Fatalf("second Init: %v", err)
	}
}

func TestStoreWriteReadRoundTrip(t *testing.T) {
	s := newTestStore(t)

	want := []byte("hello, madder")
	id, err := s.Write(bytes.NewReader(want))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if id == "" {
		t.Fatal("Write returned empty id")
	}

	var buf bytes.Buffer
	ok, err := s.Read(id, &buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !ok {
		t.Fatal("Read reported miss for a just-written blob")
	}
	if buf.String() != string(want) {
		t.Errorf("Read = %q, want %q", buf.String(), want)
	}
}

func TestStoreHas(t *testing.T) {
	s := newTestStore(t)

	id, err := s.Write(bytes.NewReader([]byte("present")))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	ok, err := s.Has(id)
	if err != nil {
		t.Fatalf("Has(present): %v", err)
	}
	if !ok {
		t.Error("Has(present) = false, want true")
	}
}

func TestStoreReadMissing(t *testing.T) {
	s := newTestStore(t)

	// Mint a well-formed, correctly-checksummed markl id for content
	// this store has never seen: write it into a SECOND, independent
	// store (its own isolated temp XDG root) instead. Content-addressed
	// ids are deterministic on their bytes, but storage is per-store —
	// so this id is valid and absent from s, exercising the "miss" path
	// rather than a malformed-id parse error.
	other := newTestStore(t)
	absentId, err := other.Write(bytes.NewReader([]byte("never written to s")))
	if err != nil {
		t.Fatalf("Write (other store): %v", err)
	}

	var buf bytes.Buffer
	ok, err := s.Read(absentId, &buf)
	if err != nil {
		t.Fatalf("Read(absent id): %v", err)
	}
	if ok {
		t.Error("Read(absent id) ok=true, want false (miss)")
	}

	has, err := s.Has(absentId)
	if err != nil {
		t.Fatalf("Has(absent id): %v", err)
	}
	if has {
		t.Error("Has(absent id) = true, want false")
	}
}
