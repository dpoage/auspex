package corpus

import "testing"

// TestCase3LegKilledByTimeout is DESIGN.md seeded-fault case 3: "A leg
// killed by timeout" (observe must record Killed, not red). The module
// command sleeps past its configured timeout, so the sandbox kills it;
// observe must exit nonzero and name the kill, never mistake it for a
// genuine red.
func TestCase3LegKilledByTimeout(t *testing.T) {
	const suiteScript = `n=$(ls src/pkg/*_test.go | wc -l); echo "$n ok"; exit 0`

	build := func(t *testing.T, moduleScript, moduleTimeout string) (p *product, base, head, mutant string) {
		p = newProduct(t)
		p.write("src/pkg/lib.go", "package pkg\n\n// IMPL: correct\n")
		p.write("src/pkg/lib_test.go", "package pkg\n\n// baseline test\n")
		base = p.commit("base")
		p.write("src/pkg/new_test.go", "package pkg\n\n// new test\n")
		head = p.commit("head")
		p.writeConfig(`(\d+) ok`, suiteScript, "10s", "src/pkg/", moduleScript, moduleTimeout)
		mutant = p.mutantPatch("mutant.patch", "src/pkg/lib.go")
		return p, base, head, mutant
	}

	t.Run("fault", func(t *testing.T) {
		// The module command sleeps well past its 1s timeout.
		p, base, head, mutant := build(t, `sleep 5; echo "1 ok"; exit 1`, "1s")
		bead := p.bead("case3 fault slice")
		p.appoint(bead, "r-case3", "heavy", "")
		stdout, stderr, code := p.run(
			"observe", "--slice", bead, "--base", base, "--head", head,
			"--mutant", mutant, "--test", "src/pkg/new_test.go",
		)
		requireExit(t, 1, code, stdout, stderr)
		requireContains(t, stderr, "obnuntiatio")
		requireContains(t, stderr, "killed")
		requireContains(t, stderr, "not run red")
		if stdout == "" {
			t.Fatal("observe printed no record id even though the leg was fully processed")
		}
	})

	t.Run("control", func(t *testing.T) {
		// The module command finishes well inside its timeout and properly
		// discriminates the mutant.
		const strict = `if grep -q 'IMPL: buggy' src/pkg/lib.go; then echo "1 ok"; exit 1; else echo "1 ok"; exit 0; fi`
		p, base, head, mutant := build(t, strict, "10s")
		bead := p.bead("case3 control slice")
		p.appoint(bead, "r-case3c", "heavy", "")
		stdout, stderr, code := p.run(
			"observe", "--slice", bead, "--base", base, "--head", head,
			"--mutant", mutant, "--test", "src/pkg/new_test.go",
		)
		requireExit(t, 0, code, stdout, stderr)
	})
}
