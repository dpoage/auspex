package corpus

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCase10RestoreThroughSymlink is DESIGN.md seeded-fault case 10: a test
// restore through a symlink. Leg (a) restores src/t/x_test.go from base; in
// the fault, src/t is a symlink whose target is an absolute path back into
// the fixture (victim/, which holds the head test). observe must refuse the
// leg (no record) and leave the fixture's git status and victim/ unchanged.
// The control replaces the symlink with the directory it points at; observe
// holds and records. The leaf subtest makes the test path itself the
// symlink: restore must replace the link with the base file, so observe
// holds with victim/ untouched.
//
// Mutant that hides this (internal/legs/legs.go, restoreFile): restore with
// os.MkdirAll + os.WriteFile, which follow symlinks, instead of the Lstat
// walk that refuses one; or keep the walk but write the leaf with
// os.WriteFile, which follows a leaf symlink.
func TestCase10RestoreThroughSymlink(t *testing.T) {
	const (
		testPath = "src/t/x_test.go"
		baseTest = "package pkg\n\n// case one\n"
		headTest = "package pkg\n\n// case one\n// case two\n"
	)

	// build commits a fixture: base holds lib.go, the base test at testPath,
	// and victim/keep.txt; head adds victim/x_test.go and reaches testPath
	// via shape — "none" is a real directory, "dir" makes src/t a symlink to
	// victim/, and "leaf" makes testPath a symlink to victim/x_test.go. The
	// suite counts the test file's cases; the module command runs red
	// exactly under the mutant.
	build := func(t *testing.T, shape string) (p *product, base, head, mutant string) {
		p = newProduct(t)
		p.write("src/pkg/lib.go", "package pkg\n\n// IMPL: correct\n")
		p.write(testPath, baseTest)
		p.write("victim/keep.txt", "keep\n")
		base = p.commit("base")
		p.write("victim/x_test.go", headTest)
		switch shape {
		case "none":
			p.write(testPath, headTest)
		case "dir":
			p.symlink("src/t", filepath.Join(p.dir, "victim"))
		case "leaf":
			p.symlink(testPath, filepath.Join(p.dir, "victim", "x_test.go"))
		default:
			t.Fatalf("unknown shape %q", shape)
		}
		head = p.commit("head")
		p.writeConfig(`(\d+) ok`,
			`n=$(grep -c case `+testPath+`); echo "$n ok"; exit 0`, "10s",
			"src/t/", `if grep -q 'IMPL: buggy' src/pkg/lib.go; then echo "1 ok"; exit 1; else echo "1 ok"; exit 0; fi`, "10s",
		)
		mutant = p.mutantPatch("mutant.patch", "src/pkg/lib.go")
		return p, base, head, mutant
	}

	// snapshot is the fixture's git status and every file under victim/
	// with its content.
	snapshot := func(t *testing.T, p *product) (status string, victim map[string]string) {
		status = p.git("status", "--short")
		entries, err := os.ReadDir(filepath.Join(p.dir, "victim"))
		if err != nil {
			t.Fatal(err)
		}
		victim = map[string]string{}
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(p.dir, "victim", e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			victim[e.Name()] = string(b)
		}
		return status, victim
	}

	requireUntouched := func(t *testing.T, p *product, beforeStatus string, beforeVictim map[string]string) {
		t.Helper()
		afterStatus, afterVictim := snapshot(t, p)
		if beforeStatus != afterStatus {
			t.Fatalf("fixture repository status changed:\nbefore: %q\nafter:  %q", beforeStatus, afterStatus)
		}
		if len(beforeVictim) != len(afterVictim) {
			t.Fatalf("victim/ changed: before %q, after %q (a leg must never write into it)", beforeVictim, afterVictim)
		}
		for name, content := range beforeVictim {
			if afterVictim[name] != content {
				t.Fatalf("victim/%s changed: before %q, after %q (a leg must never write into it)", name, content, afterVictim[name])
			}
		}
	}

	observe := func(p *product, bead, base, head, mutant string) (stdout, stderr string, code int) {
		return p.run(
			"observe", "--slice", bead, "--base", base, "--head", head,
			"--mutant", mutant, "--test", testPath,
		)
	}

	t.Run("fault", func(t *testing.T) {
		p, base, head, mutant := build(t, "dir")
		bead := p.bead("case10 fault slice")
		p.appoint(bead, "r-case10", "heavy", "")
		beforeStatus, beforeVictim := snapshot(t, p)

		stdout, stderr, code := observe(p, bead, base, head, mutant)
		requireExit(t, 1, code, stdout, stderr)
		requireContains(t, stderr, "obnuntiatio")
		requireContains(t, stderr, "src/t is a symlink")
		if stdout != "" {
			t.Fatalf("observe printed %q, want no record: a refused leg records nothing", stdout)
		}
		requireUntouched(t, p, beforeStatus, beforeVictim)
	})

	t.Run("control", func(t *testing.T) {
		p, base, head, mutant := build(t, "none")
		bead := p.bead("case10 control slice")
		p.appoint(bead, "r-case10c", "heavy", "")

		stdout, stderr, code := observe(p, bead, base, head, mutant)
		requireExit(t, 0, code, stdout, stderr)
		if stdout == "" {
			t.Fatal("observe printed no record id for a fixture with no symlink")
		}
	})

	t.Run("leaf", func(t *testing.T) {
		p, base, head, mutant := build(t, "leaf")
		bead := p.bead("case10 leaf slice")
		p.appoint(bead, "r-case10l", "heavy", "")
		beforeStatus, beforeVictim := snapshot(t, p)

		stdout, stderr, code := observe(p, bead, base, head, mutant)
		requireExit(t, 0, code, stdout, stderr)
		if stdout == "" {
			t.Fatal("observe printed no record id although restore replaces a leaf symlink")
		}
		requireUntouched(t, p, beforeStatus, beforeVictim)
	})
}
