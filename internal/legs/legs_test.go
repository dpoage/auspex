package legs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dpoage/auspex/internal/evidence"
	"github.com/dpoage/auspex/internal/ledger"
	"github.com/dpoage/auspex/internal/repo"
	"github.com/dpoage/llmkit/sandbox"
)

// requireGit fails the test when git is missing; the gate never skips.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required: %v", err)
	}
}

const (
	libPath     = "src/pkg/lib.go"
	testPath    = "src/pkg/lib_test.go"
	newTestPath = "src/pkg/new_test.go"

	baseLib  = "package pkg\n\n// IMPL: correct\n"
	headLib  = "package pkg\n\n// IMPL: correct\n"
	mutLib   = "package pkg\n\n// IMPL: buggy\n"
	baseTest = "package pkg\n\n// CHECK: none\n"
	headTest = "package pkg\n\n// CHECK: strict\n"
	newTest  = "package pkg\n\n// NEW TEST FILE\n"
)

// libMutant flips head's lib.go from "correct" to "buggy" — the criterion's
// regression. Plain unified diff; applying it does not need a .git in the
// target directory because git apply resolves paths against its working
// directory either way.
var libMutant = []byte(strings.Join([]string{
	"--- a/" + libPath,
	"+++ b/" + libPath,
	"@@ -1,3 +1,3 @@",
	" package pkg",
	" ",
	"-// IMPL: correct",
	"+// IMPL: buggy",
	"",
}, "\n"))

// legsFixture is a small git repository with a base and head commit: a
// source file, a base test that misses the regression, a head test that
// catches it, and a file that exists only at head (exercising G1's
// delete-when-absent-at-base rule).
type legsFixture struct {
	repo       *repo.Repo
	base, head repo.Hash
}

func buildLegsFixture(t *testing.T) legsFixture {
	t.Helper()
	g := newGitRepo(t)
	g.write(libPath, baseLib, 0o644)
	g.write(testPath, baseTest, 0o644)
	base := g.commit("base")

	g.write(libPath, headLib, 0o644)
	g.write(testPath, headTest, 0o644)
	g.write(newTestPath, newTest, 0o644)
	head := g.commit("head")
	return legsFixture{repo: g.open(), base: base, head: head}
}

// gitRepo is a scratch repository that a test commits trees into.
type gitRepo struct {
	t    *testing.T
	root string
}

func newGitRepo(t *testing.T) *gitRepo {
	t.Helper()
	requireGit(t)
	g := &gitRepo{t: t, root: t.TempDir()}
	g.git("init", "-b", "main")
	return g
}

