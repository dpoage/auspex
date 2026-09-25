package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dpoage/auspex/internal/evidence"
	"github.com/dpoage/auspex/internal/ledger"
)

// TestPairFlagsGroupsMutantsAndTests pins observe's --mutant/--test pairing:
// each --mutant opens a new pair, --test values belong to the most recent
// --mutant, and a --test before any --mutant is an error.
func TestPairFlagsGroupsMutantsAndTests(t *testing.T) {
	var pf pairFlags
	pf.addMutant("m1")
	if err := pf.addTest("t1"); err != nil {
		t.Fatal(err)
	}
	if err := pf.addTest("t2"); err != nil {
		t.Fatal(err)
	}
	pf.addMutant("m2")
	if err := pf.addTest("t3"); err != nil {
		t.Fatal(err)
	}
	if len(pf.mutants) != 2 || pf.mutants[0] != "m1" || pf.mutants[1] != "m2" {
		t.Fatalf("mutants = %v", pf.mutants)
	}
	if len(pf.tests) != 2 || len(pf.tests[0]) != 2 || pf.tests[0][0] != "t1" || pf.tests[0][1] != "t2" {
		t.Fatalf("pair 0 tests = %v", pf.tests[0])
	}
	if len(pf.tests[1]) != 1 || pf.tests[1][0] != "t3" {
		t.Fatalf("pair 1 tests = %v", pf.tests[1])
	}

	var empty pairFlags
	if err := empty.addTest("early"); err == nil {
		t.Fatal("--test before any --mutant: want an error, got nil")
	}
}

// TestObserveRequiresBaseArguments is a usage-error table: missing
// --slice/--base/--head, no pair at all, and a --mutant with no --test are
// all exit 2 with observe's own usage on stderr. None of these touch a
// repository, so no fixture is needed.
func TestObserveRequiresBaseArguments(t *testing.T) {
	for name, args := range map[string][]string{
		"missing slice":       {"--base", "HEAD", "--head", "HEAD", "--mutant", "m", "--test", "t"},
		"missing base":        {"--slice", "b", "--head", "HEAD", "--mutant", "m", "--test", "t"},
		"missing head":        {"--slice", "b", "--base", "HEAD", "--mutant", "m", "--test", "t"},
		"no pairs":            {"--slice", "b", "--base", "HEAD", "--head", "HEAD"},
		"mutant with no test": {"--slice", "b", "--base", "HEAD", "--head", "HEAD", "--mutant", "m"},
		"test before mutant":  {"--slice", "b", "--base", "HEAD", "--head", "HEAD", "--test", "t"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runObserve(args, &stdout, &stderr)
			if code != exitUsage {
				t.Fatalf("exit = %d, want %d; stderr=%s", code, exitUsage, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty on a usage error", stdout.String())
			}
		})
	}
}

// observeFixture builds a git+bd environment, mirrors the legs fixture's
// base/head commits, and writes .agents/auspex.toml whose suite and module
// commands fail exactly when the mutant marker and the strict head test
// are both present.
func observeFixture(t *testing.T) (led *ledger.Ledger, dir, base, mutantPath string) {
	t.Helper()
	led, dir = gitBDEnv(t)

	write := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitrun := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}

	write("src/pkg/lib.go", "package pkg\n\n// IMPL: correct\n")
	write("src/pkg/lib_test.go", "package pkg\n\n// CHECK: none\n")
	gitrun("add", "-A")
	gitrun("-c", "user.email=t@e.st", "-c", "user.name=t", "commit", "-m", "base")
	baseOut, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	base = strings.TrimSpace(string(baseOut))

	write("src/pkg/lib_test.go", "package pkg\n\n// CHECK: strict\n")
	write("src/pkg/new_test.go", "package pkg\n\n// NEW TEST FILE\n")
	gitrun("add", "-A")
	gitrun("-c", "user.email=t@e.st", "-c", "user.name=t", "commit", "-m", "head")

	const script = `n=1; [ -f src/pkg/new_test.go ] && n=2; if grep -q 'IMPL: buggy' src/pkg/lib.go && grep -q 'CHECK: strict' src/pkg/lib_test.go; then echo "$n ok"; exit 1; else echo "$n ok"; exit 0; fi`
	write(".agents/auspex.toml", "count_regex = '(\\d+) ok'\n\n"+
		"[suite]\n"+
		"argv = [\"sh\", \"-c\", \""+escapeTOML(script)+"\"]\n"+
		"timeout = \"30s\"\n\n"+
		"[[module]]\n"+
		"prefix = \"src/pkg/\"\n"+
		"argv = [\"sh\", \"-c\", \""+escapeTOML(script)+"\"]\n"+
		"timeout = \"30s\"\n")

	mutantPath = filepath.Join(t.TempDir(), "mutant.patch")
	mutant := "--- a/src/pkg/lib.go\n+++ b/src/pkg/lib.go\n@@ -1,3 +1,3 @@\n package pkg\n \n-// IMPL: correct\n+// IMPL: buggy\n"
	if err := os.WriteFile(mutantPath, []byte(mutant), 0o644); err != nil {
		t.Fatal(err)
	}
	return led, dir, base, mutantPath
}

