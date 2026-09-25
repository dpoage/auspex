package corpus

import (
	"fmt"
	"strings"
	"testing"
)

// TestCase6UnruledScopeItem is DESIGN.md seeded-fault case 6: an unruled
// SCOPE item (cp2 must name `<record>#S<k>`). A seat B APPROVE carries
// one SCOPE item that no decree ruling ever answers; cp2 must fail closed
// and name the full item id.
//
// Mutant that hides this (internal/check/check.go, cp2ScopeRuled;
// criterion C4): delete the predicate.
func TestCase6UnruledScopeItem(t *testing.T) {
	t.Run("fault", func(t *testing.T) {
		p := newProduct(t)
		bead := p.bead("case6 fault slice")
		hash := p.head()
		p.appoint(bead, "r-case6", "light", hash)
		matrix := p.validMatrix()

		transcript := p.transcript("approve-scope.json", true, matrix, []txScope{
			{ID: "finding-1", Text: "a real finding worth ruling on"},
		})
		out := p.mustRun("heed", "--slice", bead, "--seat", "B", "--hash", hash, "--model", "m", transcript)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 2 {
			t.Fatalf("heed printed %d lines, want 2 (record id, scope item id): %q", len(lines), out)
		}
		itemID := lines[1]

		stdout, stderr, code := p.run("inaugurate", "cp2", "--round", "r-case6")
		requireExit(t, 1, code, stdout, stderr)
		requireContains(t, stderr, "obnuntiatio")
		requireContains(t, stderr, fmt.Sprintf("SCOPE item %s has no ruling", itemID))
	})

	t.Run("control", func(t *testing.T) {
		p := newProduct(t)
		bead := p.bead("case6 control slice")
		hash := p.head()
		p.appoint(bead, "r-case6c", "light", hash)
		matrix := p.validMatrix()

		transcript := p.transcript("approve-scope.json", true, matrix, []txScope{
			{ID: "finding-1", Text: "a real finding worth ruling on"},
		})
		out := p.mustRun("heed", "--slice", bead, "--seat", "B", "--hash", hash, "--model", "m", transcript)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		itemID := lines[1]

		p.mustRun("decree", "--slice", bead, "--kind", "scope", "--item", itemID, "--ruling", "addressed in a follow-up")

		stdout, stderr, code := p.run("inaugurate", "cp2", "--round", "r-case6c")
		requireExit(t, 0, code, stdout, stderr)
	})
}