func (g *gitRepo) git(args ...string) string {
	g.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", g.root}, args...)...).CombinedOutput()
	if err != nil {
		g.t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// write replaces whatever is at name with a regular file holding body.
func (g *gitRepo) write(name, body string, perm os.FileMode) {
	g.t.Helper()
	p := g.clear(name)
	if err := os.WriteFile(p, []byte(body), perm); err != nil {
		g.t.Fatal(err)
	}
	if err := os.Chmod(p, perm); err != nil { // past the umask
		g.t.Fatal(err)
	}
}

// symlink replaces whatever is at name with a symlink to target.
func (g *gitRepo) symlink(name, target string) {
	g.t.Helper()
	if err := os.Symlink(target, g.clear(name)); err != nil {
		g.t.Fatal(err)
	}
}

// clear removes whatever is at name, creates the directories above it,
// and returns its path.
func (g *gitRepo) clear(name string) string {
	g.t.Helper()
	p := filepath.Join(g.root, filepath.FromSlash(name))
	if err := os.RemoveAll(p); err != nil {
		g.t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		g.t.Fatal(err)
	}
	return p
}

func (g *gitRepo) commit(msg string) repo.Hash {
	g.t.Helper()
	g.git("add", "-A")
	g.git("-c", "user.email=t@e.st", "-c", "user.name=t", "commit", "-m", msg)
	return repo.Hash(strings.TrimSpace(g.git("rev-parse", "HEAD")))
}

func (g *gitRepo) open() *repo.Repo {
	g.t.Helper()
	r, err := repo.Open(g.root)
	if err != nil {
		g.t.Fatal(err)
	}
	return r
}

// newTestStore returns an evidence.Store rooted under a fresh temp dir.
func newTestStore(t *testing.T) *evidence.Store {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s, err := evidence.Open()
	if err != nil {
		t.Fatalf("evidence.Open: %v", err)
	}
	return s
}

func simpleConfig() Config {
	return Config{
		CountRegex: regexp.MustCompile(`(\d+) ok`),
		Suite:      Command{Argv: []string{"suite"}, Timeout: time.Minute},
		Modules:    []ModuleEntry{{Prefix: "src/pkg/", Command: Command{Argv: []string{"module"}, Timeout: time.Minute}}},
	}
}

func wantFile(t *testing.T, dir, rel, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("reading %s in %s: %v", rel, dir, err)
	}
	if string(got) != want {
		t.Fatalf("%s in %s = %q, want %q", rel, dir, got, want)
	}
}

func wantAbsent(t *testing.T, dir, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
		t.Fatalf("%s in %s: want absent, stat error = %v", rel, dir, err)
	}
}

