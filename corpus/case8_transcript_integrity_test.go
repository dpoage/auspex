package corpus

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// evidenceRefOf returns the content-addressed evidence ref auspex would
// store path under: the lowercase hex SHA-256 of its bytes.
func evidenceRefOf(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}

// corruptEvidence flips the first byte of the stored transcript ref under
// p's evidence store.
func corruptEvidence(t *testing.T, p *product, ref string) {
	t.Helper()
	path := filepath.Join(p.stateHome, "auspex", "evidence", ref)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading stored evidence %s: %v", path, err)
	}
	if len(b) == 0 {
		t.Fatalf("stored evidence %s is empty", path)
	}
	b[0] ^= 0xFF
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCase8CorruptedTranscript is DESIGN.md seeded-fault case 8: "A stored
// transcript with one changed byte" (cp2 must name the ref). A verdict's
// transcript is stored, then one byte of the stored (not the source) copy
// is flipped; cp2 must fail closed and name the ref.
func TestCase8CorruptedTranscript(t *testing.T) {
	t.Run("fault", func(t *testing.T) {
		p := newProduct(t)
		bead := p.bead("case8 fault slice")
		hash := p.head()
		p.appoint(bead, "r-case8", "light", hash)
		matrix := p.validMatrix()

		transcript := p.transcript("approve.json", true, matrix, nil)
		ref := evidenceRefOf(t, transcript)
		p.mustRun("heed", "--slice", bead, "--seat", "B", "--hash", hash, "--model", "m", transcript)
		corruptEvidence(t, p, ref)

		stdout, stderr, code := p.run("inaugurate", "cp2", "--round", "r-case8")
		requireExit(t, 1, code, stdout, stderr)
		requireContains(t, stderr, "obnuntiatio")
		requireContains(t, stderr, "failed integrity")
		requireContains(t, stderr, ref)
	})

	t.Run("control", func(t *testing.T) {
		p := newProduct(t)
		bead := p.bead("case8 control slice")
		hash := p.head()
		p.appoint(bead, "r-case8c", "light", hash)
		matrix := p.validMatrix()

		transcript := p.transcript("approve.json", true, matrix, nil)
		p.mustRun("heed", "--slice", bead, "--seat", "B", "--hash", hash, "--model", "m", transcript)

		stdout, stderr, code := p.run("inaugurate", "cp2", "--round", "r-case8c")
		requireExit(t, 0, code, stdout, stderr)
	})
}
