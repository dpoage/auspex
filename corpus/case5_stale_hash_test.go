package corpus

import "testing"

// Two hand-mined commit objects (both parentless commits of the empty tree,
// differing only in their commit message) that collide on their first 7 hex
// characters but are different objects. cp2SeatApprove compares full hashes,
// so binding collidingStaleHash as an APPROVE for collidingHeadHash is always
// wrong; this pair exists only so the C11 mutant (compare 7-char prefixes) is
// exercised rather than passing by luck.
const (
	collidingHeadContent = "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\n" +
		"author t <t@e.st> 1700000000 +0000\n" +
		"committer t <t@e.st> 1700000000 +0000\n" +
		"\n" +
		"head fixture commit 140491\n"
	collidingHeadHash = "1b87bca090dfb97848b063e5f2a8c3209124f4b2"

	collidingStaleContent = "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\n" +
		"author t <t@e.st> 1700000000 +0000\n" +
		"committer t <t@e.st> 1700000000 +0000\n" +
		"\n" +
		"stale fixture commit 1242\n"
	collidingStaleHash = "1b87bca93851497165ec2f765e117e9b6a5be3bf"
)

// injectCollidingCommits writes both crafted commit objects into p's git
// object database and fails the test unless git hashes them as mined.
func injectCollidingCommits(t *testing.T, p *product) {
	t.Helper()
	if got := p.gitStdin(collidingHeadContent, "hash-object", "-w", "-t", "commit", "--stdin"); got != collidingHeadHash {
		t.Fatalf("git hashed the head fixture commit as %s, want %s", got, collidingHeadHash)
	}
	if got := p.gitStdin(collidingStaleContent, "hash-object", "-w", "-t", "commit", "--stdin"); got != collidingStaleHash {
		t.Fatalf("git hashed the stale fixture commit as %s, want %s", got, collidingStaleHash)
	}
}

// TestCase5ApproveBoundToStaleHash is DESIGN.md seeded-fault case 5: seat A's
// APPROVE is bound to a hash that shares the merge hash's first 7 characters
// but is not equal to it; seat B is properly bound. cp2 must fail closed and
// name seat A.
//
// Mutant that hides this (internal/check/check.go, cp2SeatApprove; C11):
// compare only the first 7 characters of the hash instead of the full 40-hex
// value.
func TestCase5ApproveBoundToStaleHash(t *testing.T) {
	t.Run("fault", func(t *testing.T) {
		p := newProduct(t)
		injectCollidingCommits(t, p)
		bead := p.bead("case5 fault slice")
		p.appoint(bead, "r-case5", "heavy", collidingHeadHash)
		matrix := p.validMatrix()

		staleApprove := p.transcript("seat-a-stale.json", true, matrix, nil)
		p.mustRun("heed", "--slice", bead, "--seat", "A", "--hash", collidingStaleHash, "--model", "m", staleApprove)
		properApprove := p.transcript("seat-b-ok.json", true, matrix, nil)
		p.mustRun("heed", "--slice", bead, "--seat", "B", "--hash", collidingHeadHash, "--model", "m", properApprove)

		stdout, stderr, code := p.run("inaugurate", "cp2", "--round", "r-case5")
		requireExit(t, 1, code, stdout, stderr)
		requireContains(t, stderr, "obnuntiatio")
		requireContains(t, stderr, "seat A has no APPROVE bound to "+collidingHeadHash)
	})

	t.Run("control", func(t *testing.T) {
		p := newProduct(t)
		injectCollidingCommits(t, p)
		bead := p.bead("case5 control slice")
		p.appoint(bead, "r-case5c", "heavy", collidingHeadHash)
		matrix := p.validMatrix()

		aApprove := p.transcript("seat-a-ok.json", true, matrix, nil)
		p.mustRun("heed", "--slice", bead, "--seat", "A", "--hash", collidingHeadHash, "--model", "m", aApprove)
		bApprove := p.transcript("seat-b-ok.json", true, matrix, nil)
		p.mustRun("heed", "--slice", bead, "--seat", "B", "--hash", collidingHeadHash, "--model", "m", bApprove)

		stdout, stderr, code := p.run("inaugurate", "cp2", "--round", "r-case5c")
		requireExit(t, 0, code, stdout, stderr)
	})
}
