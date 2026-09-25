package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dpoage/auspex/internal/ledger"
)

// appointedBead creates a bead and appoints it to round, so heed and decree
// (which refuse an unappointed bead) can record on it. It appoints through
// a ledger with a real clock: gitBDEnv's own returned ledger has a nil
// clock (the commands use their own openLedger() instead, so gitBDEnv's
// ledger only needs Get/Round, not Append).
func appointedBead(t *testing.T, led *ledger.Ledger, round, title string) string {
	t.Helper()
	bead := createBead(t, title)
	writer := ledger.New(".", time.Now)
	if _, err := writer.Append(ledger.SliceID(bead), ledger.Appointment{Round: ledger.RoundID(round), Tier: ledger.Heavy}); err != nil {
		t.Fatalf("appointing %s: %v", bead, err)
	}
	return bead
}

func writeTranscript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// xMatrixApprove is an APPROVE payload whose matrix is local://oracle-x.md.
const xMatrixApprove = `{"verdict":"VERDICT: APPROVE","coverage":"1 family, 2 probes, 0 skipped — local://oracle-x.md","matrix":"local://oracle-x.md","scope":[],"reply":"VERDICT: APPROVE"}`

// TestHeedMatrixExistence is V2: APPROVE with no matrix, or a matrix path
// that does not exist after resolution (local:// against --local; local://
// without --local is an error), is NotAVerdict; heed records nothing and
// exits nonzero. Mutant: skip the existence check.
func TestHeedMatrixExistence(t *testing.T) {
	t.Run("no matrix at all", func(t *testing.T) {
		led, dir := gitBDEnv(t)
		bead := appointedBead(t, led, "r-1", "heed no matrix")
		transcript := writeTranscript(t, dir, "t1.json", `{"verdict":"VERDICT: APPROVE","coverage":"1 family, 2 probes, 0 skipped","matrix":"","scope":[],"reply":"VERDICT: APPROVE"}`)
		var stdout, stderr bytes.Buffer
		code := runHeed([]string{
			"--slice", bead, "--seat", "A", "--hash", "HEAD", "--model", "m", transcript,
		}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("exit = %d, want 1: stderr %s", code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "obnuntiatio") {
			t.Fatalf("stderr = %q, want obnuntiatio", stderr.String())
		}
		slices, err := led.Round("r-1")
		if err != nil {
			t.Fatal(err)
		}
		requireNoVerdictRecorded(t, slices, bead)
	})

	t.Run("local:// matrix without --local is an error", func(t *testing.T) {
		led, dir := gitBDEnv(t)
		bead := appointedBead(t, led, "r-1", "heed local no flag")
		transcript := writeTranscript(t, dir, "t2.json", xMatrixApprove)
		var stdout, stderr bytes.Buffer
		code := runHeed([]string{
			"--slice", bead, "--seat", "A", "--hash", "HEAD", "--model", "m", transcript,
		}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("exit = %d, want 1: stderr %s", code, stderr.String())
		}
		slices, err := led.Round("r-1")
		if err != nil {
			t.Fatal(err)
		}
		requireNoVerdictRecorded(t, slices, bead)
	})

	t.Run("local:// matrix that does not exist under --local", func(t *testing.T) {
		led, dir := gitBDEnv(t)
		bead := appointedBead(t, led, "r-1", "heed local missing file")
		transcript := writeTranscript(t, dir, "t3.json", xMatrixApprove)
		localDir := t.TempDir() // exists, but oracle-x.md does not
		var stdout, stderr bytes.Buffer
		code := runHeed([]string{
			"--slice", bead, "--seat", "A", "--hash", "HEAD", "--model", "m", "--local", localDir, transcript,
		}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("exit = %d, want 1: stderr %s", code, stderr.String())
		}
		slices, err := led.Round("r-1")
		if err != nil {
			t.Fatal(err)
		}
		requireNoVerdictRecorded(t, slices, bead)
	})

	t.Run("local:// matrix that exists under --local succeeds", func(t *testing.T) {
		led, dir := gitBDEnv(t)
		bead := appointedBead(t, led, "r-1", "heed local present")
		transcript := writeTranscript(t, dir, "t4.json", xMatrixApprove)
		localDir := t.TempDir()
		writeTranscript(t, localDir, "oracle-x.md", "matrix content")
		var stdout, stderr bytes.Buffer
		code := runHeed([]string{
			"--slice", bead, "--seat", "A", "--hash", "HEAD", "--model", "m", "--local", localDir, transcript,
		}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit = %d, want 0: stderr %s", code, stderr.String())
		}
		slices, err := led.Round("r-1")
		if err != nil {
			t.Fatal(err)
		}
		v := onlyVerdict(t, slices, bead)
		if !v.Approve {
			t.Fatalf("Approve = false, want true")
		}
	})

	t.Run("REJECT with no matrix is recorded", func(t *testing.T) {
		led, dir := gitBDEnv(t)
		bead := appointedBead(t, led, "r-1", "heed reject no matrix")
		transcript := writeTranscript(t, dir, "t5.json", `{"verdict":"VERDICT: REJECT","coverage":"1 family, 2 probes, 0 skipped","matrix":"","scope":[],"reply":"VERDICT: REJECT"}`)
		var stdout, stderr bytes.Buffer
		code := runHeed([]string{
			"--slice", bead, "--seat", "A", "--hash", "HEAD", "--model", "m", transcript,
		}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit = %d, want 0: stderr %s", code, stderr.String())
		}
		slices, err := led.Round("r-1")
		if err != nil {
			t.Fatal(err)
		}
		v := onlyVerdict(t, slices, bead)
		if v.Approve {
			t.Fatalf("Approve = true, want false")
		}
	})
}

// TestHeedStoresTranscriptHashAndScopeIDs is V6: heed stores the transcript
// as evidence, records Hash as the full hash, and prints each scope item
// id. Mutant: store the hash as typed (an abbreviated --hash would then be
// recorded unresolved).
func TestHeedStoresTranscriptHashAndScopeIDs(t *testing.T) {
	led, dir := gitBDEnv(t)
	bead := appointedBead(t, led, "r-1", "heed scope ids")
	full := string(fullHead(t))
	abbrev := full[:9]
	content := `{"verdict":"VERDICT: REJECT","coverage":"1 family, 2 probes, 0 skipped","matrix":"","scope":[{"id":"S1","text":"first finding"},{"id":"S2","text":"second finding"}],"reply":"VERDICT: REJECT"}`
	transcript := writeTranscript(t, dir, "t.json", content)

	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	var stdout, stderr bytes.Buffer
	code := runHeed([]string{
		"--slice", bead, "--seat", "B", "--hash", abbrev, "--model", "m", transcript,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0: stderr %s", code, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("stdout = %q, want a record id line plus 2 scope id lines", stdout.String())
	}
	recordID := lines[0]
	if lines[1] != recordID+"#S1" || lines[2] != recordID+"#S2" {
		t.Fatalf("scope id lines = %v, want %s#S1 and %s#S2", lines[1:], recordID, recordID)
	}

	slices, err := led.Round("r-1")
	if err != nil {
		t.Fatal(err)
	}
	v := onlyVerdict(t, slices, bead)
	if string(v.Hash) != full {
		t.Fatalf("Hash = %q, want the full hash %q (not the abbreviation %q)", v.Hash, full, abbrev)
	}

	store, err := openEvidence()
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(v.Transcript)
	if err != nil {
		t.Fatalf("Get(%s): %v", v.Transcript, err)
	}
	if string(stored) != content {
		t.Fatalf("stored transcript = %q, want %q", stored, content)
	}
}

// requireNoVerdictRecorded fails the test if bead carries any Verdict
// record in slices.
func requireNoVerdictRecorded(t *testing.T, slices []ledger.Slice, bead string) {
	t.Helper()
	for _, s := range slices {
		if string(s.ID) != bead {
			continue
		}
		for _, rec := range s.Records {
			if _, ok := rec.Body.(ledger.Verdict); ok {
				t.Fatalf("a refused heed recorded %+v", rec)
			}
		}
	}
}

// onlyVerdict returns the single Verdict record on bead in slices, failing
// the test if there is not exactly one.
func onlyVerdict(t *testing.T, slices []ledger.Slice, bead string) ledger.Verdict {
	t.Helper()
	var found []ledger.Verdict
	for _, s := range slices {
		if string(s.ID) != bead {
			continue
		}
		for _, rec := range s.Records {
			if v, ok := rec.Body.(ledger.Verdict); ok {
				found = append(found, v)
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("bead %s has %d Verdict records, want 1: %+v", bead, len(found), found)
	}
	return found[0]
}
