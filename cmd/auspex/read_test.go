package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dpoage/auspex/internal/evidence"
	"github.com/dpoage/auspex/internal/ledger"
	"github.com/dpoage/auspex/internal/repo"
)

// TestReadPrintsAbsoluteTranscriptPath defends U2: read prints the record
// and absolute transcript paths -- never the bare ref. The golden is
// end-anchored on the store's own "/evidence/<ref>" suffix. Mutant:
// print the ref instead of the path.
func TestReadPrintsAbsoluteTranscriptPath(t *testing.T) {
	_, dir := gitBDEnv(t)
	led := ledger.New(dir, time.Now)
	bead := createBead(t, "read fixture slice")
	hash := fullHead(t)
	store, err := evidence.Open()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put([]byte("a verdict transcript for read"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := led.Append(ledger.SliceID(bead), ledger.Appointment{Round: "r-read", Tier: ledger.Light, MergeHash: hash}); err != nil {
		t.Fatal(err)
	}
	rec, err := led.Append(ledger.SliceID(bead), ledger.Verdict{Seat: ledger.SeatB, Hash: hash, Approve: true, Transcript: ref})
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runRead([]string{string(rec.ID)}, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("read exited %d: stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if lastLine := "transcript: " + string(ref) + "\n"; strings.HasSuffix(out, lastLine) {
		t.Fatalf("read printed the bare ref, not its path:\n%s", out)
	}
	wantSuffix := "/evidence/" + string(ref) + "\n"
	if !strings.HasSuffix(out, wantSuffix) {
		t.Fatalf("read output %q does not end with %q (the absolute transcript path)", out, wantSuffix)
	}
	wantPath := store.Path(ref)
	if !strings.Contains(out, wantPath) {
		t.Fatalf("read output %q does not contain the absolute path %q", out, wantPath)
	}
}

// TestReadPrintsRecordFields is a smoke test on the non-golden part of
// read's output: the record id, slice, and kind appear.
func TestReadPrintsRecordFields(t *testing.T) {
	_, dir := gitBDEnv(t)
	led := ledger.New(dir, time.Now)
	bead := createBead(t, "read fields slice")
	rec, err := led.Append(ledger.SliceID(bead), ledger.Appointment{Round: "r-fields", Tier: ledger.Heavy})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runRead([]string{string(rec.ID)}, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("read exited %d: stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{string(rec.ID), bead, "kind appointment", "round r-fields", "tier heavy"} {
		if !strings.Contains(out, want) {
			t.Errorf("read output %q missing %q", out, want)
		}
	}
}

// TestReadUnknownRecordIsObnuntiatio: an unknown record id fails closed.
func TestReadUnknownRecordIsObnuntiatio(t *testing.T) {
	gitBDEnv(t)
	bead := createBead(t, "no records slice")
	var stdout, stderr bytes.Buffer
	if code := runRead([]string{bead + "/000000000000"}, &stdout, &stderr); code != exitObnuntiatio {
		t.Fatalf("read exited %d, want 1: stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "obnuntiatio") {
		t.Fatalf("stderr = %q, want an obnuntiatio headline", stderr.String())
	}
}

// TestReadEmptyEvidenceRefIsObnuntiatio: a verdict stored with no
// transcript ref, which cp2 fails, is obnuntiatio, exit 1, naming the
// role, after the record is printed. Mutant: skip an empty ref.
func TestReadEmptyEvidenceRefIsObnuntiatio(t *testing.T) {
	_, dir := gitBDEnv(t)
	led := ledger.New(dir, time.Now)
	bead := createBead(t, "read empty ref slice")
	hash := fullHead(t)
	if _, err := led.Append(ledger.SliceID(bead), ledger.Appointment{Round: "r-read", Tier: ledger.Light, MergeHash: hash}); err != nil {
		t.Fatal(err)
	}
	rec, err := led.Append(ledger.SliceID(bead), ledger.Verdict{Seat: ledger.SeatB, Hash: hash, Approve: true})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runRead([]string{string(rec.ID)}, &stdout, &stderr); code != exitObnuntiatio {
		t.Fatalf("read exited %d, want 1: stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "kind verdict\n") {
		t.Fatalf("stdout = %q, want the record printed", stdout.String())
	}
	want := fmt.Sprintf("obnuntiatio: read: record %s references missing or corrupt evidence\n  transcript: no evidence ref stored\n", rec.ID)
	if stderr.String() != want {
		t.Fatalf("read stderr:\n%s\nwant:\n%s", stderr.String(), want)
	}
}

// TestReadGoldens pins read's output on a leg and a lint record: every
// evidence path labeled by role, each leg run's command, kill reason, and
// duration, and a lint's kind, input, confidence, and threshold. Missing or
// corrupt evidence is obnuntiatio, exit 1, after the record is printed.
// Goldens are full-output, byte-exact.
func TestReadGoldens(t *testing.T) {
	_, dir := gitBDEnv(t)
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	led := ledger.New(dir, func() time.Time { return at })
	bead := createBead(t, "read goldens slice")
	store, err := evidence.Open()
	if err != nil {
		t.Fatal(err)
	}
	put := func(s string) evidence.Ref {
		t.Helper()
		ref, err := store.Put([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	base, head := repo.Hash(strings.Repeat("0", 40)), repo.Hash(strings.Repeat("1", 40))
	if _, err := led.Append(ledger.SliceID(bead), ledger.Appointment{Round: "r-read", Tier: ledger.Light, MergeHash: head}); err != nil {
		t.Fatal(err)
	}
	cmd := []string{"go", "test", "./..."}
	leg := ledger.Leg{
		Base: base, Head: head, Tests: []string{"pkg/x_test.go"}, Backend: "host",
		Mutant: put("the mutant patch"),
		A:      ledger.LegRun{Cmd: cmd, Want: ledger.Green, Got: ledger.Green, Executed: 10, Counted: true, Transcript: put("leg a transcript"), Duration: 1500 * time.Millisecond},
		B:      ledger.LegRun{Cmd: []string{"go", "test", "./pkg/"}, Want: ledger.Red, Got: ledger.Killed, ExitCode: -1, KillReason: "timeout after 5m0s", Transcript: put("leg b transcript"), Duration: 5 * time.Minute},
		C:      ledger.LegRun{Cmd: cmd, Want: ledger.Green, Got: ledger.Green, Executed: 11, Counted: true, Transcript: put("leg c transcript"), Duration: 2 * time.Second},
	}
	legRec, err := led.Append(ledger.SliceID(bead), leg)
	if err != nil {
		t.Fatal(err)
	}
	lint := ledger.Lint{Kind: ledger.Blocker, Input: put("the blocker text"), Model: "jev", Answers: []ledger.Answer{
		{Lint: "blocker", Question: "class this blocker", P: 0.6, Choice: "unclassified", Confidence: 0.6, Threshold: 0.8, Flagged: true},
	}}
	lintRec, err := led.Append(ledger.SliceID(bead), lint)
	if err != nil {
		t.Fatal(err)
	}

	header := func(rec ledger.Record, kind string) string {
		return fmt.Sprintf("record %s\nslice %s\nat 2026-03-01T12:00:00Z\nkind %s\n", rec.ID, bead, kind)
	}
	legGolden := header(legRec, "leg") +
		"base " + string(base) + "\n" +
		"head " + string(head) + "\n" +
		"backend host\n" +
		"tests [pkg/x_test.go]\n" +
		`leg a result: want=green got=green exit=0 executed=10 counted=true duration=1.5s kill-reason="" cmd=["go" "test" "./..."]` + "\n" +
		`leg b result: want=red got=killed exit=-1 executed=0 counted=false duration=5m0s kill-reason="timeout after 5m0s" cmd=["go" "test" "./pkg/"]` + "\n" +
		`leg c result: want=green got=green exit=0 executed=11 counted=true duration=2s kill-reason="" cmd=["go" "test" "./..."]` + "\n" +
		"fault leg (b) was killed (timeout after 5m0s), not run red\n" +
		"fault leg (b) recorded no executed-test count\n" +
		"mutant: " + store.Path(leg.Mutant) + "\n" +
		"leg a: " + store.Path(leg.A.Transcript) + "\n" +
		"leg b: " + store.Path(leg.B.Transcript) + "\n" +
		"leg c: " + store.Path(leg.C.Transcript) + "\n"
	lintGolden := header(lintRec, "lint") +
		"lint-kind blocker\n" +
		"input " + string(lint.Input) + "\n" +
		"model jev\n" +
		`answer blocker: question="class this blocker" p=0.6 choice="unclassified" confidence=0.6 threshold=0.8 flagged=true` + "\n" +
		"lint input: " + store.Path(lint.Input) + "\n"

	for _, tc := range []struct {
		name   string
		rec    ledger.Record
		golden string
	}{{"leg", legRec, legGolden}, {"lint", lintRec, lintGolden}} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runRead([]string{string(tc.rec.ID)}, &stdout, &stderr); code != exitSuccess {
				t.Fatalf("read exited %d: stderr=%q", code, stderr.String())
			}
			if out := stdout.String(); out != tc.golden {
				t.Fatalf("read output:\n%s\nwant:\n%s", out, tc.golden)
			}
		})
	}

	t.Run("missing or corrupt evidence", func(t *testing.T) {
		if err := os.Remove(store.Path(leg.Mutant)); err != nil {
			t.Fatal(err)
		}
		path := store.Path(leg.B.Transcript)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body[0] ^= 0xff
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := runRead([]string{string(legRec.ID)}, &stdout, &stderr); code != exitObnuntiatio {
			t.Fatalf("read exited %d, want 1: stderr=%q", code, stderr.String())
		}
		if stdout.String() != legGolden {
			t.Fatalf("read output:\n%s\nwant:\n%s", stdout.String(), legGolden)
		}
		wantErr := fmt.Sprintf("obnuntiatio: read: record %s references missing or corrupt evidence\n", legRec.ID) +
			fmt.Sprintf("  mutant %s: evidence: no transcript stored under %s\n", leg.Mutant, leg.Mutant) +
			fmt.Sprintf("  leg b %s: evidence: stored transcript %s no longer matches its hash\n", leg.B.Transcript, leg.B.Transcript)
		if stderr.String() != wantErr {
			t.Fatalf("read stderr:\n%s\nwant:\n%s", stderr.String(), wantErr)
		}
	})
}

// TestReadUsageErrors: zero or more than one argument is exit 2.
func TestReadUsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"a", "b"}} {
		var stdout, stderr bytes.Buffer
		if code := runRead(args, &stdout, &stderr); code != exitUsage {
			t.Errorf("runRead(%v) = %d, want 2", args, code)
		}
	}
}
