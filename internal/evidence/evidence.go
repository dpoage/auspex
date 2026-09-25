// Package evidence is where auspex keeps transcripts. Callers do not know
// where the files live, how they are named, or how their integrity is
// checked: every byte string is stored once, named by its SHA-256 hash, and
// re-verified on every read.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Ref is the lowercase hex SHA-256 of a stored byte string.
type Ref string

// CorruptError reports stored bytes that no longer hash to their Ref.
type CorruptError struct {
	Ref Ref
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("evidence: stored transcript %s no longer matches its hash", e.Ref)
}

// Store is the one content-addressed transcript store. It serves every
// repository: a content address cannot collide.
type Store struct {
	root string
}

// Open returns the store at $XDG_STATE_HOME/auspex/evidence/ (default
// ~/.local/state) and creates it when missing.
func Open() (*Store, error) {
	root, err := stateHome()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "auspex", "evidence")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("evidence: open: %w", err)
	}
	return &Store{root: dir}, nil
}

// Put stores b and returns its Ref. Storing the same bytes again is a no-op
// that returns the same Ref. The write is atomic: a reader never sees a
// partial file.
func (s *Store) Put(b []byte) (Ref, error) {
	sum := sha256.Sum256(b)
	ref := Ref(hex.EncodeToString(sum[:]))
	dst := s.Path(ref)
	if _, err := os.Stat(dst); err == nil {
		return ref, nil
	}
	tmp, err := os.CreateTemp(s.root, ".put-*")
	if err != nil {
		return "", fmt.Errorf("evidence: put: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return "", fmt.Errorf("evidence: put: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("evidence: put: %w", err)
	}
	if err := os.Rename(name, dst); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("evidence: put: %w", err)
	}
	return ref, nil
}

// Get returns the bytes stored under r after re-verifying their hash. It is
// a *CorruptError when the file's bytes no longer hash to r, and an error
// when no transcript with r was ever stored.
func (s *Store) Get(r Ref) ([]byte, error) {
	b, err := os.ReadFile(s.Path(r))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("evidence: no transcript stored under %s", r)
		}
		return nil, fmt.Errorf("evidence: get: %w", err)
	}
	sum := sha256.Sum256(b)
	if got := Ref(hex.EncodeToString(sum[:])); got != r {
		return nil, &CorruptError{Ref: r}
	}
	return b, nil
}

// Path is the file path that backs r, for commands that print locations.
func (s *Store) Path(r Ref) string { return filepath.Join(s.root, string(r)) }

// stateHome resolves $XDG_STATE_HOME, falling back to ~/.local/state. Per
// the XDG spec a non-absolute value is ignored.
func stateHome() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" && filepath.IsAbs(x) {
		return x, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("evidence: no state home: %w", err)
	}
	return filepath.Join(home, ".local", "state"), nil
}
