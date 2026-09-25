package legs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dpoage/auspex/internal/evidence"
	"github.com/dpoage/auspex/internal/ledger"
	"github.com/dpoage/auspex/internal/repo"
	"github.com/dpoage/llmkit/sandbox"
)

// Pair is one mutant and the test paths it names, as observe reads them
// from repeated --mutant/--test flags.
type Pair struct {
	// MutantPatch is the raw contents of the mutant patch file, applied
	// with `git apply` and stored as evidence (Leg.Mutant).
	MutantPatch []byte
	// Tests are canonical repository-relative paths. Leg (a) restores
	// each from base; leg (b)'s module command is chosen by their
	// longest matching config module entry, which every path in Tests
	// must share.
	Tests []string
}

// Run executes leg (c) once, then legs (a) and (b) for every pair, and
// returns one ledger.Leg per pair in the given order, each carrying the
// shared leg (c) run.
//
// Before any leg runs, Run checks every pair: its test paths are canonical
// and share one module entry, its mutant applies to an export of head, and
// each test path is a regular file or absent at base. Run returns no Legs
// on any error, before or during the legs, so a caller never records a
// partial result — the "no record" half of criterion G5.
func Run(ctx context.Context, sb sandbox.Sandbox, backend string, cfg Config, r *repo.Repo, store *evidence.Store, base, head repo.Hash, pairs []Pair) ([]ledger.Leg, error) {
	if len(pairs) == 0 {
		return nil, fmt.Errorf("legs: at least one mutant/test pair is required")
	}
	moduleCmds, restores, err := preflight(cfg, r, base, head, pairs)
	if err != nil {
		return nil, err
	}

	// Leg (c): head, no mutant, full suite, run exactly once and shared
	// by every pair's record.
	cRun, err := runLeg(ctx, sb, store, r, cfg.CountRegex, head, nil, nil, cfg.Suite, ledger.Green)
	if err != nil {
		return nil, fmt.Errorf("legs: leg (c): %w", err)
	}

	out := make([]ledger.Leg, len(pairs))
	for i, p := range pairs {
		mutantRef, err := store.Put(p.MutantPatch)
		if err != nil {
			return nil, fmt.Errorf("legs: pair %d: storing the mutant: %w", i, err)
		}

		// Leg (a): head + mutant, test paths restored from base
		// (deleted when absent at base), full suite, expected green.
		aRun, err := runLeg(ctx, sb, store, r, cfg.CountRegex, head, p.MutantPatch, restores[i], cfg.Suite, ledger.Green)
		if err != nil {
			return nil, fmt.Errorf("legs: pair %d leg (a): %w", i, err)
		}
		// Leg (b): head + mutant, no restoration, the pair's module
		// command, expected red.
		bRun, err := runLeg(ctx, sb, store, r, cfg.CountRegex, head, p.MutantPatch, nil, moduleCmds[i], ledger.Red)
		if err != nil {
			return nil, fmt.Errorf("legs: pair %d leg (b): %w", i, err)
		}

		out[i] = ledger.Leg{
			Base:    base,
			Head:    head,
			Mutant:  mutantRef,
			Tests:   append([]string(nil), p.Tests...),
			Backend: backend,
			A:       aRun,
			B:       bRun,
			C:       cRun,
		}
	}
	return out, nil
}

// preflight checks every pair before any leg runs and returns each pair's
// module command and the base state of each of its test paths. It exports
// head once to check every mutant with `git apply --check`, and base once to
// read the test paths; both exports are removed on return.
func preflight(cfg Config, r *repo.Repo, base, head repo.Hash, pairs []Pair) ([]Command, [][]baseFile, error) {
	cmds := make([]Command, len(pairs))
	for i, p := range pairs {
		cmd, err := cfg.ModuleFor(p.Tests)
		if err != nil {
			return nil, nil, fmt.Errorf("legs: pair %d: %w", i, err)
		}
		cmds[i] = cmd
	}

	headDir, err := exportTree(r, head)
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(headDir)
	for i, p := range pairs {
		if err := applyMutant(headDir, p.MutantPatch, true); err != nil {
			return nil, nil, fmt.Errorf("legs: pair %d: mutant does not apply to head: %w", i, err)
		}
	}

	baseDir, err := exportTree(r, base)
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(baseDir)
	restores := make([][]baseFile, len(pairs))
	for i, p := range pairs {
		for _, t := range p.Tests {
			f, err := readBase(baseDir, t)
			if err != nil {
				return nil, nil, fmt.Errorf("legs: pair %d: %w", i, err)
			}
			restores[i] = append(restores[i], f)
		}
	}
	return cmds, restores, nil
}

