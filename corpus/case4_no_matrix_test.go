package corpus

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCase4ApproveWithNoMatrixFile defends DESIGN.md seeded-fault case 4:
// "An APPROVE transcript with no matrix file" (heed must refuse it). heed
// resolves a matrix on one of two branches, and each gets a fault and a
// control that differ only in whether the matrix file was written:
//   - local: a local://<name> matrix resolved against --local's directory;
//   - absolute: an absolute matrix path, with no --local.
//
// A fault: heed exits nonzero, names the missing matrix, prints nothing,
// records nothing. A control: heed records the verdict. Mutant to expose:
// skip the os.Stat existence check, accept a missing local:// matrix, or
// accept a missing matrix when no --local was given.
func TestCase4ApproveWithNoMatrixFile(t *testing.T) {
	const matrixName = "oracle-case4-A.md"

	// heed runs an APPROVE transcript whose matrix is written only when
	// withMatrix is set. local selects the local:// branch; otherwise the
	// matrix is an absolute path and --local is not given.
	heed := func(t *testing.T, local, withMatrix bool, round string) (matrixPath, stdout, stderr string, code int) {
		p := newProduct(t)
		bead := p.bead(round + " slice")
		p.appoint(bead, round, "heavy", "")
		dir := t.TempDir()
		matrixPath = filepath.Join(dir, matrixName)
		if withMatrix {
			if err := os.WriteFile(matrixPath, []byte("# matrix\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		args := []string{"heed", "--slice", bead, "--seat", "A", "--hash", "HEAD", "--model", "m"}
		matrix := matrixPath
		if local {
			matrix = "local://" + matrixName
			args = append(args, "--local", dir)
		}
		args = append(args, p.transcript("approve.json", true, matrix, nil))
		stdout, stderr, code = p.run(args...)
		return matrixPath, stdout, stderr, code
	}

	fault := func(t *testing.T, local bool, round string) {
		matrixPath, stdout, stderr, code := heed(t, local, false, round)
		requireExit(t, 1, code, stdout, stderr)
		requireContains(t, stderr, "obnuntiatio")
		requireContains(t, stderr, "matrix "+matrixPath+" does not exist")
		if stdout != "" {
			t.Fatalf("heed printed %q, want nothing recorded", stdout)
		}
	}

	control := func(t *testing.T, local bool, round string) {
		_, stdout, stderr, code := heed(t, local, true, round)
		requireExit(t, 0, code, stdout, stderr)
		if stdout == "" {
			t.Fatal("heed printed no record id for a valid APPROVE")
		}
	}

	t.Run("local fault", func(t *testing.T) { fault(t, true, "r-case4l") })
	t.Run("local control", func(t *testing.T) { control(t, true, "r-case4lc") })
	t.Run("absolute fault", func(t *testing.T) { fault(t, false, "r-case4a") })
	t.Run("absolute control", func(t *testing.T) { control(t, false, "r-case4ac") })
}
