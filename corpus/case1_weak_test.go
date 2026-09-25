package corpus

import "testing"

// TestCase1WeakTestPassesUnderMutant is DESIGN.md seeded-fault case 1: a
// test that passes under its mutant (leg (b) stays green). The named
// --test path grows the executed-test count from base to head (so every
// other Leg.Faults condition holds) but its module command never actually
// checks the mutant marker, so leg (b) comes back Green instead of Red.
// observe must still exit nonzero and name the fault.
//
// Mutant that hides this (internal/ledger/ledger.go, Leg.Faults): drop the
// `case l.B.Got != Red` arm of the outcome switch, so a leg (b) that
// merely fails to run red (as opposed to being killed) is never reported.
func TestCase1WeakTestPassesUnderMutant(t *testing.T) {
	build := func(t *testing.T, moduleScript string) (p *product, base, head, mutant string) {
		p = newProduct(t)
		p.write("src/pkg/lib.go", "package pkg\n\n// IMPL: correct\n")
		p.write("src/pkg/lib_test.go", "package pkg\n\n// baseline test, checks nothing\n")
		base = p.commit("base")
		p.write("src/pkg/new_test.go", "package pkg\n\n// new test, does not check the marker either\n")
		head = p.commit("head")
		p.writeConfig(
			`(\d+) ok`,
			`n=$(ls src/pkg/*_test.go | wc -l); echo "$n ok"; exit 0`, "10s",
			"src/pkg/", moduleScript, "10s",
		)
		mutant = p.mutantPatch("mutant.patch", "src/pkg/lib.go")
		return p, base, head, mutant
	}

	t.Run("fault", func(t *testing.T) {
		// The module command never inspects lib.go: leg (b) is Green under
		// the mutant no matter what.
		p, base, head, mutant := build(t, `echo "1 ok"; exit 0`)
		bead := p.bead("case1 fault slice")
		p.appoint(bead, "r-case1", "heavy", "")
		stdout, stderr, code := p.run(
			"observe", "--slice", bead, "--base", base, "--head", head,
			"--mutant", mutant, "--test", "src/pkg/new_test.go",
		)
		requireExit(t, 1, code, stdout, stderr)
		requireContains(t, stderr, "obnuntiatio")
		requireContains(t, stderr, "leg (b) did not run red")
		if stdout == "" {
			t.Fatal("observe printed no record id even though the leg was fully processed")
		}
	})

	t.Run("control", func(t *testing.T) {
		// The module command greps for the mutant marker: leg (b) runs red,
		// which is the expected result, so observe holds.
		const strict = `if grep -q 'IMPL: buggy' src/pkg/lib.go; then echo "1 ok"; exit 1; else echo "1 ok"; exit 0; fi`
		p, base, head, mutant := build(t, strict)
		bead := p.bead("case1 control slice")
		p.appoint(bead, "r-case1c", "heavy", "")
		stdout, stderr, code := p.run(
			"observe", "--slice", bead, "--base", base, "--head", head,
			"--mutant", mutant, "--test", "src/pkg/new_test.go",
		)
		requireExit(t, 0, code, stdout, stderr)
	})
}
