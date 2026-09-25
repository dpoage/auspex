// Package corpus is the seeded-fault corpus (auspex-jmg.9): one test per
// DESIGN.md "Validation > Seeded-fault corpus" case, run against the real
// auspex binary built from this tree over a scratch bd database and a
// disposable fixture git repository. Every case also carries a control: the
// same fixture with its fault removed, which must hold.
//
// auspex names every fault on stderr after its "obnuntiatio" headline, and
// prints record ids (heed adds one <record>#S<k> line per SCOPE item) on
// stdout, plus the "auspicia prospera" headline for a passing cp2.
package corpus

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// auspexBin is the binary built once for the whole package by TestMain.
var auspexBin string

// buildDir is removed by TestMain after every test has run.
var buildDir string

func TestMain(m *testing.M) {
	// go test runs a package's tests in the package directory, so the
	// repository root is its parent (runtime.Caller names a module path
	// under -trimpath, not a directory).
	wd, err := os.Getwd()
	if err != nil {
		panic("corpus: " + err.Error())
	}
	repoRoot := filepath.Dir(wd)

	dir, err := os.MkdirTemp("", "auspex-corpus-bin")
	if err != nil {
		panic(err)
	}
	buildDir = dir
	auspexBin = filepath.Join(dir, "auspex")
	build := exec.Command("go", "build", "-o", auspexBin, "./cmd/auspex")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		panic("corpus: go build ./cmd/auspex: " + err.Error() + ": " + string(out))
	}

	code := m.Run()
	os.RemoveAll(buildDir)
	os.Exit(code)
}

// requireTool fails the test when name is not on PATH; the corpus never skips.
func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Fatalf("%s is required: %v", name, err)
	}
}

