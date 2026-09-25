package evidence

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestEvidencePutGetRoundTrip is criterion E1: for every byte string b,
// Get(Put(b)) == b and Put(b) returns the same Ref twice.
func TestEvidencePutGetRoundTrip(t *testing.T) {
	s := testStore(t)
	inputs := [][]byte{
		{},
		{0},
		[]byte("a transcript\nwith lines\n"),
		bytes.Repeat([]byte{0xff, 0x00, 0x7f}, 10000),
	}
	for i, b := range inputs {
		ref, err := s.Put(b)
		if err != nil {
			t.Fatalf("Put(%d): %v", i, err)
		}
		if ref == "" {
			t.Fatalf("Put(%d) returned an empty ref", i)
		}
		again, err := s.Put(b)
		if err != nil {
			t.Fatalf("Put(%d) again: %v", i, err)
		}
		if again != ref {
			t.Fatalf("Put(%d) returned %s then %s; want the same ref", i, ref, again)
		}
		got, err := s.Get(ref)
		if err != nil {
			t.Fatalf("Get(%d): %v", i, err)
		}
		if !bytes.Equal(got, b) {
			t.Fatalf("Get(Put(input %d)) != input: got %d bytes, want %d", i, len(got), len(b))
		}
	}
}

// TestEvidenceGetDetectsCorruption is criterion E2: after any single byte of
// a stored file changes, Get returns *CorruptError{Ref} and no bytes.
func TestEvidenceGetDetectsCorruption(t *testing.T) {
	s := testStore(t)
	body := bytes.Repeat([]byte("transcript body — "), 500)
	for _, at := range []int{0, len(body) / 2, len(body) - 1} {
		ref, err := s.Put(body)
		if err != nil {
			t.Fatal(err)
		}
		path := s.Path(ref)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw[at] ^= 0x01
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ref)
		if got != nil {
			t.Fatalf("byte %d: Get returned bytes on a corrupt transcript", at)
		}
		var corrupt *CorruptError
		if !errors.As(err, &corrupt) {
			t.Fatalf("byte %d: Get err = %v, want *CorruptError", at, err)
		}
		if corrupt.Ref != ref {
			t.Fatalf("byte %d: CorruptError.Ref = %s, want %s", at, corrupt.Ref, ref)
		}
		// Repair the file so the next position starts from a clean store.
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ref); err != nil {
			t.Fatalf("byte %d: repaired transcript still fails: %v", at, err)
		}
	}
}

// TestEvidenceGetMissingRefErrors is criterion E3: Get of a ref that was
// never stored returns an error.
func TestEvidenceGetMissingRefErrors(t *testing.T) {
	s := testStore(t)
	never := Ref("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	b, err := s.Get(never)
	if err == nil {
		t.Fatalf("Get of a never-stored ref returned %v bytes and no error", b)
	}
	if b != nil {
		t.Fatal("Get of a never-stored ref returned bytes")
	}
}
