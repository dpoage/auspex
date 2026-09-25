package repo

import (
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// requireGit fails the test when git is missing; the gate never skips.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required: %v", err)
	}
}

// fixtureRepo builds a small repository with two commits and returns the
// Repo, the two full hashes, and the root.
func fixtureRepo(t *testing.T) (*Repo, Hash, Hash, string) {
	t.Helper()
	root := t.TempDir()
	gitout := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	gitout("init", "-b", "main")
	write := func(name, body string) {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) Hash {
		gitout("-c", "user.email=t@e.st", "-c", "user.name=t", "commit", "-a", "-m", msg)
		out := gitout("rev-parse", "HEAD")
		return Hash(strings.TrimSpace(out))
	}
	write("hello.txt", "hello\n")
	gitout("add", ".")
	base := commit("base")
	write("hello.txt", "goodbye\n")
	write("sub/inner.txt", "inner\n")
	if err := os.Symlink("hello.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	gitout("add", ".")
	head := commit("head")
	r, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return r, base, head, root
}

func TestRepoOpenOutsideRepositoryErrors(t *testing.T) {
	requireGit(t)
	if _, err := Open(t.TempDir()); err == nil {
		t.Fatal("Open outside a repository must error")
	}
}

// TestRepoResolveFullHashAndUnknownRev: for every input, Resolve returns
// exactly one 40-lowercase-hex commit hash or an error.
func TestRepoResolveFullHashAndUnknownRev(t *testing.T) {
	requireGit(t)
	r, _, head, root := fixtureRepo(t)
	if r.Root() != root {
		t.Fatalf("Root() = %q, want %q", r.Root(), root)
	}
	for _, rev := range []string{"HEAD", string(head), string(head)[:9], "main"} {
		got, err := r.Resolve(rev)
		if err != nil || got != head {
			t.Fatalf("Resolve(%q) = %q (%v), want %q", rev, got, err, head)
		}
	}
	for _, rev := range []string{
		"no-such-rev", "", "^HEAD", "HEAD~1..HEAD", "HEAD...HEAD~1",
		"--foo", "-q", "--all", "--verify", "--git-dir", "--show-toplevel", "--symbolic",
		"--since=2020-01-01",
	} {
		if got, err := r.Resolve(rev); err == nil {
			t.Errorf("Resolve(%q) = %q with no error, want an error", rev, got)
		}
	}
}

// TestRepoExportLeavesRepositoryUnchanged is criterion R1: Export writes the
// tree with no .git and leaves worktrees, status, HEAD, and every ref as
// they were.
func TestRepoExportLeavesRepositoryUnchanged(t *testing.T) {
	requireGit(t)
	r, _, head, root := fixtureRepo(t)
	gitout := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	before := gitout("worktree", "list", "--porcelain") +
		gitout("status", "--short") +
		gitout("rev-parse", "HEAD") +
		gitout("for-each-ref")
	// A second worktree exists so a worktree-add mutant has state to disturb.
	other := filepath.Join(t.TempDir(), "other")
	gitout("worktree", "add", "--detach", other, string(head))
	before = gitout("worktree", "list", "--porcelain") +
		gitout("status", "--short") +
		gitout("rev-parse", "HEAD") +
		gitout("for-each-ref")

	dst := filepath.Join(t.TempDir(), "tree")
	if err := r.Export(head, dst); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dst, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "goodbye\n" {
		t.Fatalf("exported hello.txt = %q", body)
	}
	if _, err := os.Stat(filepath.Join(dst, "sub", "inner.txt")); err != nil {
		t.Fatalf("nested file missing from export: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".git")); !os.IsNotExist(err) {
		t.Fatalf("export must contain no .git (stat err = %v)", err)
	}
	after := gitout("worktree", "list", "--porcelain") +
		gitout("status", "--short") +
		gitout("rev-parse", "HEAD") +
		gitout("for-each-ref")
	if after != before {
		t.Fatalf("repository state changed across Export:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestRepoExportWritesTreeExactly: Export writes every file of
// `git ls-tree -r rev` byte-identical to its blob, with its executable bit,
// symlinks as symlinks, and nothing else; attributes (export-ignore,
// export-subst, eol, ident) do not apply. The index is unchanged.
func TestRepoExportWritesTreeExactly(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	gitout := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	write := func(name, body string, perm os.FileMode) {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), perm); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, perm); err != nil {
			t.Fatal(err)
		}
	}
	gitout("init", "-b", "main")
	write(".gitattributes", "/tests export-ignore\nVERSION export-subst\ncrlf.txt text eol=crlf\nident.txt ident\n", 0o644)
	write("tests/x_test.go", "package x\n", 0o644)
	write("VERSION", "ver: $Format:%H$\n", 0o644)
	write("crlf.txt", "one\ntwo\n", 0o644)
	write("ident.txt", "$Id$\n", 0o644)
	write("run.sh", "#!/bin/sh\necho run\n", 0o755)
	write("a/b/deep.txt", "deep\n", 0o644)
	if err := os.Symlink("run.sh", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	gitout("add", ".")
	gitout("-c", "user.email=t@e.st", "-c", "user.name=t", "commit", "-m", "attributes")
	head := Hash(strings.TrimSpace(gitout("rev-parse", "HEAD")))
	r, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(root, strings.TrimSpace(gitout("rev-parse", "--git-path", "index")))
	indexSum := func() [32]byte {
		b, err := os.ReadFile(indexPath)
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(b)
	}
	indexBefore := indexSum()

	dst := filepath.Join(t.TempDir(), "tree")
	if err := r.Export(head, dst); err != nil {
		t.Fatal(err)
	}

	var want []string
	for _, entry := range strings.Split(gitout("ls-tree", "-r", "-z", string(head)), "\x00") {
		if entry == "" {
			continue
		}
		meta, name, _ := strings.Cut(entry, "\t")
		f := strings.Fields(meta)
		mode, oid := f[0], f[2]
		want = append(want, name)
		blob := gitout("cat-file", "blob", oid)
		p := filepath.Join(dst, name)
		info, err := os.Lstat(p)
		if err != nil {
			t.Errorf("%s (%s) missing from export: %v", name, mode, err)
			continue
		}
		switch mode {
		case "120000":
			target, err := os.Readlink(p)
			if err != nil || target != blob {
				t.Errorf("%s: symlink target %q (%v), want %q", name, target, err, blob)
			}
		case "100644", "100755":
			if !info.Mode().IsRegular() {
				t.Errorf("%s: mode %v, want a regular file", name, info.Mode())
				continue
			}
			if got, want := info.Mode().Perm()&0o111 != 0, mode == "100755"; got != want {
				t.Errorf("%s: executable = %v, want %v (mode %v)", name, got, want, info.Mode())
			}
			body, err := os.ReadFile(p)
			if err != nil || string(body) != blob {
				t.Errorf("%s: content %q (%v), want the blob %q", name, body, err, blob)
			}
		default:
			t.Fatalf("fixture has unexpected mode %s for %s", mode, name)
		}
	}
	var got []string
	err = filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dst, p)
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("export holds\n%s\nwant exactly ls-tree's\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if indexSum() != indexBefore {
		t.Error("Export changed the index")
	}

	stray := t.TempDir()
	if err := os.WriteFile(filepath.Join(stray, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Export(head, stray); err == nil {
		t.Error("Export into a non-empty directory must error")
	}
}

// TestRepoExportRefusesUnsafeTrees: on hand-built trees that git clone
// accepts, Export errors, writes nothing outside dst, writes no path with a
// .git component (in any case), and leaves the repository unchanged. One
// tree holds a symlink a pointing outside dst and a tree a/x; the others
// hold .git/config and .GIT/config.
func TestRepoExportRefusesUnsafeTrees(t *testing.T) {
	requireGit(t)
	r, _, _, root := fixtureRepo(t)
	gitIn := func(stdin string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.email=t@e.st", "-c", "user.name=t"}, args...)...)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	outside := t.TempDir()
	blob := gitIn("pwned\n", "hash-object", "-w", "--stdin")
	link := gitIn(outside, "hash-object", "-w", "--stdin")
	sub := gitIn("100644 blob "+blob+"\tx\n", "mktree")
	config := gitIn("100644 blob "+blob+"\tconfig\n", "mktree")
	commit := func(tree string) Hash { return Hash(gitIn("", "commit-tree", tree, "-m", "hand-built")) }
	trees := map[string]Hash{
		"symlink a outside, tree a/x": commit(gitIn("120000 blob "+link+"\ta\n040000 tree "+sub+"\ta\n", "mktree")),
		".git/config":                 commit(gitIn("040000 tree "+config+"\t.git\n", "mktree")),
		".GIT/config":                 commit(gitIn("040000 tree "+config+"\t.GIT\n", "mktree")),
	}
	state := func() string {
		return gitIn("", "worktree", "list", "--porcelain") + gitIn("", "status", "--short") +
			gitIn("", "rev-parse", "HEAD") + gitIn("", "for-each-ref")
	}
	before := state()
	for name, rev := range trees {
		dst := filepath.Join(t.TempDir(), "tree")
		if err := r.Export(rev, dst); err == nil {
			t.Errorf("%s: Export returned no error", name)
		}
		present, err := os.ReadDir(outside)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range present {
			t.Errorf("%s: Export wrote %s outside dst", name, e.Name())
		}
		err = filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dst, p)
			for _, c := range strings.Split(filepath.ToSlash(rel), "/") {
				if strings.EqualFold(c, ".git") {
					t.Errorf("%s: Export wrote %s", name, rel)
				}
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if after := state(); after != before {
		t.Fatalf("repository state changed across Export:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