// runLeg exports head into a fresh tree outside the repository, applies
// mutant when non-nil, restores the test paths in restore when non-nil,
// and executes cmd in it, classifying the result against want.
// Restoration happens after the mutant is applied, so a test file's base
// state always wins over anything the mutant touched there.
func runLeg(ctx context.Context, sb sandbox.Sandbox, store *evidence.Store, r *repo.Repo, countRE *regexp.Regexp, head repo.Hash, mutant []byte, restore []baseFile, cmd Command, want ledger.Outcome) (ledger.LegRun, error) {
	dir, err := exportTree(r, head)
	if err != nil {
		return ledger.LegRun{}, err
	}
	defer os.RemoveAll(dir)

	if mutant != nil {
		if err := applyMutant(dir, mutant, false); err != nil {
			return ledger.LegRun{}, fmt.Errorf("mutant does not apply: %w", err)
		}
	}
	if restore != nil {
		if err := restoreFromBase(dir, restore); err != nil {
			return ledger.LegRun{}, err
		}
	}

	res, err := sb.Exec(ctx, sandbox.Spec{RepoDir: dir, Cmd: cmd.Argv, Timeout: cmd.Timeout})
	if err != nil {
		return ledger.LegRun{}, fmt.Errorf("running %v: %w", cmd.Argv, err)
	}
	return buildLegRun(store, countRE, cmd.Argv, want, res)
}

// exportTree exports rev into a fresh temporary directory outside the
// repository, as every leg's tree must be per G4/acceptance 2.
func exportTree(r *repo.Repo, rev repo.Hash) (string, error) {
	dir, err := os.MkdirTemp("", "auspex-leg-")
	if err != nil {
		return "", fmt.Errorf("legs: preparing a tree: %w", err)
	}
	if err := r.Export(rev, dir); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("legs: exporting %s: %w", rev, err)
	}
	return dir, nil
}

// applyMutant applies patch to the tree at dir with `git apply`, reading the
// patch from stdin; with check it only reports whether the patch applies.
// This works on a plain directory with no .git: git apply resolves paths
// relative to its working directory regardless of whether one is a
// repository.
func applyMutant(dir string, patch []byte, check bool) error {
	args := []string{"apply", "--whitespace=nowarn"}
	if check {
		args = append(args, "--check")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git apply: %v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// baseFile is one test path's state at base: a regular file's content and
// permission bits, or absent.
type baseFile struct {
	path    string
	present bool
	content []byte
	perm    fs.FileMode
}

// readBase reads the canonical tree path p from the base export at dir
// without following a symlink. A symlink or non-directory above p means
// base has no file there, since git tracks nothing beneath one; p itself
// must be a regular file or absent.
func readBase(dir, p string) (baseFile, error) {
	parts := strings.Split(p, "/")
	cur := dir
	for _, c := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, c)
		info, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) || err == nil && !info.IsDir() {
			return baseFile{path: p}, nil
		}
		if err != nil {
			return baseFile{}, fmt.Errorf("legs: reading %s at base: %w", p, err)
		}
	}
	full := filepath.Join(dir, filepath.FromSlash(p))
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return baseFile{path: p}, nil
	}
	if err != nil {
		return baseFile{}, fmt.Errorf("legs: reading %s at base: %w", p, err)
	}
	if !info.Mode().IsRegular() {
		return baseFile{}, fmt.Errorf("legs: %s at base is not a regular file", p)
	}
	content, err := os.ReadFile(full)
	if err != nil {
		return baseFile{}, fmt.Errorf("legs: reading %s at base: %w", p, err)
	}
	return baseFile{path: p, present: true, content: content, perm: info.Mode().Perm()}, nil
}

