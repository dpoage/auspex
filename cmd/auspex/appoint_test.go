package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dpoage/auspex/internal/ledger"
	"github.com/dpoage/auspex/internal/repo"
)

// requireGit fails the test when git is missing; the gate never skips.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required: %v", err)
	}
}

// gitBDEnv builds one directory that is both a git repository and a bd database.
func gitBDEnv(t *testing.T) (*ledger.Ledger, string) {
	t.Helper()
	requireGit(t)
	if _, err := exec.LookPath("bd"); err != nil {
		t.Fatalf("bd is required: %v", err)
	}
	dir := t.TempDir()
	t.Setenv("BEADS_DIR", "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"-c", "user.email=t@e.st", "-c", "user.name=t", "commit", "--allow-empty", "-m", "base"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	cmd := exec.Command("bd", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bd init: %v: %s", err, out)
	}
	t.Chdir(dir)
	return ledger.New(dir, nil), dir
}

// fullHead is the fixture repository's full 40-hex HEAD hash.
func fullHead(t *testing.T) repo.Hash {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return repo.Hash(strings.TrimSpace(string(out)))
}

func createBead(t *testing.T, title string) string {
	t.Helper()
	out, err := exec.Command("bd", "create", "-t", "task", title, "--json").Output()
	if err != nil {
		t.Fatalf("bd create: %v", err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("bd create: %v", err)
	}
	return created.ID
}

// TestAppointRecordsFullMergeHash pins A1: MergeHash is the full hash of
// --merge-hash, or "" when absent; an unknown tier or rev is exit 2 with
// nothing recorded.
func TestAppointRecordsFullMergeHash(t *testing.T) {
	t.Run("full hash recorded", func(t *testing.T) {
		led, _ := gitBDEnv(t)
		bead := createBead(t, "appointed slice")
		short := string(fullHead(t))[:9]
		var stdout, stderr bytes.Buffer
		if code := runAppoint([]string{
			"--slice", bead, "--round", "r-a", "--tier", "heavy", "--merge-hash", short,
		}, &stdout, &stderr); code != 0 {
			t.Fatalf("appoint exited %d: %s", code, stderr.String())
		}
		recordID := ledger.RecordID(strings.TrimSpace(stdout.String()))
		rec, err := led.Get(recordID)
		if err != nil {
			t.Fatalf("Get(%s): %v", recordID, err)
		}
		a, ok := rec.Body.(ledger.Appointment)
		if !ok {
			t.Fatalf("record body is %T, want Appointment", rec.Body)
		}
		if a.MergeHash != fullHead(t) {
			t.Fatalf("MergeHash = %q, want the full hash %q", a.MergeHash, fullHead(t))
		}
		if a.Round != "r-a" || a.Tier != ledger.Heavy {
			t.Fatalf("appointment = %+v", a)
		}
	})
	t.Run("no flag means empty merge hash", func(t *testing.T) {
		led, _ := gitBDEnv(t)
		bead := createBead(t, "appointed without hash")
		var stdout, stderr bytes.Buffer
		if code := runAppoint([]string{
			"--slice", bead, "--round", "r-a", "--tier", "light",
		}, &stdout, &stderr); code != 0 {
			t.Fatalf("appoint exited %d: %s", code, stderr.String())
		}
		rec, err := led.Get(ledger.RecordID(strings.TrimSpace(stdout.String())))
		if err != nil {
			t.Fatal(err)
		}
		a := rec.Body.(ledger.Appointment)
		if a.MergeHash != "" {
			t.Fatalf("MergeHash = %q, want empty", a.MergeHash)
		}
		if a.Tier != ledger.Light {
			t.Fatalf("Tier = %v", a.Tier)
		}
	})
	t.Run("unknown tier is a usage error and records nothing", func(t *testing.T) {
		led, _ := gitBDEnv(t)
		bead := createBead(t, "unappointed, bad tier")
		var stdout, stderr bytes.Buffer
		code := runAppoint([]string{
			"--slice", bead, "--round", "r-a", "--tier", "colossal",
		}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("unknown tier exited %d, want 2", code)
		}
		slices, err := led.Round("r-a")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range slices {
			if s.ID == ledger.SliceID(bead) && len(s.Records) != 0 {
				t.Fatalf("a refused appoint recorded %+v", s.Records)
			}
		}
	})
	t.Run("unknown rev is a usage error and records nothing", func(t *testing.T) {
		led, _ := gitBDEnv(t)
		bead := createBead(t, "unappointed, bad rev")
		var stdout, stderr bytes.Buffer
		code := runAppoint([]string{
			"--slice", bead, "--round", "r-a", "--tier", "standard", "--merge-hash", "not-a-rev",
		}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("unknown rev exited %d, want 2", code)
		}
		slices, err := led.Round("r-a")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range slices {
			if s.ID == ledger.SliceID(bead) && len(s.Records) != 0 {
				t.Fatalf("a refused appoint recorded %+v", s.Records)
			}
		}
	})
	t.Run("missing bead is obnuntiatio", func(t *testing.T) {
		_, _ = gitBDEnv(t)
		var stdout, stderr bytes.Buffer
		if code := runAppoint([]string{
			"--slice", "nosuchbead-xyz", "--round", "r-a", "--tier", "heavy",
		}, &stdout, &stderr); code != 1 {
			t.Fatalf("missing bead exited %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "obnuntiatio") {
			t.Fatalf("stderr = %q, want an obnuntiatio headline", stderr.String())
		}
	})
	t.Run("slice label added", func(t *testing.T) {
		gitBDEnv(t)
		bead := createBead(t, "labeled slice")
		var stdout, stderr bytes.Buffer
		if code := runAppoint([]string{
			"--slice", bead, "--round", "r-a", "--tier", "heavy",
		}, &stdout, &stderr); code != 0 {
			t.Fatalf("appoint exited %d: %s", code, stderr.String())
		}
		out, err := exec.Command("bd", "list", "--json", "--all", "-n", "0", "--label", "auspex-slice:r-a").Output()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), bead) {
			t.Fatalf("bd list of the round's slices lacks %s: %s", bead, out)
		}
	})
}