func escapeTOML(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

func appointHeavy(t *testing.T, bead, round string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := runAppoint([]string{"--slice", bead, "--round", round, "--tier", "heavy"}, &out, &errOut); code != 0 {
		t.Fatalf("appoint: exit %d: %s", code, errOut.String())
	}
}

// gitStateOf snapshots worktree list, status, HEAD, and refs so a test can
// compare before and after observe (criterion G4).
func gitStateOf(t *testing.T, dir string) (worktrees, status, head, refs string) {
	t.Helper()
	run := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	return run("worktree", "list"), run("status", "--short"), run("rev-parse", "HEAD"), run("show-ref")
}

// TestObserveEndToEnd runs the full observe command against a real
// HostExec sandbox and a real bead: it prints one record ID, records a
// fault-free Leg with the expected outcomes, leaves the source repository
// untouched (G4), and exits 0.
func TestObserveEndToEnd(t *testing.T) {
	led, dir, base, mutantPath := observeFixture(t)
	bead := createBead(t, "observed slice")
	appointHeavy(t, bead, "r-observe")

	beforeWT, beforeStatus, beforeHead, beforeRefs := gitStateOf(t, dir)

	var stdout, stderr bytes.Buffer
	code := runObserve([]string{
		"--slice", bead, "--base", base, "--head", "HEAD",
		"--mutant", mutantPath,
		"--test", "src/pkg/lib_test.go", "--test", "src/pkg/new_test.go",
	}, &stdout, &stderr)
	if code != exitSuccess {
		t.Fatalf("observe exited %d: %s", code, stderr.String())
	}

	afterWT, afterStatus, afterHead, afterRefs := gitStateOf(t, dir)
	if beforeWT != afterWT || beforeStatus != afterStatus || beforeHead != afterHead || beforeRefs != afterRefs {
		t.Fatalf("source repository changed:\nworktree: %q -> %q\nstatus:   %q -> %q\nHEAD:     %q -> %q\nrefs:     %q -> %q",
			beforeWT, afterWT, beforeStatus, afterStatus, beforeHead, afterHead, beforeRefs, afterRefs)
	}

	recordID := ledger.RecordID(strings.TrimSpace(stdout.String()))
	rec, err := led.Get(recordID)
	if err != nil {
		t.Fatalf("Get(%s): %v", recordID, err)
	}
	leg, ok := rec.Body.(ledger.Leg)
	if !ok {
		t.Fatalf("record body is %T, want ledger.Leg", rec.Body)
	}
	if leg.A.Got != ledger.Green || leg.B.Got != ledger.Red || leg.C.Got != ledger.Green {
		t.Fatalf("leg outcomes = A:%v B:%v C:%v, want Green/Red/Green", leg.A.Got, leg.B.Got, leg.C.Got)
	}
	if faults := leg.Faults(); len(faults) != 0 {
		t.Fatalf("Faults() = %v, want none", faults)
	}
}

// TestObserveExitsNonzeroWhenLegBStaysGreen is G6 at the command layer: when a
// pair's test does not catch the mutant, observe still records the leg and
// prints its ID but exits nonzero.
func TestObserveExitsNonzeroWhenLegBStaysGreen(t *testing.T) {
	led, dir, base, mutantPath := observeFixture(t)
	bead := createBead(t, "unfaulty test slice")
	appointHeavy(t, bead, "r-observe-2")
	_ = dir

	var stdout, stderr bytes.Buffer
	// Head is base, whose lib_test.go never checks the IMPL marker, so
	// leg (b) stays green under the mutant.
	code := runObserve([]string{
		"--slice", bead, "--base", base, "--head", base,
		"--mutant", mutantPath,
		"--test", "src/pkg/lib_test.go",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("observe exited %d, want 1 (obnuntiatio); stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "obnuntiatio") {
		t.Fatalf("stderr = %q, want an obnuntiatio headline", stderr.String())
	}
	recordID := ledger.RecordID(strings.TrimSpace(stdout.String()))
	if recordID == "" {
		t.Fatal("observe printed no record ID even though the pair was fully processed")
	}
	rec, err := led.Get(recordID)
	if err != nil {
		t.Fatalf("Get(%s): %v", recordID, err)
	}
	leg := rec.Body.(ledger.Leg)
	if leg.B.Got != ledger.Green {
		t.Fatalf("leg (b) Got = %v, want Green (the base test at base never sees CHECK: strict)", leg.B.Got)
	}
	if faults := leg.Faults(); len(faults) == 0 {
		t.Fatal("Faults() empty even though leg (b) failed to catch the mutant")
	}
}

// TestObserveRefusesUnappointedSliceBeforeAnyLeg is F5 at the command layer:
// an unappointed slice is refused before any leg runs (exit 1, no record ID,
// no transcript). Mutant: check the appointment after the legs.
func TestObserveRefusesUnappointedSliceBeforeAnyLeg(t *testing.T) {
	_, _, base, mutantPath := observeFixture(t)
	bead := createBead(t, "never appointed")

	var stdout, stderr bytes.Buffer
	code := runObserve([]string{
		"--slice", bead, "--base", base, "--head", "HEAD",
		"--mutant", mutantPath,
		"--test", "src/pkg/lib_test.go", "--test", "src/pkg/new_test.go",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("observe exited %d, want 1 (obnuntiatio); stderr=%s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no record ID", stdout.String())
	}
	store, err := evidence.Open()
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadDir(store.Path("")) // the store's own directory
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("the evidence store holds %d entries: legs ran before the appointment was checked", len(stored))
	}
}
