package corpus

import (
	"fmt"
	"strings"
	"testing"
)

// TestCase7MissingTriageAfterTwoRejects is DESIGN.md seeded-fault case 7:
// "A missing triage after 2 consecutive REJECTs from one seat" (cp2 must
// name the REJECT). Seat B REJECTs twice in a row, then APPROVEs, with no
// triage ruling recorded in between; cp2 must fail closed and name the
// second REJECT. The hiding mutant (internal/check/check.go, cp2Triage;
// criterion C5) deletes the predicate.
func TestCase7MissingTriageAfterTwoRejects(t *testing.T) {
	t.Run("fault", func(t *testing.T) {
		p := newProduct(t)
		bead := p.bead("case7 fault slice")
		hash := p.head()
		p.appoint(bead, "r-case7", "light", hash)
		matrix := p.validMatrix()

		p.mustRun("heed", "--slice", bead, "--seat", "B", "--hash", hash, "--model", "m",
			p.transcript("reject1.json", false, "", nil))
		reject2Out := p.mustRun("heed", "--slice", bead, "--seat", "B", "--hash", hash, "--model", "m",
			p.transcript("reject2.json", false, "", nil))
		reject2ID := strings.TrimSpace(reject2Out)
		// No triage ruling here: the fault.
		p.mustRun("heed", "--slice", bead, "--seat", "B", "--hash", hash, "--model", "m",
			p.transcript("approve.json", true, matrix, nil))

		stdout, stderr, code := p.run("inaugurate", "cp2", "--round", "r-case7")
		requireExit(t, 1, code, stdout, stderr)
		requireContains(t, stderr, "obnuntiatio")
		requireContains(t, stderr, fmt.Sprintf("seat B's REJECT %s (2 consecutive) has no triage ruling before the seat's next verdict", reject2ID))
	})

	t.Run("control", func(t *testing.T) {
		p := newProduct(t)
		bead := p.bead("case7 control slice")
		hash := p.head()
		p.appoint(bead, "r-case7c", "light", hash)
		matrix := p.validMatrix()

		p.mustRun("heed", "--slice", bead, "--seat", "B", "--hash", hash, "--model", "m",
			p.transcript("reject1.json", false, "", nil))
		p.mustRun("heed", "--slice", bead, "--seat", "B", "--hash", hash, "--model", "m",
			p.transcript("reject2.json", false, "", nil))
		p.mustRun("decree", "--slice", bead, "--kind", "triage", "--item", "loop-cap", "--ruling", "reviewed", "--seat", "B")
		p.mustRun("heed", "--slice", bead, "--seat", "B", "--hash", hash, "--model", "m",
			p.transcript("approve.json", true, matrix, nil))

		stdout, stderr, code := p.run("inaugurate", "cp2", "--round", "r-case7c")
		requireExit(t, 0, code, stdout, stderr)
	})
}