// filteredEnv drops every listed key from os.Environ so callers can append
// their own value (including a deliberately empty one) without a duplicate
// entry shadowing it.
func filteredEnv(drop ...string) []string {
	out := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		keep := true
		for _, d := range drop {
			if key == d {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}

// product is a disposable git repository plus its own bd database and
// evidence store (the "fixture git repository with a trivial suite" K1
// asks for). Every subprocess the harness starts in it runs with dir as
// working directory and env as environment.
type product struct {
	t         *testing.T
	dir       string
	env       []string
	stateHome string
}

// newProduct builds one scratch git+bd environment. Its env clears BEADS_DIR
// so bd initializes and finds its database in dir whatever the caller's
// BEADS_DIR is, and points XDG_STATE_HOME and TMPDIR at fresh scratch dirs
// so evidence.Open and auspex's exported leg trees never touch real ones.
func newProduct(t *testing.T) *product {
	t.Helper()
	requireTool(t, "git")
	requireTool(t, "bd")
	dir := t.TempDir()
	stateHome := t.TempDir()
	env := filteredEnv("BEADS_DIR", "XDG_STATE_HOME", "TMPDIR")
	env = append(env, "BEADS_DIR=", "XDG_STATE_HOME="+stateHome, "TMPDIR="+t.TempDir())
	p := &product{t: t, dir: dir, env: env, stateHome: stateHome}
	p.git("init", "-q", "-b", "main")
	p.git("-c", "user.email=t@e.st", "-c", "user.name=t", "commit", "--allow-empty", "-m", "base")
	runIn(t, dir, env, "bd", "init")
	return p
}

func runIn(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (p *product) git(args ...string) string {
	p.t.Helper()
	return runIn(p.t, p.dir, p.env, "git", args...)
}

// gitStdin runs git with input on stdin (for `hash-object --stdin`) and
// returns trimmed stdout.
func (p *product) gitStdin(input string, args ...string) string {
	p.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", p.dir}, args...)...)
	cmd.Env = p.env
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		p.t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// write creates rel under the product tree, making parent directories as
// needed.
func (p *product) write(rel, content string) {
	p.t.Helper()
	path := filepath.Join(p.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

// symlink replaces whatever is at rel with a symlink to target (which may be
// an absolute path outside the product tree, as a malicious head commit could
// carry).
func (p *product) symlink(rel, target string) {
	p.t.Helper()
	path := filepath.Join(p.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.t.Fatal(err)
	}
	if err := os.RemoveAll(path); err != nil {
		p.t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		p.t.Fatal(err)
	}
}

// commit stages every change and commits it, returning the new HEAD.
func (p *product) commit(msg string) string {
	p.t.Helper()
	p.git("add", "-A")
	p.git("-c", "user.email=t@e.st", "-c", "user.name=t", "commit", "-m", msg)
	return p.git("rev-parse", "HEAD")
}

func (p *product) head() string {
	p.t.Helper()
	return p.git("rev-parse", "HEAD")
}

// bead creates a bd task bead and returns its id.
func (p *product) bead(title string) string {
	p.t.Helper()
	out := runIn(p.t, p.dir, p.env, "bd", "create", "-t", "task", title, "--json")
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		p.t.Fatalf("bd create: %v: %s", err, out)
	}
	return created.ID
}

// run executes the built auspex binary with args, in the product's
// directory and environment, and returns its stdout, stderr, and exit
// code.
func (p *product) run(args ...string) (stdout, stderr string, code int) {
	p.t.Helper()
	cmd := exec.Command(auspexBin, args...)
	cmd.Dir = p.dir
	cmd.Env = p.env
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	switch e := err.(type) {
	case nil:
		code = 0
	case *exec.ExitError:
		code = e.ExitCode()
	default:
		p.t.Fatalf("running auspex %v: %v", args, err)
	}
	return so.String(), se.String(), code
}

// mustRun runs args and fails the test unless the exit code is 0.
func (p *product) mustRun(args ...string) (stdout string) {
	p.t.Helper()
	so, se, code := p.run(args...)
	if code != 0 {
		p.t.Fatalf("auspex %v exited %d: stdout=%q stderr=%q", args, code, so, se)
	}
	return so
}

// appoint appoints bead to round at tier, with mergeHash bound when
// non-empty.
func (p *product) appoint(bead, round, tier, mergeHash string) {
	p.t.Helper()
	args := []string{"appoint", "--slice", bead, "--round", round, "--tier", tier}
	if mergeHash != "" {
		args = append(args, "--merge-hash", mergeHash)
	}
	p.mustRun(args...)
}

// mutantPatch writes a unified diff turning "// IMPL: correct" into
// "// IMPL: buggy" in path (a file already committed with that exact line)
// and returns the patch file's path.
func (p *product) mutantPatch(name, path string) string {
	p.t.Helper()
	patch := "--- a/" + path + "\n" +
		"+++ b/" + path + "\n" +
		"@@ -1,3 +1,3 @@\n" +
		" package pkg\n" +
		" \n" +
		"-// IMPL: correct\n" +
		"+// IMPL: buggy\n"
	dst := filepath.Join(p.t.TempDir(), name)
	if err := os.WriteFile(dst, []byte(patch), 0o644); err != nil {
		p.t.Fatal(err)
	}
	return dst
}

// escapeTOML escapes a Go string for embedding inside a TOML basic string.
func escapeTOML(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// writeConfig writes .agents/auspex.toml with one suite command and one
// module command, both `sh -c` scripts, sharing countRegex.
func (p *product) writeConfig(countRegex, suiteScript, suiteTimeout, modulePrefix, moduleScript, moduleTimeout string) {
	p.t.Helper()
	var b strings.Builder
	b.WriteString("count_regex = '" + countRegex + "'\n\n")
	b.WriteString("[suite]\n")
	b.WriteString("argv = [\"sh\", \"-c\", \"" + escapeTOML(suiteScript) + "\"]\n")
	b.WriteString("timeout = \"" + suiteTimeout + "\"\n\n")
	b.WriteString("[[module]]\n")
	b.WriteString("prefix = \"" + modulePrefix + "\"\n")
	b.WriteString("argv = [\"sh\", \"-c\", \"" + escapeTOML(moduleScript) + "\"]\n")
	b.WriteString("timeout = \"" + moduleTimeout + "\"\n")
	p.write(".agents/auspex.toml", b.String())
}

// txScope is one {id, text} entry of a transcript's scope list.
type txScope struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// txPayload is the oracle output schema heed reads.
type txPayload struct {
	Verdict  string    `json:"verdict"`
	Coverage string    `json:"coverage"`
	Matrix   string    `json:"matrix"`
	Scope    []txScope `json:"scope"`
	Reply    string    `json:"reply"`
}

// transcript writes an oracle transcript to name under the product tree.
// approve selects APPROVE or REJECT; matrix is folded into coverage after an
// em-dash (or omitted from coverage when empty); scope is the SCOPE items.
// The reply field never contains the word "verdict", so it can never
// disagree with the verdict field.
func (p *product) transcript(name string, approve bool, matrix string, scope []txScope) string {
	p.t.Helper()
	token := "REJECT"
	if approve {
		token = "APPROVE"
	}
	coverage := "1 family, 2 probes, 0 skipped"
	if matrix != "" {
		coverage += " \u2014 " + matrix
	}
	if scope == nil {
		scope = []txScope{}
	}
	payload := txPayload{
		Verdict:  "VERDICT: " + token,
		Coverage: coverage,
		Matrix:   matrix,
		Scope:    scope,
		Reply:    "no blocking findings",
	}
	b, err := json.Marshal(payload)
	if err != nil {
		p.t.Fatal(err)
	}
	path := filepath.Join(p.dir, name)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		p.t.Fatal(err)
	}
	return path
}

// validMatrix writes a matrix file outside the product tree and returns
// its absolute path, for an APPROVE transcript whose matrix must exist.
func (p *product) validMatrix() string {
	p.t.Helper()
	path := filepath.Join(p.t.TempDir(), "oracle-matrix.md")
	if err := os.WriteFile(path, []byte("# matrix\n"), 0o644); err != nil {
		p.t.Fatal(err)
	}
	return path
}

// requireExit fails the test unless code matches want, printing stdout and
// stderr on mismatch.
func requireExit(t *testing.T, want, code int, stdout, stderr string) {
	t.Helper()
	if code != want {
		t.Fatalf("exit = %d, want %d: stdout=%q stderr=%q", code, want, stdout, stderr)
	}
}

// requireContains fails unless s contains want.
func requireContains(t *testing.T, s, want string) {
	t.Helper()
	if !strings.Contains(s, want) {
		t.Fatalf("output %q does not contain %q", s, want)
	}
}