// restoreFromBase makes every test path in the leg tree at dir as it is at
// base: the base content with its permission bits, or absent. It never
// writes, creates, or removes through a symlink. Every directory above a
// path is checked with Lstat, and a symlink or non-directory there is an
// error; a missing one is created only when base has the file. Whatever
// is at the path itself, a file or a symlink, is removed before the base
// content is written.
func restoreFromBase(dir string, files []baseFile) error {
	for _, f := range files {
		if err := restoreFile(dir, f); err != nil {
			return fmt.Errorf("legs: restoring %s from base: %w", f.path, err)
		}
	}
	return nil
}

func restoreFile(dir string, f baseFile) error {
	parts := strings.Split(f.path, "/")
	cur := dir
	for i, c := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, c)
		info, err := os.Lstat(cur)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if !f.present {
				return nil // nothing to remove beneath a missing directory
			}
			if err := os.Mkdir(cur, 0o755); err != nil {
				return err
			}
		case err != nil:
			return err
		case info.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink", strings.Join(parts[:i+1], "/"))
		case !info.IsDir():
			return fmt.Errorf("%s is not a directory", strings.Join(parts[:i+1], "/"))
		}
	}
	dst := filepath.Join(dir, filepath.FromSlash(f.path))
	info, err := os.Lstat(dst)
	switch {
	case err == nil && info.IsDir():
		return fmt.Errorf("%s is a directory", f.path)
	case err == nil:
		if err := os.Remove(dst); err != nil {
			return err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if !f.present {
		return nil
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, f.perm)
	if err != nil {
		return err
	}
	_, err = out.Write(f.content)
	if err == nil {
		err = out.Chmod(f.perm) // the umask may have narrowed perm
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// buildLegRun stores res's combined transcript as evidence and classifies
// its outcome. InfraKilled is checked before the exit code: a sandbox kill
// is recorded as Killed, never as Red, regardless of what the command
// happened to exit with. The count reads stdout and stderr joined by a
// newline, so digits never run together across the two; a truncated
// stream may have lost count lines, so its leg is recorded uncounted.
func buildLegRun(store *evidence.Store, countRE *regexp.Regexp, cmd []string, want ledger.Outcome, res sandbox.Result) (ledger.LegRun, error) {
	ref, err := store.Put(transcript(cmd, res))
	if err != nil {
		return ledger.LegRun{}, fmt.Errorf("legs: storing the transcript: %w", err)
	}
	var got ledger.Outcome
	switch {
	case res.InfraKilled():
		got = ledger.Killed
	case res.ExitCode == 0:
		got = ledger.Green
	default:
		got = ledger.Red
	}
	executed, counted := 0, false
	if !res.StdoutTruncated && !res.StderrTruncated {
		executed, counted = countExecuted(countRE, res.Stdout+"\n"+res.Stderr)
	}
	return ledger.LegRun{
		Cmd:        cmd,
		Want:       want,
		Got:        got,
		ExitCode:   res.ExitCode,
		KillReason: res.KillReason(),
		Executed:   executed,
		Counted:    counted,
		Transcript: ref,
		Duration:   res.Duration,
	}, nil
}

// transcript renders cmd's execution as one byte string for evidence
// storage: the argv, exit and kill state, then stdout and stderr in full.
func transcript(cmd []string, res sandbox.Result) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "$ %s\nexit=%d killed=%v\n", strings.Join(cmd, " "), res.ExitCode, res.InfraKilled())
	if reason := res.KillReason(); reason != "" {
		fmt.Fprintf(&b, "kill reason: %s\n", reason)
	}
	b.WriteString("\n--- stdout ---\n")
	b.WriteString(res.Stdout)
	b.WriteString("\n--- stderr ---\n")
	b.WriteString(res.Stderr)
	return b.Bytes()
}
