package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/dpoage/auspex/internal/ledger"
	"github.com/dpoage/auspex/internal/repo"
)

// TestAuguryFixtureRound is criterion U1 (acceptance 7): augury prints
// records in time order, wall time per DESIGN.md phase, one omen line per
// flagged answer, and the non-product blocker share, on a fixture round
// built through ledger.New with an injected clock, carrying two
// appointments per slice; the first phase starts at the first
// appointment. Hiding mutants: sort by record ID; use Slice.Appointment
// (the latest) as the phase start.
func TestAuguryFixtureRound(t *testing.T) {
	_, dir := gitBDEnv(t)
	bead := createBead(t, "augury fixture slice")

	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	at := func(h float64) time.Time { return base.Add(time.Duration(h * float64(time.Hour))) }
	times := []time.Time{
		at(0),   // 0: first appointment
		at(1),   // 1: first leg record
		at(1.5), // 2: second appointment (the latest -- must NOT be used as phase start)
		at(2),   // 3: seat A reject #1
		at(3),   // 4: seat A reject #2 (2nd consecutive)
		at(3.5), // 5: seat A triage ruling
		at(5),   // 6: seat A approve (next verdict after the 2nd reject)
		at(7),   // 7: seat B's one and only (first) verdict
		at(8),   // 8: composition verdict
		at(9),   // 9: a flagged brief lint
		at(10),  // 10: a blocker lint
	}
	idx := 0
	clock := func() time.Time {
		tt := times[idx]
		idx++
		return tt
	}
	led := ledger.New(dir, clock)
	sliceID := ledger.SliceID(bead)
	hash := repo.Hash(strings.Repeat("1", 40))
	hash2 := repo.Hash(strings.Repeat("2", 40))

	mustAppend := func(b ledger.Body) {
		t.Helper()
		if _, err := led.Append(sliceID, b); err != nil {
			t.Fatalf("Append(%T): %v", b, err)
		}
	}

	mustAppend(ledger.Appointment{Round: "r-u1", Tier: ledger.Heavy, MergeHash: hash})                                       // t0
	mustAppend(ledger.Leg{Base: repo.Hash(strings.Repeat("0", 40)), Head: hash, Backend: "host", Tests: []string{"x_test"}}) // t1
	mustAppend(ledger.Appointment{Round: "r-u1", Tier: ledger.Heavy, MergeHash: hash2})                                      // t2 (latest appointment)
	mustAppend(ledger.Verdict{Seat: ledger.SeatA, Hash: hash, Approve: false})                                               // t3 reject 1
	mustAppend(ledger.Verdict{Seat: ledger.SeatA, Hash: hash, Approve: false})                                               // t4 reject 2
	mustAppend(ledger.Ruling{Kind: ledger.Triage, Item: "loop-cap", Text: "reviewed", Seat: ledger.SeatA})                   // t5
	mustAppend(ledger.Verdict{Seat: ledger.SeatA, Hash: hash, Approve: true})                                                // t6
	mustAppend(ledger.Verdict{Seat: ledger.SeatB, Hash: hash, Approve: true})                                                // t7
	mustAppend(ledger.Verdict{Seat: ledger.SeatComposition, Hash: hash, Approve: true})                                      // t8
	mustAppend(ledger.Lint{Kind: ledger.Brief, Model: "jev", Answers: []ledger.Answer{
		{Lint: "skill-routing", Question: "does the brief name a skill?", P: 0.85, Threshold: 0.8, Flagged: true},
	}}) // t9
	mustAppend(ledger.Lint{Kind: ledger.Blocker, Model: "jev", Answers: []ledger.Answer{
		{Lint: "blocker", Question: "class this blocker", Choice: "PRODUCT", Confidence: 0.9},
		{Lint: "blocker", Question: "class this blocker", Choice: "TEST_MACHINERY", Confidence: 0.9},
	}}) // t10

	var stdout, stderr bytes.Buffer
	if code := runAugury([]string{"r-u1"}, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("augury exited %d: stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	// The round has exactly 11 records; augury lists each one first, in
	// time order (never by RecordID, which carries no time signal).
	if len(lines) < 11 {
		t.Fatalf("augury printed %d lines, want at least 11 record lines:\n%s", len(lines), out)
	}
	var prev time.Time
	for i := 0; i < 11; i++ {
		fields := strings.SplitN(lines[i], " ", 2)
		if len(fields) != 2 {
			t.Fatalf("record line %d = %q, want a leading RFC3339 timestamp", i, lines[i])
		}
		ts, err := time.Parse(time.RFC3339, fields[0])
		if err != nil {
			t.Fatalf("record line %d = %q: %v", i, lines[i], err)
		}
		if i > 0 && ts.Before(prev) {
			t.Fatalf("augury did not print records in time order: line %d (%s) precedes line %d (%s)", i, ts, i-1, prev)
		}
		prev = ts
	}

	// Phases: the first ("appoint to first leg") must be measured from the
	// FIRST appointment (t0, duration 1h to the first leg at t1), never
	// from Slice.Appointment (the latest, t2, which would make it 1h -
	// 30m = 30m short, or even negative).
	wantPhases := []string{
		"phase slice " + bead + ": appoint to first leg: 1h0m0s",
		"phase slice " + bead + ": first leg to seat A's first verdict: 1h0m0s",
		"phase slice " + bead + ": seat A reject to next verdict: 1h0m0s",
		"phase slice " + bead + ": seat A reject to next verdict: 2h0m0s",
		"phase slice " + bead + ": first leg to seat B's first verdict: 6h0m0s",
		"phase slice " + bead + ": to composition verdict: 1h0m0s",
	}
	for _, w := range wantPhases {
		if !strings.Contains(out, w) {
			t.Errorf("augury output missing phase line %q:\n%s", w, out)
		}
	}

	if want := "omen: skill-routing flagged at 0.85 (threshold 0.8)"; !strings.Contains(out, want) {
		t.Errorf("augury output missing %q:\n%s", want, out)
	}
	if want := "non-product blocker share: 1/2"; !strings.Contains(out, want) {
		t.Errorf("augury output missing %q:\n%s", want, out)
	}
}

// clockAt returns a ledger clock that stamps successive records with
// times, in order.
func clockAt(t *testing.T, times ...time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		if i >= len(times) {
			t.Fatalf("clock asked for record %d, fixture has %d times", i+1, len(times))
		}
		i++
		return times[i-1]
	}
}

// TestAuguryOmenUnclassifiedBlocker: the blocker share counts only
// classified blockers; an unclassified one (confidence under 0.8, in the
// shape omen records it) prints as "unclassified at <p>", never "flagged
// at", and is counted on its own line. Hiding mutant: count unclassified
// as non-product.
func TestAuguryOmenUnclassifiedBlocker(t *testing.T) {
	blocker := func(choice string, conf float64) ledger.Answer {
		return ledger.Answer{Lint: "blocker", Question: "class this blocker", P: conf, Choice: choice, Confidence: conf, Threshold: 0.8, Flagged: conf < 0.8}
	}
	s := ledger.Slice{ID: "s1", Records: []ledger.Record{
		{ID: "s1/l1", Slice: "s1", At: time.Unix(1, 0), Body: ledger.Lint{Kind: ledger.Blocker, Answers: []ledger.Answer{blocker("unclassified", 0.6)}}},
		{ID: "s1/l2", Slice: "s1", At: time.Unix(2, 0), Body: ledger.Lint{Kind: ledger.Blocker, Answers: []ledger.Answer{blocker("PRODUCT", 0.9)}}},
		{ID: "s1/l3", Slice: "s1", At: time.Unix(3, 0), Body: ledger.Lint{Kind: ledger.Blocker, Answers: []ledger.Answer{blocker("PROSE", 0.95)}}},
	}}
	var out bytes.Buffer
	printOmenSummary(&out, []ledger.Slice{s})
	want := "omen: blocker unclassified at 0.6 (threshold 0.8)\n" +
		"non-product blocker share: 1/2\n" +
		"unclassified blockers: 1\n"
	if out.String() != want {
		t.Fatalf("printOmen =\n%s\nwant\n%s", out.String(), want)
	}
}

// TestAuguryGolden pins augury's whole output on a one-slice round: each
// timeline line ends with its record id, and a phase whose end precedes
// its start (seat B's verdict recorded before the first leg) prints
// "n/a (...)", never a negative duration. End-anchored full-output golden.
func TestAuguryGolden(t *testing.T) {
	_, dir := gitBDEnv(t)
	bead := createBead(t, "augury golden slice")
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	led := ledger.New(dir, clockAt(t, base, base.Add(30*time.Minute), base.Add(time.Hour), base.Add(2*time.Hour)))
	hash := repo.Hash(strings.Repeat("1", 40))
	var ids []ledger.RecordID
	for _, b := range []ledger.Body{
		ledger.Appointment{Round: "r-golden", Tier: ledger.Light, MergeHash: hash},
		ledger.Verdict{Seat: ledger.SeatB, Hash: hash, Approve: true},
		ledger.Leg{Base: repo.Hash(strings.Repeat("0", 40)), Head: hash, Backend: "host", Tests: []string{"x_test"}},
		ledger.Lint{Kind: ledger.Brief, Model: "jev", Answers: []ledger.Answer{
			{Lint: "skill-routing", Question: "does the brief name a skill?", P: 0.85, Threshold: 0.8, Flagged: true},
		}},
	} {
		rec, err := led.Append(ledger.SliceID(bead), b)
		if err != nil {
			t.Fatalf("Append(%T): %v", b, err)
		}
		ids = append(ids, rec.ID)
	}

	var stdout, stderr bytes.Buffer
	if code := runAugury([]string{"r-golden"}, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("augury exited %d: stderr=%q", code, stderr.String())
	}
	want := "2026-03-01T00:00:00Z slice " + bead + " appointment " + string(ids[0]) + "\n" +
		"2026-03-01T00:30:00Z slice " + bead + " verdict " + string(ids[1]) + "\n" +
		"2026-03-01T01:00:00Z slice " + bead + " leg " + string(ids[2]) + "\n" +
		"2026-03-01T02:00:00Z slice " + bead + " lint " + string(ids[3]) + "\n" +
		"phase slice " + bead + ": appoint to first leg: 1h0m0s\n" +
		"phase slice " + bead + ": first leg to seat B's first verdict: n/a (ends 30m0s before it starts)\n" +
		"omen: skill-routing flagged at 0.85 (threshold 0.8)\n"
	if out := stdout.String(); out != want {
		t.Fatalf("augury output:\n%s\nwant:\n%s", out, want)
	}
}

// TestAuguryRoundWithNoSlices: a round with no slices is obnuntiatio,
// exit 1, with nothing on stdout. End-anchored golden.
func TestAuguryRoundWithNoSlices(t *testing.T) {
	gitBDEnv(t)
	var stdout, stderr bytes.Buffer
	if code := runAugury([]string{"r-none"}, &stdout, &stderr); code != exitObnuntiatio {
		t.Fatalf("augury exited %d, want 1: stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if want := "obnuntiatio: round r-none has no slices\n"; stderr.String() != want || stdout.Len() != 0 {
		t.Fatalf("augury stdout=%q stderr=%q, want stdout empty and stderr %q", stdout.String(), stderr.String(), want)
	}
}

// TestAuguryScopesRecordsToRound: on a bead appointed to an earlier round
// and then to this one, augury lists and times only the records at or
// after the first appointment to this round; the first phase starts at
// that appointment, not the earlier round's. Hiding mutant: use all records.
func TestAuguryScopesRecordsToRound(t *testing.T) {
	_, dir := gitBDEnv(t)
	bead := createBead(t, "reused slice")
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	led := ledger.New(dir, clockAt(t, base, base.Add(time.Hour), base.Add(5*time.Hour), base.Add(7*time.Hour)))
	hash := repo.Hash(strings.Repeat("1", 40))
	leg := ledger.Leg{Base: repo.Hash(strings.Repeat("0", 40)), Head: hash, Backend: "host", Tests: []string{"x_test"}}
	var ids []ledger.RecordID
	for _, b := range []ledger.Body{
		ledger.Appointment{Round: "r-old", Tier: ledger.Light, MergeHash: hash},
		leg,
		ledger.Appointment{Round: "r-new", Tier: ledger.Light, MergeHash: hash},
		leg,
	} {
		rec, err := led.Append(ledger.SliceID(bead), b)
		if err != nil {
			t.Fatalf("Append(%T): %v", b, err)
		}
		ids = append(ids, rec.ID)
	}

	var stdout, stderr bytes.Buffer
	if code := runAugury([]string{"r-new"}, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("augury exited %d: stderr=%q", code, stderr.String())
	}
	want := "2026-03-01T05:00:00Z slice " + bead + " appointment " + string(ids[2]) + "\n" +
		"2026-03-01T07:00:00Z slice " + bead + " leg " + string(ids[3]) + "\n" +
		"phase slice " + bead + ": appoint to first leg: 2h0m0s\n"
	if out := stdout.String(); out != want {
		t.Fatalf("augury output:\n%s\nwant:\n%s", out, want)
	}
}

// TestAuguryUsageErrors: zero or more than one argument is exit 2.
func TestAuguryUsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"a", "b"}} {
		var stdout, stderr bytes.Buffer
		if code := runAugury(args, &stdout, &stderr); code != exitUsage {
			t.Errorf("runAugury(%v) = %d, want 2", args, code)
		}
	}
}
