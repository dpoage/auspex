package corpus

import "testing"

// TestCase9TruncatedStreamIsUncounted defends DESIGN.md seeded-fault case 9:
// "A truncated leg stream" (observe must not count it). The module command
// exceeds the sandbox's per-stream cap on one stream, so that stream is
// truncated. The output still contains matching count lines and correctly
// discriminates the mutant (nonzero exit), but observe must record the leg
// uncounted rather than trust a partial count, and exit nonzero. Each
// stream floods in its own subtest because the guard checks each stream
// flag independently.
//
// Mutant to expose (internal/legs/legs.go, buildLegRun): drop the
// `!res.StdoutTruncated && !res.StderrTruncated` guard, or either half of it.
func TestCase9TruncatedStreamIsUncounted(t *testing.T) {
	const suiteScript = `n=$(ls src/pkg/*_test.go | wc -l); echo "$n ok"; exit 0`

	build := func(t *testing.T, moduleScript string) (p *product, base, head, mutant string) {
		p = newProduct(t)
		p.write("src/pkg/lib.go", "package pkg\n\n// IMPL: correct\n")
		p.write("src/pkg/lib_test.go", "package pkg\n\n// baseline test\n")
		base = p.commit("base")
		p.write("src/pkg/new_test.go", "package pkg\n\n// new test\n")
		head = p.commit("head")
		p.writeConfig(`(\d+) ok`, suiteScript, "10s", "src/pkg/", moduleScript, "10s")
		mutant = p.mutantPatch("mutant.patch", "src/pkg/lib.go")
		return p, base, head, mutant
	}

	// fault floods one stream past the sandbox's 1 MiB cap with lines that
	// themselves match the count regex, prints an ordinary count line on
	// the other stream, then correctly discriminates the mutant. The leg
	// still must not be counted. toFlood/toOther are shell redirections:
	// "" for stdout, " >&2" for stderr.
	fault := func(t *testing.T, toFlood, toOther, round string) {
		script := `yes "1 ok" | head -c 1200000` + toFlood + `; echo "1 ok"` + toOther +
			`; if grep -q 'IMPL: buggy' src/pkg/lib.go; then exit 1; else exit 0; fi`
		p, base, head, mutant := build(t, script)
		bead := p.bead(round + " slice")
		p.appoint(bead, round, "heavy", "")
		stdout, stderr, code := p.run(
			"observe", "--slice", bead, "--base", base, "--head", head,
			"--mutant", mutant, "--test", "src/pkg/new_test.go",
		)
		requireExit(t, 1, code, stdout, stderr)
		requireContains(t, stderr, "obnuntiatio")
		requireContains(t, stderr, "leg (b) recorded no executed-test count")
		if stdout == "" {
			t.Fatal("observe printed no record id even though the leg was fully processed")
		}
	}

	t.Run("fault stdout", func(t *testing.T) { fault(t, "", " >&2", "r-case9o") })
	t.Run("fault stderr", func(t *testing.T) { fault(t, " >&2", "", "r-case9e") })

	t.Run("control", func(t *testing.T) {
		// Module command output is ordinary, well under the cap.
		const strict = `if grep -q 'IMPL: buggy' src/pkg/lib.go; then echo "1 ok"; exit 1; else echo "1 ok"; exit 0; fi`
		p, base, head, mutant := build(t, strict)
		bead := p.bead("case9 control slice")
		p.appoint(bead, "r-case9c", "heavy", "")
		stdout, stderr, code := p.run(
			"observe", "--slice", bead, "--base", base, "--head", head,
			"--mutant", mutant, "--test", "src/pkg/new_test.go",
		)
		requireExit(t, 0, code, stdout, stderr)
	})
}
