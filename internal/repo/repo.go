// Package repo is how auspex asks git. It hides the repository root
// discovery, revision resolution, and tree export behind one type.
package repo

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Hash is a full 40-hex git object name.
type Hash string

// Repo is a git repository that auspex reads. Every method is read-only with
// respect to the repository.
type Repo struct {
	root string
}

// Open returns the repository containing dir. It is an error when dir is
// outside any repository.
func Open(dir string) (*Repo, error) {
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("repo: %s is not inside a git repository: %w", dir, err)
	}
	return &Repo{root: strings.TrimSpace(string(out))}, nil
}

// Root is the repository's absolute working-tree root.
func (r *Repo) Root() string { return r.root }

// fullHash is the only shape Resolve returns.
var fullHash = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Resolve returns the full hash of the commit that rev names; rev^{commit}
// peels tags and other object names to commits. An unknown rev, an option,
// a range, or a negation is an error.
func (r *Repo) Resolve(rev string) (Hash, error) {
	out, err := git(r.root, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("repo: unknown rev %q: %w", rev, err)
	}
	// --verify still prints "^<hash>" for a negation such as ^HEAD.
	h := strings.TrimSuffix(string(out), "\n")
	if !fullHash.MatchString(h) {
		return "", fmt.Errorf("repo: rev %q does not name one commit (git printed %q)", rev, h)
	}
	return Hash(h), nil
}

// Export writes the tree of rev into dst, creating dst; an existing dst must
// be empty. Every file is written exactly as stored at rev (no attribute
// processing: export-ignore, export-subst, eol, and filters do not apply),
// with its executable bit; symlinks stay symlinks, and a submodule is an
// empty directory. Export writes only inside dst and never through a
// symlink it wrote: a tree with a path component equal to .git (in any
// case) is an error before anything is written, and an entry under a
// symlink or file is an error when it is reached. The repository's
// worktrees, status, index, HEAD, and refs are unchanged afterwards.
func (r *Repo) Export(rev Hash, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("repo: export: %w", err)
	}
	if present, err := os.ReadDir(dst); err != nil {
		return fmt.Errorf("repo: export: %w", err)
	} else if len(present) != 0 {
		return fmt.Errorf("repo: export: %s is not empty", dst)
	}
	list, err := git(r.root, "ls-tree", "-r", "-z", "--full-tree", "--end-of-options", string(rev))
	if err != nil {
		return fmt.Errorf("repo: export %s: %w", rev, err)
	}
	blobs, err := newBlobReader(r.root)
	if err != nil {
		return fmt.Errorf("repo: export %s: %w", rev, err)
	}
	defer blobs.abort()
	var entries []treeEntry
	for _, line := range strings.Split(string(list), "\x00") {
		if line == "" {
			continue
		}
		e, err := parseTreeEntry(line)
		if err != nil {
			return fmt.Errorf("repo: export %s: %w", rev, err)
		}
		if err := checkExportPath(e.path); err != nil {
			return fmt.Errorf("repo: export %s: %s: %w", rev, e.path, err)
		}
		entries = append(entries, e)
	}
	for _, e := range entries {
		if err := writeEntry(blobs, dst, e); err != nil {
			return fmt.Errorf("repo: export %s: %s: %w", rev, e.path, err)
		}
	}
	if err := blobs.close(); err != nil {
		return fmt.Errorf("repo: export %s: %w", rev, err)
	}
	return nil
}

// treeEntry is one entry of `git ls-tree -z`: "<mode> <type> <oid>\t<path>".
type treeEntry struct {
	mode, oid, path string
}

func parseTreeEntry(line string) (treeEntry, error) {
	meta, p, ok := strings.Cut(line, "\t")
	fields := strings.Fields(meta)
	if !ok || len(fields) != 3 {
		return treeEntry{}, fmt.Errorf("unreadable ls-tree entry %q", line)
	}
	return treeEntry{mode: fields[0], oid: fields[2], path: p}, nil
}