// requireRefused runs appoint with args and requires exit 2, appoint's own usage
// on stderr, and no comment or label recorded on bead.
func requireRefused(t *testing.T, bead string, args []string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := runAppoint(args, &stdout, &stderr); code != 2 {
		t.Errorf("appoint %q exited %d, want 2 (stdout %q, stderr %q)", args, code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "-merge-hash") || strings.Contains(stderr.String(), "commands:") {
		t.Errorf("appoint %q: usage does not list appoint's flags:\n%s", args, stderr.String())
	}
	out, err := exec.Command("bd", "show", bead, "--json").Output()
	if err != nil {
		t.Fatalf("bd show %s: %v", bead, err)
	}
	var shown []struct {
		Labels       []string `json:"labels"`
		CommentCount int      `json:"comment_count"`
	}
	if err := json.Unmarshal(out, &shown); err != nil || len(shown) != 1 {
		t.Fatalf("bd show %s: %v: %s", bead, err, out)
	}
	if len(shown[0].Labels) != 0 || shown[0].CommentCount != 0 {
		t.Fatalf("appoint %q recorded on %s: labels %v, %d comments", args, bead, shown[0].Labels, shown[0].CommentCount)
	}
}

// TestAppointRefusesMalformedArguments: trailing positional arguments and a
// --merge-hash that is empty or whitespace are exit 2 with nothing recorded.
func TestAppointRefusesMalformedArguments(t *testing.T) {
	gitBDEnv(t)
	bead := createBead(t, "malformed appoints")
	flags := []string{"--slice", bead, "--round", "r-a", "--tier", "heavy"}
	for _, extra := range [][]string{
		{"HEAD", "--merge-hash", "HEAD"},
		{"--", "--merge-hash", "HEAD"},
		{"HEAD"},
		{"--merge-hash", ""},
		{"--merge-hash="},
		{"--merge-hash", "  "},
	} {
		requireRefused(t, bead, append(append([]string(nil), flags...), extra...))
	}
}

// TestAppointRefusesInvalidRoundID: a round id that does not match
// ^[A-Za-z0-9][A-Za-z0-9._-]{0,241}$ is exit 2 with nothing recorded.
func TestAppointRefusesInvalidRoundID(t *testing.T) {
	gitBDEnv(t)
	bead := createBead(t, "bad round ids")
	for _, round := range []string{"a,b", " ", "jmg-1 ", " jmg-1", "-r", ".r", "a/b", "a:b"} {
		requireRefused(t, bead, []string{"--slice", bead, "--round", round, "--tier", "heavy"})
	}
	var stdout, stderr bytes.Buffer
	if code := runAppoint([]string{"--slice", bead, "--round", "jmg-1.r_2", "--tier", "light"}, &stdout, &stderr); code != 0 {
		t.Fatalf("a valid round id exited %d: %s", code, stderr.String())
	}
}

// TestAppointRoundIDLengthBound: 242 characters fills bd's 255-character
// "auspex-slice:<id>" label exactly; one more is exit 2 with nothing recorded.
func TestAppointRoundIDLengthBound(t *testing.T) {
	led, _ := gitBDEnv(t)
	longest := strings.Repeat("r0._-", 48) + "ab"
	if len(longest) != 242 {
		t.Fatalf("fixture id has %d characters, want 242", len(longest))
	}
	bead := createBead(t, "longest round id")
	var stdout, stderr bytes.Buffer
	if code := runAppoint([]string{"--slice", bead, "--round", longest, "--tier", "standard"}, &stdout, &stderr); code != 0 {
		t.Fatalf("appoint with a 242-character round id exited %d: %s", code, stderr.String())
	}
	slices, err := led.Round(ledger.RoundID(longest))
	if err != nil {
		t.Fatal(err)
	}
	if len(slices) != 1 || string(slices[0].ID) != bead || slices[0].Appointment == nil || slices[0].Appointment.Round != ledger.RoundID(longest) {
		t.Fatalf("Round(<242 characters>) = %+v, want %s with its appointment", slices, bead)
	}
	other := createBead(t, "round id one too long")
	requireRefused(t, other, []string{"--slice", other, "--round", longest + "x", "--tier", "standard"})
}

// TestHelpListsRegisteredCommandsInDesignOrder checks the help surface: help
// names appoint; a name no command registers is a usage error.
func TestHelpListsRegisteredCommandsInDesignOrder(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help exited %d", code)
	}
	if !strings.Contains(stdout.String(), "appoint") {
		t.Fatalf("help does not list appoint:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "nonesuch") {
		t.Fatalf("help lists the unregistered nonesuch:\n%s", stdout.String())
	}
	if code := run([]string{"nonesuch"}, &stdout, &stderr); code != 2 {
		t.Fatalf("unregistered command exited %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage") && !strings.Contains(stderr.String(), "commands") {
		t.Fatalf("usage did not go to stderr:\n%s", stderr.String())
	}
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("no-args exited %d, want 2", code)
	}
}

// TestConstructors checks the composition root's constructors: newJudge
// fails closed without the key, openRepo errors outside a repository.
func TestConstructors(t *testing.T) {
	t.Setenv("LLMKIT_TYPESAFE_API_KEY", "")
	if _, err := newJudge(); err == nil {
		t.Fatal("newJudge must fail closed when LLMKIT_TYPESAFE_API_KEY is unset")
	}
	t.Setenv("LLMKIT_TYPESAFE_API_KEY", "test-key")
	if _, err := newJudge(); err != nil {
		t.Fatalf("newJudge: %v", err)
	}
	sb, backend := newSandbox()
	if sb == nil || backend != "host" {
		t.Fatalf("newSandbox = %v, %q", sb, backend)
	}
	t.Chdir(t.TempDir()) // outside any repository
	if _, err := openRepo(); err == nil {
		t.Fatal("openRepo outside a repository must error")
	}
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	if _, err := openEvidence(); err != nil {
		t.Fatalf("openEvidence: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "auspex", "evidence")); err != nil {
		t.Fatalf("evidence dir not created: %v", err)
	}
}

// TestProsperaAndObnuntiatio pins the headline helpers' shapes.
func TestProsperaAndObnuntiatio(t *testing.T) {
	var stdout bytes.Buffer
	prospera(&stdout, "cp2 holds for round %s (%d slices, %d records)", "r-1", 5, 14)
	want := "auspicia prospera: cp2 holds for round r-1 (5 slices, 14 records)\n"
	if stdout.String() != want {
		t.Fatalf("prospera wrote %q, want %q", stdout.String(), want)
	}
	var stderr bytes.Buffer
	if code := obnuntiatio(&stderr, "cp2 fails for round r-1", []string{
		"slice s1: seat B has no APPROVE bound to abc",
	}); code != 1 {
		t.Fatalf("obnuntiatio returned %d, want 1", code)
	}
	wantErr := "obnuntiatio: cp2 fails for round r-1\n  slice s1: seat B has no APPROVE bound to abc\n"
	if stderr.String() != wantErr {
		t.Fatalf("obnuntiatio wrote %q, want %q", stderr.String(), wantErr)
	}
}
