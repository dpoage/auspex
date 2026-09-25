package corpus

import "testing"

// TestCase2NewTestNotCollected is DESIGN.md seeded-fault case 2: "A new
// test that CI does not collect" (leg (c)'s count is unchanged). The
// module command properly discriminates the mutant (leg (b) runs red, as
// it should), but the suite command's count is fixed regardless of which
// test files exist, so leg (c) never executes more tests than leg (a).
// observe must still exit nonzero and name the fault. The hiding mutant
// (internal/ledger/ledger.go, Leg.Faults) is `<` instead of `<=`, so an
// equal count no longer faults.
func TestCase2NewTestNotCollected(t *testing.T) {
	const strictModule = `if grep -q 'IMPL: buggy' src/pkg/lib.go; then echo "1 ok"; exit 1; else echo "1 ok"; exit 0; fi`

	build := func(t *testing.T, suiteScript string) (p *product, base, head, mutant string) {
		p = newProduct(t)
		p.write("src/pkg/lib.go", "package pkg\n\n// IMPL: correct\n")
		p.write("src/pkg/lib_test.go", "package pkg\n\n// baseline test\n")
		base = p.commit("base")
		p.write("src/pkg/new_test.go", "package pkg\n\n// new test CI may or may not collect\n")
		head = p.commit("head")
		p.writeConfig(`(\d+) ok`, suiteScript, "10s", "src/pkg/", strictModule, "10s")
		mutant = p.mutantPatch("mutant.patch", "src/pkg/lib.go")
		return p, base, head, mutant
	}

	t.Run("fault", func(t *testing.T) {
		// The suite always reports a count of 1, whatever test files exist:
		// leg (c) never executes more than leg (a).
		p, base, head, mutant := build(t, `echo "1 ok"; exit 0`)
		bead := p.bead("case2 fault slice")
		p.appoint(bead, "r-case2", "heavy", "")
		stdout, stderr, code := p.run(
			"observe", "--slice", bead, "--base", base, "--head", head,
			"--mutant", mutant, "--test", "src/pkg/new_test.go",
		)
		requireExit(t, 1, code, stdout, stderr)
		requireContains(t, stderr, "obnuntiatio")
		requireContains(t, stderr, "leg (c) executed no more tests than leg (a)")
		if stdout == "" {
			t.Fatal("observe printed no record id even though the leg was fully processed")
		}
	})

	t.Run("control", func(t *testing.T) {
		// The suite actually counts test files, so leg (c) picks up the new one.
		p, base, head, mutant := build(t, `n=$(ls src/pkg/*_test.go | wc -l); echo "$n ok"; exit 0`)
		bead := p.bead("case2 control slice")
		p.appoint(bead, "r-case2c", "heavy", "")
		stdout, stderr, code := p.run(
			"observe", "--slice", bead, "--base", base, "--head", head,
			"--mutant", mutant, "--test", "src/pkg/new_test.go",
		)
		requireExit(t, 0, code, stdout, stderr)
	})
}