// checkExportPath refuses a tree path that would leave the export directory
// or has a .git component (compared case-insensitively).
func checkExportPath(p string) error {
	if !filepath.IsLocal(p) {
		return fmt.Errorf("path leaves the export directory")
	}
	for _, c := range strings.Split(p, "/") {
		if strings.EqualFold(c, ".git") {
			return fmt.Errorf("path has a .git component")
		}
	}
	return nil
}

// writeEntry writes one tree entry under dst.
func writeEntry(blobs *blobReader, dst string, e treeEntry) error {
	if err := makeParents(dst, e.path); err != nil {
		return err
	}
	target := filepath.Join(dst, filepath.FromSlash(e.path))
	switch e.mode {
	case "100644", "100755":
		perm := os.FileMode(0o644)
		if e.mode == "100755" {
			perm = 0o755
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if err != nil {
			return err
		}
		err = blobs.copy(e.oid, f)
		if err == nil {
			err = f.Chmod(perm) // the umask may have narrowed perm
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	case "120000":
		var link bytes.Buffer
		if err := blobs.copy(e.oid, &link); err != nil {
			return err
		}
		return os.Symlink(link.String(), target)
	case "160000":
		return os.Mkdir(target, 0o755)
	}
	return fmt.Errorf("unsupported mode %s", e.mode)
}

// makeParents creates the directories above tree path p under dst, one
// component at a time. A component that already exists must be a real
// directory; a symlink or file there is an error, never a path to follow.
func makeParents(dst, p string) error {
	parts := strings.Split(p, "/")
	dir := dst
	for i, c := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, c)
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			continue
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", strings.Join(parts[:i+1], "/"))
		}
	}
	return nil
}

// blobReader streams blob contents from one `git cat-file --batch` process,
// which flushes its reply after every object.
type blobReader struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	stderr bytes.Buffer
	closed bool
}

func newBlobReader(dir string) (*blobReader, error) {
	b := &blobReader{cmd: exec.Command("git", "-C", dir, "cat-file", "--batch")}
	b.cmd.Stderr = &b.stderr
	in, err := b.cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := b.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := b.cmd.Start(); err != nil {
		return nil, err
	}
	b.in, b.out = in, bufio.NewReader(out)
	return b, nil
}

// copy writes the content of blob oid to w.
func (b *blobReader) copy(oid string, w io.Writer) error {
	if _, err := io.WriteString(b.in, oid+"\n"); err != nil {
		return fmt.Errorf("git cat-file: %w", err)
	}
	header, err := b.out.ReadString('\n')
	if err != nil {
		return fmt.Errorf("git cat-file: %w", err)
	}
	var gotOID, typ string
	var size int64
	if n, _ := fmt.Sscanf(header, "%s %s %d\n", &gotOID, &typ, &size); n != 3 || gotOID != oid || typ != "blob" {
		return fmt.Errorf("git cat-file: unexpected reply %q for blob %s", strings.TrimSpace(header), oid)
	}
	if _, err := io.CopyN(w, b.out, size); err != nil {
		return fmt.Errorf("git cat-file: %w", err)
	}
	if lf, err := b.out.ReadByte(); err != nil || lf != '\n' {
		return fmt.Errorf("git cat-file: blob %s is not followed by a newline", oid)
	}
	return nil
}

// close ends the cat-file process after its last reply.
func (b *blobReader) close() error {
	b.closed = true
	b.in.Close()
	if err := b.cmd.Wait(); err != nil {
		return fmt.Errorf("git cat-file: %w: %s", err, strings.TrimSpace(b.stderr.String()))
	}
	return nil
}

// abort kills a cat-file process that close did not end; a reply it is
// still writing would otherwise block Wait.
func (b *blobReader) abort() {
	if !b.closed {
		b.cmd.Process.Kill()
		b.cmd.Wait()
	}
}

// git runs one git command in dir and returns its stdout. Stderr travels in
// the error.
func git(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, err
		}
		return nil, fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out, nil
}