// TestRunPreparesTreesPerLeg is criterion G1: leg (a) is head + mutant +
// every test path restored from base (deleted when absent at base); leg
// (b) is head + mutant with no restoration; leg (c) is plain head.
func TestRunPreparesTreesPerLeg(t *testing.T) {
	fx := buildLegsFixture(t)
	store := newTestStore(t)
	cfg := simpleConfig()
	pair := Pair{MutantPatch: libMutant, Tests: []string{testPath, newTestPath}}

	calls := 0
	mock := &sandbox.Mock{ResponseFunc: func(n int, spec sandbox.Spec) (sandbox.Result, error) {
		calls++
		switch n {
		case 0: // leg (c): head, no mutant.
			wantFile(t, spec.RepoDir, libPath, headLib)
			wantFile(t, spec.RepoDir, testPath, headTest)
			wantFile(t, spec.RepoDir, newTestPath, newTest)
		case 1: // leg (a): head + mutant + tests restored from base.
			wantFile(t, spec.RepoDir, libPath, mutLib)
			wantFile(t, spec.RepoDir, testPath, baseTest)
			wantAbsent(t, spec.RepoDir, newTestPath) // absent at base: deleted
		case 2: // leg (b): head + mutant, no restoration.
			wantFile(t, spec.RepoDir, libPath, mutLib)
			wantFile(t, spec.RepoDir, testPath, headTest)
			wantFile(t, spec.RepoDir, newTestPath, newTest)
		default:
			t.Fatalf("unexpected Exec call %d", n)
		}
		return sandbox.Result{ExitCode: 0}, nil
	}}

	if _, err := Run(context.Background(), mock, "mock", cfg, fx.repo, store, fx.base, fx.head, []Pair{pair}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 3 {
		t.Fatalf("Exec called %d times, want 3 (one per leg)", calls)
	}
}

// TestRunClassifiesInfraKillBeforeExitCode is criterion G2: a leg whose
// Result.InfraKilled() is true records Got=Killed, never Red or Green, no
// matter what its exit code says.
func TestRunClassifiesInfraKillBeforeExitCode(t *testing.T) {
	fx := buildLegsFixture(t)
	store := newTestStore(t)
	cfg := simpleConfig()
	pair := Pair{MutantPatch: libMutant, Tests: []string{testPath}}

	mock := &sandbox.Mock{ResponseFunc: func(n int, spec sandbox.Spec) (sandbox.Result, error) {
		if n == 2 { // leg (b): exit code says "passed", but it was killed.
			return sandbox.Result{ExitCode: 0, TimedOut: true}, nil
		}
		return sandbox.Result{ExitCode: 0}, nil
	}}

	legsOut, err := Run(context.Background(), mock, "mock", cfg, fx.repo, store, fx.base, fx.head, []Pair{pair})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := legsOut[0].B.Got; got != ledger.Killed {
		t.Fatalf("leg (b) Got = %v, want Killed", got)
	}
	if faults := legsOut[0].Faults(); len(faults) == 0 {
		t.Fatal("a killed leg (b) must produce a fault")
	}
}

// TestRunSumsEveryRegexMatch is criterion G3: the executed count is the sum
// of every numeric capture group over every match, and zero matches means
// Counted is false.
func TestRunSumsEveryRegexMatch(t *testing.T) {
	fx := buildLegsFixture(t)
	store := newTestStore(t)
	cfg := simpleConfig()
	pair := Pair{MutantPatch: libMutant, Tests: []string{testPath}}

	mock := &sandbox.Mock{DefaultResponse: sandbox.MockResponse{
		Result: sandbox.Result{ExitCode: 0, Stdout: "3 ok\n4 ok\n", Stderr: "5 ok\n"},
	}}
	legsOut, err := Run(context.Background(), mock, "mock", cfg, fx.repo, store, fx.base, fx.head, []Pair{pair})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if legsOut[0].A.Executed != 12 || !legsOut[0].A.Counted {
		t.Fatalf("leg (a) Executed=%d Counted=%v, want 12 true (3+4+5 across every match)", legsOut[0].A.Executed, legsOut[0].A.Counted)
	}

	mock2 := &sandbox.Mock{DefaultResponse: sandbox.MockResponse{Result: sandbox.Result{ExitCode: 0, Stdout: "no matches here"}}}
	legsOut2, err := Run(context.Background(), mock2, "mock", cfg, fx.repo, store, fx.base, fx.head, []Pair{pair})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if legsOut2[0].A.Counted {
		t.Fatalf("leg (a) Counted=true with zero regex matches, want false")
	}
}

// TestRunAbortsWithNoRecordsOnUsageErrors is criterion G5's Run-level half
// and F5's pre-flight: a mutant that does not apply to head, or a test path
// with no matching module entry, aborts the whole Run with an error and no
// Legs at all, before any leg runs. Mutant: check the mutant after leg (c).
func TestRunAbortsWithNoRecordsOnUsageErrors(t *testing.T) {
	fx := buildLegsFixture(t)
	store := newTestStore(t)
	cfg := simpleConfig()
	mock := &sandbox.Mock{DefaultResponse: sandbox.MockResponse{Result: sandbox.Result{ExitCode: 0}}}

	t.Run("unmatched module", func(t *testing.T) {
		pair := Pair{MutantPatch: libMutant, Tests: []string{"nope/x_test.go"}}
		legsOut, err := Run(context.Background(), mock, "mock", cfg, fx.repo, store, fx.base, fx.head, []Pair{pair})
		if err == nil || legsOut != nil {
			t.Fatalf("Run with an unmatched test path: want (nil, error), got (%v, %v)", legsOut, err)
		}
		if n := mock.CallCount(); n != 0 {
			t.Fatalf("Run executed %d legs before refusing an unmatched test path, want 0", n)
		}
	})

	t.Run("mutant does not apply", func(t *testing.T) {
		badPatch := []byte(strings.Join([]string{
			"--- a/" + libPath,
			"+++ b/" + libPath,
			"@@ -1,3 +1,3 @@",
			" does",
			"-not",
			"+match",
			" anything",
			"",
		}, "\n"))
		pair := Pair{MutantPatch: badPatch, Tests: []string{testPath}}
		legsOut, err := Run(context.Background(), mock, "mock", cfg, fx.repo, store, fx.base, fx.head, []Pair{pair})
		if err == nil || legsOut != nil {
			t.Fatalf("Run with a non-applying mutant: want (nil, error), got (%v, %v)", legsOut, err)
		}
		if n := mock.CallCount(); n != 0 {
			t.Fatalf("Run executed %d legs before refusing a non-applying mutant, want 0", n)
		}
	})
}

// TestRunSharesLegCAcrossPairs is criterion G6: leg (c) runs exactly once
// no matter how many pairs there are, and every pair's record carries the
// same leg (c) run.
func TestRunSharesLegCAcrossPairs(t *testing.T) {
	fx := buildLegsFixture(t)
	store := newTestStore(t)
	cfg := simpleConfig()
	pairs := []Pair{
		{MutantPatch: libMutant, Tests: []string{testPath}},
		{MutantPatch: libMutant, Tests: []string{testPath}},
	}

	calls := 0
	mock := &sandbox.Mock{ResponseFunc: func(n int, spec sandbox.Spec) (sandbox.Result, error) {
		calls++
		return sandbox.Result{ExitCode: 0}, nil
	}}
	legsOut, err := Run(context.Background(), mock, "mock", cfg, fx.repo, store, fx.base, fx.head, pairs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 5 {
		t.Fatalf("Exec called %d times for 2 pairs, want 5 (one shared leg (c) plus 2x(a+b))", calls)
	}
	if !reflect.DeepEqual(legsOut[0].C, legsOut[1].C) {
		t.Fatalf("leg (c) differs between pairs' records:\n%+v\n%+v", legsOut[0].C, legsOut[1].C)
	}
}

// TestRunPassesConfiguredTimeoutsToSpec is criterion G7: each leg's
// configured command timeout reaches sandbox.Spec.Timeout — legs (a) and
// (c) get the suite's timeout, leg (b) gets its module's.
func TestRunPassesConfiguredTimeoutsToSpec(t *testing.T) {
	fx := buildLegsFixture(t)
	store := newTestStore(t)
	suiteTimeout := 37 * time.Second
	moduleTimeout := 41 * time.Second
	cfg := Config{
		CountRegex: regexp.MustCompile(`(\d+) ok`),
		Suite:      Command{Argv: []string{"suite"}, Timeout: suiteTimeout},
		Modules:    []ModuleEntry{{Prefix: "src/pkg/", Command: Command{Argv: []string{"module"}, Timeout: moduleTimeout}}},
	}
	pair := Pair{MutantPatch: libMutant, Tests: []string{testPath}}

	var gotTimeouts []time.Duration
	mock := &sandbox.Mock{ResponseFunc: func(n int, spec sandbox.Spec) (sandbox.Result, error) {
		gotTimeouts = append(gotTimeouts, spec.Timeout)
		return sandbox.Result{ExitCode: 0}, nil
	}}
	if _, err := Run(context.Background(), mock, "mock", cfg, fx.repo, store, fx.base, fx.head, []Pair{pair}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []time.Duration{suiteTimeout, suiteTimeout, moduleTimeout} // (c), (a), (b)
	if !reflect.DeepEqual(gotTimeouts, want) {
		t.Fatalf("Spec.Timeout per Exec call = %v, want %v", gotTimeouts, want)
	}
}

// hostExecConfig builds a Config whose suite and module commands are real
// shell scripts: both fail (exit 1) exactly when the mutant marker AND the
// strict (head) test are both present in the tree, and pass otherwise. This
// models a real suite/test relationship without needing a compiled
// toolchain in the exported tree.
func hostExecConfig(t *testing.T) Config {
	t.Helper()
	// n counts the always-present test file plus 1 when the criterion's
	// new test is present. Leg (c) keeps it; leg (a) deletes it because it
	// was absent at base. So leg (c)'s executed count is higher -- the
	// proof the leg (c) check depends on.
	script := `n=1; [ -f ` + newTestPath + ` ] && n=2
if grep -q 'IMPL: buggy' ` + libPath + ` && grep -q 'CHECK: strict' ` + testPath + `; then echo "$n ok"; exit 1; else echo "$n ok"; exit 0; fi`
	return Config{
		CountRegex: regexp.MustCompile(`(\d+) ok`),
		Suite:      Command{Argv: []string{"sh", "-c", script}, Timeout: 30 * time.Second},
		Modules:    []ModuleEntry{{Prefix: "src/pkg/", Command: Command{Argv: []string{"sh", "-c", script}, Timeout: 30 * time.Second}}},
	}
}

// TestRunIsolatesSourceRepositoryWithHostExec is criterion G4 (acceptance
// 2): after observe's legs run for real (HostExec, no mocking), the source
// repository's worktree list, status, HEAD, and refs are unchanged.
func TestRunIsolatesSourceRepositoryWithHostExec(t *testing.T) {
	requireGit(t)
	fx := buildLegsFixture(t)
	store := newTestStore(t)
	cfg := hostExecConfig(t)
	pair := Pair{MutantPatch: libMutant, Tests: []string{testPath, newTestPath}}

	root := fx.repo.Root()
	gitState := func() (worktrees, status, head, refs string) {
		t.Helper()
		run := func(args ...string) string {
			out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
			if err != nil {
				t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
			}
			return string(out)
		}
		return run("worktree", "list"), run("status", "--short"), run("rev-parse", "HEAD"), run("show-ref")
	}
	beforeWT, beforeStatus, beforeHead, beforeRefs := gitState()

	if _, err := Run(context.Background(), sandbox.NewHostExec(), "host", cfg, fx.repo, store, fx.base, fx.head, []Pair{pair}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	afterWT, afterStatus, afterHead, afterRefs := gitState()
	if beforeWT != afterWT {
		t.Fatalf("git worktree list changed:\nbefore: %q\nafter:  %q", beforeWT, afterWT)
	}
	if beforeStatus != afterStatus {
		t.Fatalf("git status --short changed:\nbefore: %q\nafter:  %q", beforeStatus, afterStatus)
	}
	if beforeHead != afterHead {
		t.Fatalf("HEAD changed:\nbefore: %q\nafter:  %q", beforeHead, afterHead)
	}
	if beforeRefs != afterRefs {
		t.Fatalf("refs changed:\nbefore: %q\nafter:  %q", beforeRefs, afterRefs)
	}
}

// TestRunEndToEndWithHostExec defends G4 end-to-end: a real HostExec run
// of all three legs on a criterion whose new test catches the mutant.
// Verifies classified outcomes, Faults, and the evidence round-trip.
func TestRunEndToEndWithHostExec(t *testing.T) {
	requireGit(t)
	fx := buildLegsFixture(t)
	store := newTestStore(t)
	cfg := hostExecConfig(t)
	pair := Pair{MutantPatch: libMutant, Tests: []string{testPath, newTestPath}}

	legsOut, err := Run(context.Background(), sandbox.NewHostExec(), "host", cfg, fx.repo, store, fx.base, fx.head, []Pair{pair})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	leg := legsOut[0]
	if leg.A.Got != ledger.Green {
		t.Fatalf("leg (a) Got = %v, want Green", leg.A.Got)
	}
	if leg.B.Got != ledger.Red {
		t.Fatalf("leg (b) Got = %v, want Red", leg.B.Got)
	}
	if leg.C.Got != ledger.Green {
		t.Fatalf("leg (c) Got = %v, want Green", leg.C.Got)
	}
	if leg.Backend != "host" {
		t.Fatalf("Backend = %q, want %q", leg.Backend, "host")
	}
	if faults := leg.Faults(); len(faults) != 0 {
		t.Fatalf("Faults() = %v, want none", faults)
	}

	if b, err := store.Get(leg.Mutant); err != nil || string(b) != string(libMutant) {
		t.Fatalf("stored mutant round-trip: err=%v content=%q", err, b)
	}
	if _, err := store.Get(leg.A.Transcript); err != nil {
		t.Fatalf("leg (a) transcript: %v", err)
	}
	if _, err := store.Get(leg.B.Transcript); err != nil {
		t.Fatalf("leg (b) transcript: %v", err)
	}
}

// TestRunTruncatedStreamIsUncounted is F1: the sandbox keeps only the head
// and tail of an oversized stream, so a leg whose stdout or stderr was
// truncated records Counted=false and the leg is a fault, whatever count
// lines survived. Mutant: ignore the truncation flags.
func TestRunTruncatedStreamIsUncounted(t *testing.T) {
	fx := buildLegsFixture(t)
	store := newTestStore(t)
	pair := Pair{MutantPatch: libMutant, Tests: []string{testPath}}
	mock := &sandbox.Mock{ResponseFunc: func(n int, spec sandbox.Spec) (sandbox.Result, error) {
		switch n {
		case 0: // leg (c)
			return sandbox.Result{ExitCode: 0, Stdout: "9 ok\n", StderrTruncated: true}, nil
		case 1: // leg (a)
			return sandbox.Result{ExitCode: 0, Stdout: "3 ok\n", StdoutTruncated: true}, nil
		}
		return sandbox.Result{ExitCode: 1, Stdout: "1 ok\n"}, nil
	}}
	legsOut, err := Run(context.Background(), mock, "mock", simpleConfig(), fx.repo, store, fx.base, fx.head, []Pair{pair})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	leg := legsOut[0]
	if leg.A.Counted || leg.C.Counted {
		t.Fatalf("truncated legs counted: A executed=%d counted=%v, C executed=%d counted=%v; want both uncounted",
			leg.A.Executed, leg.A.Counted, leg.C.Executed, leg.C.Counted)
	}
	if !leg.B.Counted || leg.B.Executed != 1 {
		t.Fatalf("untruncated leg (b) executed=%d counted=%v, want 1 true", leg.B.Executed, leg.B.Counted)
	}
	if len(leg.Faults()) == 0 {
		t.Fatal("a leg with truncated streams has no faults")
	}
}

// TestRunCountsStreamsApart is F7: stdout and stderr are joined by a
// newline before counting, so a digit ending stdout never runs into a count
// line starting stderr. Mutant: join them with no separator.
func TestRunCountsStreamsApart(t *testing.T) {
	fx := buildLegsFixture(t)
	store := newTestStore(t)
	pair := Pair{MutantPatch: libMutant, Tests: []string{testPath}}
	mock := &sandbox.Mock{DefaultResponse: sandbox.MockResponse{
		Result: sandbox.Result{ExitCode: 0, Stdout: "build 1", Stderr: "1 ok\n"},
	}}
	legsOut, err := Run(context.Background(), mock, "mock", simpleConfig(), fx.repo, store, fx.base, fx.head, []Pair{pair})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if a := legsOut[0].A; a.Executed != 1 || !a.Counted {
		t.Fatalf("leg (a) executed=%d counted=%v, want 1 true", a.Executed, a.Counted)
	}
}

// TestRunRestoreNeverFollowsSymlinks (G4): head replaces a test path,
// or the directory above one, with a symlink into the source checkout.
// Restoring a symlinked test path replaces the link with the base file; a
// symlinked directory above a test path is an error with no Legs. Either
// way the source checkout's status is unchanged. Mutant: restore with
// MkdirAll and WriteFile, which follow symlinks.
func TestRunRestoreNeverFollowsSymlinks(t *testing.T) {
	cfg := simpleConfig()
	cfg.Modules = []ModuleEntry{{Prefix: "src/", Command: Command{Argv: []string{"module"}, Timeout: time.Minute}}}

	t.Run("symlinked test path", func(t *testing.T) {
		g := newGitRepo(t)
		store := newTestStore(t)
		g.write(libPath, baseLib, 0o644)
		g.write(testPath, baseTest, 0o644)
		g.write("src/pkg/victim.txt", "victim content\n", 0o644)
		base := g.commit("base")
		g.symlink(testPath, filepath.Join(g.root, "src", "pkg", "victim.txt"))
		head := g.commit("head")
		before := g.git("status", "--short")

		mock := &sandbox.Mock{ResponseFunc: func(n int, spec sandbox.Spec) (sandbox.Result, error) {
			if n == 1 { // leg (a): the base file stands where the link was
				info, err := os.Lstat(filepath.Join(spec.RepoDir, testPath))
				if err != nil {
					t.Fatalf("leg (a) %s: %v", testPath, err)
				}
				if !info.Mode().IsRegular() {
					t.Errorf("leg (a) %s mode = %v, want a regular file", testPath, info.Mode())
				}
				wantFile(t, spec.RepoDir, testPath, baseTest)
			}
			return sandbox.Result{ExitCode: 0}, nil
		}}
		pair := Pair{MutantPatch: libMutant, Tests: []string{testPath}}
		if _, err := Run(context.Background(), mock, "mock", cfg, g.open(), store, base, head, []Pair{pair}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if after := g.git("status", "--short"); after != before {
			t.Fatalf("source git status --short changed:\nbefore: %q\nafter:  %q", before, after)
		}
	})

	t.Run("symlinked directory above a test path", func(t *testing.T) {
		g := newGitRepo(t)
		store := newTestStore(t)
		g.write(libPath, baseLib, 0o644)
		g.write("src/t/x_test.go", "base x test\n", 0o644)
		g.write("victim/keep.txt", "keep\n", 0o644)
		base := g.commit("base")
		g.symlink("src/t", filepath.Join(g.root, "victim"))
		head := g.commit("head")
		before := g.git("status", "--short")

		mock := &sandbox.Mock{DefaultResponse: sandbox.MockResponse{Result: sandbox.Result{ExitCode: 0}}}
		pair := Pair{MutantPatch: libMutant, Tests: []string{"src/t/x_test.go"}}
		legsOut, err := Run(context.Background(), mock, "mock", cfg, g.open(), store, base, head, []Pair{pair})
		if err == nil || legsOut != nil {
			t.Fatalf("Run through a symlinked directory: want (nil, error), got (%v, %v)", legsOut, err)
		}
		if after := g.git("status", "--short"); after != before {
			t.Fatalf("source git status --short changed:\nbefore: %q\nafter:  %q", before, after)
		}
	})
}

// TestRunRestoresBaseMode (G1 "as at base"): a test path that is
// executable at base and not at head is restored executable. Mutant:
// restore at a fixed 0644.
func TestRunRestoresBaseMode(t *testing.T) {
	g := newGitRepo(t)
	store := newTestStore(t)
	g.write(libPath, baseLib, 0o644)
	g.write(testPath, baseTest, 0o755)
	base := g.commit("base")
	g.write(testPath, headTest, 0o644)
	head := g.commit("head")

	mock := &sandbox.Mock{ResponseFunc: func(n int, spec sandbox.Spec) (sandbox.Result, error) {
		if n == 1 { // leg (a)
			info, err := os.Lstat(filepath.Join(spec.RepoDir, testPath))
			if err != nil {
				t.Fatalf("leg (a) %s: %v", testPath, err)
			}
			if perm := info.Mode().Perm(); perm != 0o755 {
				t.Errorf("leg (a) %s mode = %v, want 0755 as at base", testPath, perm)
			}
		}
		return sandbox.Result{ExitCode: 0}, nil
	}}
	pair := Pair{MutantPatch: libMutant, Tests: []string{testPath}}
	if _, err := Run(context.Background(), mock, "mock", simpleConfig(), g.open(), store, base, head, []Pair{pair}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
