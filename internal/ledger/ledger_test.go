package ledger

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dpoage/auspex/internal/evidence"
	"github.com/dpoage/auspex/internal/repo"
)

// --- harness ---------------------------------------------------------------

// requireBD fails the test when bd is missing; the gate never skips.
func requireBD(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Fatalf("bd is required: %v", err)
	}
}

// clock hands the ledger an injected clock so records carry known times.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock { return &clock{now: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Minute)
	return c.now
}

// bdEnv builds a scratch bd database and returns the ledger over it plus the
// directory.
func bdEnv(t *testing.T) (*Ledger, string, *clock) {
	t.Helper()
	scratchEnv(t)
	return bdDB(t)
}

// scratchEnv clears BEADS_DIR and points XDG_STATE_HOME at scratch. A test
// whose subtests run in parallel calls it once, then bdDB per subtest.
func scratchEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BEADS_DIR", "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// bdDB builds a scratch bd database and returns the ledger over it plus the
// directory.
func bdDB(t *testing.T) (*Ledger, string, *clock) {
	t.Helper()
	requireBD(t)
	dir := t.TempDir()
	initBD(t, dir)
	c := newClock()
	return New(dir, c.Now), dir, c
}

func initBD(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("bd", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bd init: %v: %s", err, out)
	}
}

// bdRun runs one bd command in dir and fails the test on error.
func bdRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("bd", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// createBead creates one bead and returns its full id.
func createBead(t *testing.T, dir, title string) string {
	t.Helper()
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(bdRun(t, dir, "create", "-t", "task", title, "--json")), &created); err != nil {
		t.Fatalf("bd create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("bd create returned no id")
	}
	return created.ID
}

// label adds a slice label, the way appoint does.
func label(t *testing.T, dir, bead, round string) {
	t.Helper()
	bdRun(t, dir, "label", "add", bead, "auspex-slice:"+round)
}

// close closes a bead.
func closeBead(t *testing.T, dir, bead string) {
	t.Helper()
	bdRun(t, dir, "close", bead)
}

// shim installs a fake bd first on PATH. failArgs: the shim fails with exit
// status 1 when the arguments begin with exactly this prefix; everything
// else passes through to the real bd.
func shim(t *testing.T, failArgs ...string) {
	t.Helper()
	real, err := exec.LookPath("bd")
	if err != nil {
		t.Fatalf("bd is required: %v", err)
	}
	dir := t.TempDir()
	var cond string
	for i, a := range failArgs {
		if i > 0 {
			cond += " && "
		}
		cond += fmt.Sprintf("[ \"$%d\" = %q ]", i+1, a)
	}
	script := fmt.Sprintf("#!/bin/sh\nif %s; then echo \"bd shim: %s refused\" >&2; exit 1; fi\nexec %s \"$@\"\n",
		cond, strings.Join(failArgs, " "), real)
	path := filepath.Join(dir, "bd")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// commentText returns every comment text on a bead, in order.
func commentTexts(t *testing.T, dir, bead string) []string {
	var raw []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(bdRun(t, dir, "comments", bead, "--json")), &raw); err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, c := range raw {
		texts = append(texts, c.Text)
	}
	return texts
}

func auspexCommentCount(t *testing.T, dir, bead string) int {
	t.Helper()
	n := 0
	for _, txt := range commentTexts(t, dir, bead) {
		if strings.HasPrefix(txt, commentPrefix) {
			n++
		}
	}
	return n
}

// sampleLeg returns a Leg with every field non-zero (exit codes included,
// though a real green leg exits 0).
func sampleLeg() Leg {
	run := func(want, got Outcome, exit int, executed int, counted bool) LegRun {
		return LegRun{
			Cmd: []string{"gleam", "test"}, Want: want, Got: got,
			ExitCode: exit, KillReason: "timeout 300s", Executed: executed,
			Counted: counted, Transcript: evidence.Ref("aa11"), Duration: 90 * time.Second,
		}
	}
	return Leg{
		Base: repo.Hash(strings.Repeat("a", 40)), Head: repo.Hash(strings.Repeat("b", 40)),
		Mutant: evidence.Ref(strings.Repeat("c", 64)), Tests: []string{"test/mutant_test.gleam"},
		Backend: "host",
		A:       run(Green, Green, 3, 569, true),
		B:       run(Red, Red, 1, 12, true),
		C:       run(Green, Green, 4, 570, true),
	}
}

func sampleVerdict() Verdict {
	return Verdict{
		Seat: SeatComposition, Hash: repo.Hash(strings.Repeat("d", 40)),
		Model: "jev-latest", Approve: true, Coverage: "34 files", Matrix: "m.md",
		Scope:      []ScopeItem{{ID: "S1", Text: "the fix"}, {ID: "S2", Text: "the nit"}},
		Transcript: evidence.Ref(strings.Repeat("e", 64)),
	}
}

func sampleLint() Lint {
	return Lint{
		Kind: Blocker, Input: evidence.Ref(strings.Repeat("f", 64)), Model: "jev-latest",
		Answers: []Answer{{
			Lint: "blocker-class", Question: "Which class is this blocker?",
			P: 0.91, Choice: "PRODUCT", Confidence: 0.93, Threshold: 0.8, Flagged: true,
		}},
	}
}

// --- L1 --------------------------------------------------------------------

// TestLedgerAppendGetRoundTripAllKinds is L1: every Body kind with every field
// non-zero round-trips through Append then Get and Round, with the same ID
// and At.
func TestLedgerAppendGetRoundTripAllKinds(t *testing.T) {
	led, dir, _ := bdEnv(t)
	bead := createBead(t, dir, "round-trip slice")
	other := createBead(t, dir, "contract consumer")
	label(t, dir, bead, "r-1")
	label(t, dir, other, "r-1")

	type named struct {
		name string
		body Body
		want Body // expected after Append (ids resolved)
	}

	var fullID string
	if rec, err := led.Append(SliceID(bead), Appointment{Round: "r-1", Tier: Heavy, MergeHash: repo.Hash(strings.Repeat("1", 40))}); err != nil {
		t.Fatal(err)
	} else {
		fullID = string(rec.Slice)
	}

	cases := []named{
		{"appointment", Appointment{Round: "r-1", Tier: Standard, MergeHash: repo.Hash(strings.Repeat("2", 40))}, nil},
		{"leg", sampleLeg(), nil},
		{"verdict", sampleVerdict(), nil},
		{"ruling-scope", Ruling{Kind: Scope, Item: fullID + "#S1", Text: "the fix is in scope"}, nil},
		{"ruling-triage", Ruling{Kind: Triage, Text: "loop cap", Seat: SeatA}, nil},
		{"ruling-deviation", Ruling{Kind: Deviation, Text: "one-line deviation"}, nil},
		{"ruling-contract", Ruling{Kind: Contract, Text: "seam revised", To: []SliceID{SliceID(other)}}, nil},
		{"lint", sampleLint(), nil},
	}
	// A contract ruling's To carries the full bead id bd resolved.
	cases[6].want = Ruling{Kind: Contract, Text: "seam revised", To: []SliceID{SliceID(other)}}

	ids := make(map[string]Record, len(cases))
	for _, c := range cases {
		rec, err := led.Append(SliceID(bead), c.body)
		if err != nil {
			t.Fatalf("Append(%s): %v", c.name, err)
		}
		if rec.ID == "" || rec.At.IsZero() {
			t.Fatalf("Append(%s) left ID or At unset", c.name)
		}
		if rec.Slice != SliceID(fullID) {
			t.Fatalf("Append(%s): Slice = %q, want the full bead id %q", c.name, rec.Slice, fullID)
		}
		ids[c.name] = rec
		want := c.want
		if want == nil {
			want = c.body
		}
		got, err := led.Get(rec.ID)
		if err != nil {
			t.Fatalf("Get(%s): %v", c.name, err)
		}
		if got.ID != rec.ID || !got.At.Equal(rec.At) {
			t.Fatalf("Get(%s): ID/At = %s/%v, want %s/%v", c.name, got.ID, got.At, rec.ID, rec.At)
		}
		if !equalBody(got.Body, want) {
			t.Fatalf("Get(%s) body =\n%#v\nwant\n%#v", c.name, got.Body, want)
		}
	}

	slices, err := led.Round("r-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(slices) < 1 {
		t.Fatal("Round returned no slices")
	}
	byID := make(map[SliceID]Slice, len(slices))
	for _, s := range slices {
		byID[s.ID] = s
	}
	got := byID[SliceID(fullID)]
	if len(got.Records) != len(cases)+1 { // +1: the appointment above
		t.Fatalf("Round returned %d records, want %d", len(got.Records), len(cases)+1)
	}
	// Round returns every appended record with the body it was appended with.
	for _, c := range cases {
		found := false
		for _, rec := range got.Records {
			if rec.ID == ids[c.name].ID {
				found = true
				want := c.want
				if want == nil {
					want = c.body
				}
				if !equalBody(rec.Body, want) {
					t.Fatalf("Round(%s) body =\n%#v\nwant\n%#v", c.name, rec.Body, want)
				}
				if !rec.At.Equal(ids[c.name].At) {
					t.Fatalf("Round(%s) At = %v, want %v", c.name, rec.At, ids[c.name].At)
				}
			}
		}
		if !found {
			t.Fatalf("Round is missing record %s (%s)", ids[c.name].ID, c.name)
		}
	}
}

func bodyName(b Body) string {
	switch v := b.(type) {
	case Appointment:
		return "appointment"
	case Leg:
		return "leg"
	case Verdict:
		return "verdict"
	case Ruling:
		return fmt.Sprintf("ruling-%s", v.Kind)
	case Lint:
		return "lint"
	}
	return "?"
}

func equalBody(a, b Body) bool {
	switch x := a.(type) {
	case Appointment:
		y, ok := b.(Appointment)
		return ok && x == y
	case Leg:
		y, ok := b.(Leg)
		return ok && legsEqual(x, y)
	case Verdict:
		y, ok := b.(Verdict)
		return ok && verdictsEqual(x, y)
	case Ruling:
		y, ok := b.(Ruling)
		return ok && rulingsEqual(x, y)
	case Lint:
		y, ok := b.(Lint)
		return ok && lintsEqual(x, y)
	}
	return false
}

func legsEqual(a, b Leg) bool {
	return a.Base == b.Base && a.Head == b.Head && a.Mutant == b.Mutant &&
		strings.Join(a.Tests, ",") == strings.Join(b.Tests, ",") && a.Backend == b.Backend &&
		legRunsEqual(a.A, b.A) && legRunsEqual(a.B, b.B) && legRunsEqual(a.C, b.C)
}

func legRunsEqual(a, b LegRun) bool {
	return strings.Join(a.Cmd, ",") == strings.Join(b.Cmd, ",") && a.Want == b.Want && a.Got == b.Got &&
		a.ExitCode == b.ExitCode && a.KillReason == b.KillReason && a.Executed == b.Executed &&
		a.Counted == b.Counted && a.Transcript == b.Transcript && a.Duration == b.Duration
}

func verdictsEqual(a, b Verdict) bool {
	if a.Seat != b.Seat || a.Hash != b.Hash || a.Model != b.Model || a.Approve != b.Approve ||
		a.Coverage != b.Coverage || a.Matrix != b.Matrix || a.Transcript != b.Transcript ||
		len(a.Scope) != len(b.Scope) {
		return false
	}
	for i := range a.Scope {
		if a.Scope[i] != b.Scope[i] {
			return false
		}
	}
	return true
}

func rulingsEqual(a, b Ruling) bool {
	if a.Kind != b.Kind || a.Item != b.Item || a.Text != b.Text || a.Seat != b.Seat || len(a.To) != len(b.To) {
		return false
	}
	for i := range a.To {
		if a.To[i] != b.To[i] {
			return false
		}
	}
	return true
}

func lintsEqual(a, b Lint) bool {
	if a.Kind != b.Kind || a.Input != b.Input || a.Model != b.Model || len(a.Answers) != len(b.Answers) {
		return false
	}
	for i := range a.Answers {
		if a.Answers[i] != b.Answers[i] {
			return false
		}
	}
	return true
}

// --- L2 --------------------------------------------------------------------

// TestLedgerConcurrentAppends is L2: 16 concurrent Appends on one slice,
// repeated in 5 trials, Round returns 16 records every trial.
func TestLedgerConcurrentAppends(t *testing.T) {
	for trial := 1; trial <= 5; trial++ {
		t.Run(fmt.Sprintf("trial%d", trial), func(t *testing.T) {
			led, dir, _ := bdEnv(t)
			bead := createBead(t, dir, fmt.Sprintf("concurrent %d", trial))
			// Label like appoint does; the 16 concurrent Appends are then the
			// slice's only records.
			label(t, dir, bead, "r-2")
			var wg sync.WaitGroup
			errs := make([]error, 16)
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					leg := sampleLeg()
					leg.Backend = fmt.Sprintf("host-%d", i)
					_, err := led.Append(SliceID(bead), leg)
					errs[i] = err
				}(i)
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("concurrent Append %d: %v", i, err)
				}
			}
			slices, err := led.Round("r-2")
			if err != nil {
				t.Fatal(err)
			}
			var records int
			for _, s := range slices {
				if s.ID == SliceID(bead) {
					records = len(s.Records)
				}
			}
			if records != 16 {
				t.Fatalf("trial %d: slice %s has %d records, want 16", trial, bead, records)
			}
		})
	}
}

// --- L3 --------------------------------------------------------------------

// TestLedgerCorruptCommentFails is L3: a prefixed comment with bad JSON, an
// unknown kind, or an unknown enum value makes Round and Get error (even
// for a valid record on the same bead).
func TestLedgerCorruptCommentFails(t *testing.T) {
	cases := []struct{ name, comment string }{
		{"bad json", commentPrefix + `{not json`},
		{"unknown kind", commentPrefix + `{"id":"x/aaaaaaaaaaaa","kind":"gossip","at":"2026-09-24T00:00:00Z"}`},
		{"missing id", commentPrefix + `{"kind":"leg","at":"2026-09-24T00:00:00Z"}`},
		{"unknown outcome", commentPrefix + `{"id":"x/aaaaaaaaaaaa","kind":"leg","at":"2026-09-24T00:00:00Z","a":{"got":"bogus","counted":true}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			led, dir, _ := bdEnv(t)
			bead := createBead(t, dir, "corrupt "+tc.name)
			label(t, dir, bead, "r-3")
			valid, err := led.Append(SliceID(bead), sampleLeg())
			if err != nil {
				t.Fatal(err)
			}
			bdRun(t, dir, "comments", "add", bead, tc.comment)
			if _, err := led.Round("r-3"); err == nil {
				t.Fatal("Round returned no error on an unreadable auspex comment")
			}
			if _, err := led.Get(valid.ID); err == nil {
				t.Fatal("Get of the valid record returned no error beside an unreadable auspex comment")
			}
		})
	}
}

// --- L4 --------------------------------------------------------------------

// TestLedgerIgnoresUnprefixedComments is L4: comments without the prefix are
// ignored.
func TestLedgerIgnoresUnprefixedComments(t *testing.T) {
	led, dir, _ := bdEnv(t)
	bead := createBead(t, dir, "noisy slice")
	label(t, dir, bead, "r-4")
	bdRun(t, dir, "comments", "add", bead, "a human note about the loop cap")
	bdRun(t, dir, "comments", "add", bead, "auspex:v2 {\"future\":true}")
	rec, err := led.Append(SliceID(bead), sampleLeg())
	if err != nil {
		t.Fatal(err)
	}
	slices, err := led.Round("r-4")
	if err != nil {
		t.Fatalf("Round must ignore comments without the prefix: %v", err)
	}
	for _, s := range slices {
		if s.ID == SliceID(bead) && len(s.Records) != 1 {
			t.Fatalf("Round returned %d records, want exactly the 1 auspex record", len(s.Records))
		}
	}
	if _, err := led.Get(rec.ID); err != nil {
		t.Fatalf("Get: %v", err)
	}
}

// --- L5 --------------------------------------------------------------------

// TestLedgerRoundSliceAssembly is L5: the latest appointment by At wins; a
// labeled bead with no records appears with Appointment nil; a closed
// labeled bead is still listed.
func TestLedgerRoundSliceAssembly(t *testing.T) {
	led, dir, _ := bdEnv(t)
	two := createBead(t, dir, "two appointments")
	empty := createBead(t, dir, "labeled, no records")
	shut := createBead(t, dir, "closed slice")
	for _, bead := range []string{two, empty, shut} {
		label(t, dir, bead, "r-5")
	}
	closeBead(t, dir, shut)

	first, err := led.Append(SliceID(two), Appointment{Round: "r-5", Tier: Light})
	if err != nil {
		t.Fatal(err)
	}
	second, err := led.Append(SliceID(two), Appointment{Round: "r-5", Tier: Heavy, MergeHash: repo.Hash(strings.Repeat("5", 40))})
	if err != nil {
		t.Fatal(err)
	}
	if !second.At.After(first.At) {
		t.Fatalf("test clock did not advance: %v then %v", first.At, second.At)
	}
	if _, err := led.Append(SliceID(two), sampleLeg()); err != nil {
		t.Fatal(err)
	}

	slices, err := led.Round("r-5")
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[SliceID]Slice, len(slices))
	for _, s := range slices {
		byID[s.ID] = s
	}
	if len(slices) != 3 {
		t.Fatalf("Round returned %d slices, want 3 (%v)", len(slices), slices)
	}
	latest := byID[SliceID(two)].Appointment
	if latest == nil || latest.Tier != Heavy {
		t.Fatalf("Appointment is not the latest by At: %+v", byID[SliceID(two)].Appointment)
	}
	if got := byID[SliceID(empty)]; len(got.Records) != 0 || got.Appointment != nil {
		t.Fatalf("labeled bead with no records: %+v", got)
	}
	if _, ok := byID[SliceID(shut)]; !ok {
		t.Fatal("the closed labeled bead is not listed")
	}
}

// --- L6 --------------------------------------------------------------------

// TestLedgerBDFailureErrors is L6: a failing bd makes Append/Round/Get error.
func TestLedgerBDFailureErrors(t *testing.T) {
	t.Run("comments add fails", func(t *testing.T) {
		led, dir, _ := bdEnv(t)
		bead := createBead(t, dir, "failing writes")
		label(t, dir, bead, "r-6")
		shim(t, "comments", "add")
		if _, err := led.Append(SliceID(bead), sampleLeg()); err == nil {
			t.Fatal("Append returned no error while bd comments add failed")
		}
		if n := auspexCommentCount(t, dir, bead); n != 0 {
			t.Fatalf("a failing bd left %d auspex comments behind", n)
		}
	})
	t.Run("comments read fails", func(t *testing.T) {
		led, dir, _ := bdEnv(t)
		bead := createBead(t, dir, "failing reads")
		label(t, dir, bead, "r-6")
		rec, err := led.Append(SliceID(bead), sampleLeg())
		if err != nil {
			t.Fatal(err)
		}
		shim(t, "comments", bead)
		if _, err := led.Round("r-6"); err == nil {
			t.Fatal("Round returned no error while bd comments failed")
		}
		if _, err := led.Get(rec.ID); err == nil {
			t.Fatal("Get returned no error while bd comments failed")
		}
	})
	t.Run("error carries its cause", func(t *testing.T) {
		led, _, _ := bdEnv(t)
		// bd prints nothing on stderr for a missing bead; the exec error
		// must still reach the caller.
		_, err := led.Get("nosuchbead-xyz/aaaaaaaaaaaa")
		if err == nil || !strings.Contains(err.Error(), "exit status 1") {
			t.Fatalf("Get on a missing bead: %v, want an error naming bd's exit status", err)
		}
	})
}

// --- L7 --------------------------------------------------------------------

// TestLegFaults is L7: Leg.Faults is empty iff the seven conditions hold;
// each violated condition yields exactly one line. A red leg (b) without a
// count proves nothing about the test, so it is a fault (mutant: drop the
// B.Counted clause); a killed leg (b) names its kill reason (mutant: leave
// the reason out).
func TestLegFaults(t *testing.T) {
	healthy := sampleLeg()
	if faults := healthy.Faults(); len(faults) != 0 {
		t.Fatalf("a healthy leg has faults: %v", faults)
	}
	cases := []struct {
		name   string
		mutate func(*Leg)
	}{
		{"a not green", func(l *Leg) { l.A.Got = Red }},
		{"b not red", func(l *Leg) { l.B.Got = Green }},
		{"b killed", func(l *Leg) { l.B.Got = Killed }},
		{"b not counted", func(l *Leg) { l.B.Counted = false }},
		{"c not green", func(l *Leg) { l.C.Got = Red }},
		{"a not counted", func(l *Leg) { l.A.Counted = false }},
		{"c not counted", func(l *Leg) { l.C.Counted = false }},
		{"c count not higher", func(l *Leg) { l.C.Executed = l.A.Executed }},
		{"c count lower", func(l *Leg) { l.C.Executed = l.A.Executed - 1 }},
		{"everything wrong", func(l *Leg) {
			l.A.Got, l.B.Got, l.C.Got = Red, Green, Red
			l.A.Counted, l.B.Counted, l.C.Counted = false, false, false
			l.C.Executed = l.A.Executed
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := healthy
			tc.mutate(&l)
			faults := l.Faults()
			if len(faults) == 0 {
				t.Fatal("Faults is empty on a violated condition")
			}
			for _, f := range faults {
				if !strings.HasPrefix(f, "leg (") {
					t.Fatalf("fault %q is not a plain sentence about a leg", f)
				}
			}
			if tc.name == "everything wrong" && len(faults) != 7 {
				t.Fatalf("got %d fault lines, want one per violated condition (7): %v", len(faults), faults)
			}
			if tc.name != "everything wrong" && len(faults) != 1 {
				t.Fatalf("got %d fault lines, want exactly 1: %v", len(faults), faults)
			}
		})
	}

	killed := healthy
	killed.B.Got = Killed
	if faults := killed.Faults(); len(faults) != 1 || !strings.Contains(faults[0], killed.B.KillReason) {
		t.Fatalf("killed leg (b) faults = %v, want one line naming %q", faults, killed.B.KillReason)
	}
}

// TestRecordEvidenceListsEveryRefByRole pins Evidence: each body kind's roles
// in a fixed order, an empty ref like a set one, and every evidence.Ref field
// of the body exactly once. cp2 and read take the refs they verify from
// Evidence, so a ref field it leaves out is one neither verifies. Mutant:
// drop leg (b) from Evidence.
func TestRecordEvidenceListsEveryRefByRole(t *testing.T) {
	for _, tc := range []struct {
		body  Body
		roles []string
	}{
		{Appointment{}, nil},
		{Leg{}, []string{"mutant", "leg a", "leg b", "leg c"}},
		{Verdict{}, []string{"transcript"}},
		{Ruling{}, nil},
		{Lint{}, []string{"lint input"}},
	} {
		t.Run(KindOf(tc.body), func(t *testing.T) {
			var roles []string
			for _, e := range (Record{Body: tc.body}).Evidence() {
				if e.Ref != "" {
					t.Errorf("role %s of a zero body has ref %q", e.Role, e.Ref)
				}
				roles = append(roles, e.Role)
			}
			if fmt.Sprint(roles) != fmt.Sprint(tc.roles) {
				t.Errorf("roles of an empty %s = %q, want %q", KindOf(tc.body), roles, tc.roles)
			}

			v := reflect.New(reflect.TypeOf(tc.body)).Elem()
			var fields []evidence.Ref
			setRefFields(v, &fields)
			listed := map[evidence.Ref]int{}
			for _, e := range (Record{Body: v.Interface().(Body)}).Evidence() {
				listed[e.Ref]++
			}
			for _, ref := range fields {
				if listed[ref] != 1 {
					t.Errorf("evidence field set to %q is listed %d times, want once", ref, listed[ref])
				}
			}
			if len(listed) != len(fields) {
				t.Errorf("Evidence lists %d refs, the body has %d evidence fields", len(listed), len(fields))
			}
		})
	}
}

// setRefFields sets every evidence.Ref field reachable through v's struct
// fields to a distinct ref and appends each to refs.
func setRefFields(v reflect.Value, refs *[]evidence.Ref) {
	switch {
	case v.Type() == reflect.TypeOf(evidence.Ref("")):
		ref := evidence.Ref(fmt.Sprintf("ref-%d", len(*refs)))
		v.SetString(string(ref))
		*refs = append(*refs, ref)
	case v.Kind() == reflect.Struct:
		for i := range v.NumField() {
			setRefFields(v.Field(i), refs)
		}
	}
}

// --- L8 --------------------------------------------------------------------

// TestLedgerAppendGuards is L8: a non-Appointment on a bead with no
// auspex-slice:* label errors and writes nothing; an Appointment on a missing
// bead errors and writes nothing; a unique id prefix records the full bead id.
func TestLedgerAppendGuards(t *testing.T) {
	scratchEnv(t) // once: t.Setenv is not allowed in the parallel subtests
	t.Run("unappointed bead refuses records", func(t *testing.T) {
		t.Parallel()
		led, dir, _ := bdDB(t)
		bead := createBead(t, dir, "never appointed")
		if _, err := led.Append(SliceID(bead), sampleLeg()); err == nil {
			t.Fatal("Append of a Leg on an unappointed bead returned no error")
		}
		if n := auspexCommentCount(t, dir, bead); n != 0 {
			t.Fatalf("the refused Leg wrote %d comments", n)
		}
		if _, err := led.Append(SliceID(bead), sampleVerdict()); err == nil {
			t.Fatal("Append of a Verdict on an unappointed bead returned no error")
		}
		if n := auspexCommentCount(t, dir, bead); n != 0 {
			t.Fatalf("the refused Verdict wrote %d comments", n)
		}
	})
	t.Run("missing bead errors", func(t *testing.T) {
		t.Parallel()
		led, dir, _ := bdDB(t)
		if _, err := led.Append(SliceID("nosuchbead-xyz"), Appointment{Round: "r-8", Tier: Heavy}); err == nil {
			t.Fatal("Append of an Appointment on a missing bead returned no error")
		}
		out := bdRun(t, dir, "list", "--json", "--all", "-n", "0", "--label", "auspex-slice:r-8")
		if strings.TrimSpace(out) != "[]" {
			t.Fatalf("a missing-bead appoint wrote something: %s", out)
		}
	})
	t.Run("unique prefix resolves to the full id", func(t *testing.T) {
		t.Parallel()
		led, dir, _ := bdDB(t)
		bead := createBead(t, dir, "prefix resolution")
		prefix := bead[:len(bead)-2]
		rec, err := led.Append(SliceID(prefix), Appointment{Round: "r-8", Tier: Light})
		if err != nil {
			t.Fatal(err)
		}
		if rec.Slice != SliceID(bead) {
			t.Fatalf("record carries %q, want the full bead id %q", rec.Slice, bead)
		}
		if !strings.HasPrefix(string(rec.ID), bead+"/") {
			t.Fatalf("record id %q does not carry the full bead id", rec.ID)
		}
		got, err := led.Get(rec.ID)
		if err != nil {
			t.Fatalf("Get by full id: %v", err)
		}
		if got.ID != rec.ID {
			t.Fatalf("Get returned %q, want %q", got.ID, rec.ID)
		}
	})
	t.Run("contract ruling resolves its To ids", func(t *testing.T) {
		t.Parallel()
		led, dir, _ := bdDB(t)
		owner := createBead(t, dir, "contract owner")
		label(t, dir, owner, "r-8")
		// bd matches a partial id against any substring of a bead's hash, so
		// the partial id must first be shown to name the consumer alone.
		consumer, prefix := uniquePartial(t, dir, "contract consumer")
		label(t, dir, consumer, "r-8")
		rec, err := led.Append(SliceID(owner), Ruling{Kind: Contract, Text: "seam revised", To: []SliceID{SliceID(prefix)}})
		if err != nil {
			t.Fatal(err)
		}
		ruling := rec.Body.(Ruling)
		if len(ruling.To) != 1 || ruling.To[0] != SliceID(consumer) {
			t.Fatalf("To = %v, want the full bead id %q", ruling.To, consumer)
		}
	})
	t.Run("ruling field rules", func(t *testing.T) {
		t.Parallel()
		led, dir, _ := bdDB(t)
		bead := createBead(t, dir, "ruling rules")
		label(t, dir, bead, "r-8")
		if _, err := led.Append(SliceID(bead), Ruling{Kind: Triage, Text: "no seat"}); err == nil {
			t.Fatal("a triage ruling without a seat must error")
		}
		if _, err := led.Append(SliceID(bead), Ruling{Kind: Contract, Text: "no to"}); err == nil {
			t.Fatal("a contract ruling without --to must error")
		}
		if _, err := led.Append(SliceID(bead), Ruling{Kind: Scope, Item: "x#S1", Text: "with a seat", Seat: SeatA}); err == nil {
			t.Fatal("a scope ruling with a seat must error")
		}
		if n := auspexCommentCount(t, dir, bead); n != 0 {
			t.Fatalf("invalid rulings wrote %d comments", n)
		}
	})
}

// TestLedgerAppointed: Appointed is nil for an appointed bead and an error
// for a bead never appointed and for a missing bead, so observe can refuse
// before running any leg. Mutant: always nil.
func TestLedgerAppointed(t *testing.T) {
	led, dir, _ := bdEnv(t)
	appointed := createBead(t, dir, "appointed")
	if _, err := led.Append(SliceID(appointed), Appointment{Round: "r-ap", Tier: Light}); err != nil {
		t.Fatal(err)
	}
	if err := led.Appointed(SliceID(appointed)); err != nil {
		t.Fatalf("Appointed(appointed bead) = %v, want nil", err)
	}
	never := createBead(t, dir, "never appointed")
	if err := led.Appointed(SliceID(never)); err == nil {
		t.Fatal("Appointed(never-appointed bead) = nil, want an error")
	}
	if err := led.Appointed(SliceID("nosuchbead-xyz")); err == nil {
		t.Fatal("Appointed(missing bead) = nil, want an error")
	}
}

// uniquePartial creates beads titled title until one has a partial id
// (its full id less the last two characters) that bd resolves to that bead
// alone, and returns the bead's full id and the partial id.
func uniquePartial(t *testing.T, dir, title string) (string, string) {
	t.Helper()
	for range 20 {
		bead := createBead(t, dir, title)
		partial := bead[:len(bead)-2]
		cmd := exec.Command("bd", "show", partial, "--json")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			continue // ambiguous: bd exits nonzero
		}
		var shown []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(out, &shown) == nil && len(shown) == 1 && shown[0].ID == bead {
			return bead, partial
		}
	}
	t.Fatalf("no %q bead in 20 has a partial id that bd resolves to it alone", title)
	return "", ""
}

// TestLedgerAppendRefusesUnreadableBodies: Append writes nothing and errors
// for any body whose encoding would not decode back to an equal body (zero
// or out-of-range enums, values JSON cannot carry) and for an invalid round
// id; every body it accepts is readable by Round and Get.
func TestLedgerAppendRefusesUnreadableBodies(t *testing.T) {
	led, dir, _ := bdEnv(t)
	appointed := createBead(t, dir, "appointed slice")
	label(t, dir, appointed, "r-9")
	fresh := createBead(t, dir, "never appointed")
	leg := func(mutate func(*Leg)) Leg { l := sampleLeg(); mutate(&l); return l }
	verdict := func(seat Seat) Verdict { v := sampleVerdict(); v.Seat = seat; return v }
	lint := func(mutate func(*Lint)) Lint {
		l := sampleLint()
		l.Answers = append([]Answer(nil), l.Answers...)
		mutate(&l)
		return l
	}
	cases := []struct {
		name  string
		slice string
		body  Body
	}{
		{"verdict seat none", appointed, verdict(SeatNone)},
		{"verdict seat out of range", appointed, verdict(Seat(9))},
		{"appointment tier zero", fresh, Appointment{Round: "r-9", Tier: 0}},
		{"appointment tier out of range", fresh, Appointment{Round: "r-9", Tier: Tier(7)}},
		{"appointment round with comma", fresh, Appointment{Round: "a,b", Tier: Heavy}},
		{"appointment round blank", fresh, Appointment{Round: " ", Tier: Heavy}},
		{"ruling kind zero", appointed, Ruling{Kind: 0, Text: "no kind"}},
		{"ruling kind out of range", appointed, Ruling{Kind: RulingKind(9), Text: "no kind"}},
		{"lint kind zero", appointed, lint(func(l *Lint) { l.Kind = 0 })},
		{"lint kind out of range", appointed, lint(func(l *Lint) { l.Kind = LintKind(9) })},
		{"leg got out of range", appointed, leg(func(l *Leg) { l.A.Got = Outcome(7) })},
		{"leg want out of range", appointed, leg(func(l *Leg) { l.B.Want = Outcome(9) })},
		{"answer p not a number", appointed, lint(func(l *Lint) { l.Answers[0].P = math.NaN() })},
	}
	for _, tc := range cases {
		if rec, err := led.Append(SliceID(tc.slice), tc.body); err == nil {
			t.Errorf("Append(%s) = %s with no error, want an error", tc.name, rec.ID)
		}
	}
	for _, bead := range []string{appointed, fresh} {
		if n := auspexCommentCount(t, dir, bead); n != 0 {
			t.Fatalf("refused bodies wrote %d comments on %s", n, bead)
		}
	}
	if out := bdRun(t, dir, "label", "list", fresh); strings.Contains(out, "auspex-slice:") {
		t.Fatalf("a refused appointment labeled %s: %s", fresh, out)
	}
	// Empty and nil slices both read back as nil; Append accepts either.
	empty := sampleLeg()
	empty.Tests = []string{}
	rec, err := led.Append(SliceID(appointed), empty)
	if err != nil {
		t.Fatalf("Append of a leg with empty Tests: %v", err)
	}
	if _, err := led.Get(rec.ID); err != nil {
		t.Fatalf("Get after the refusals: %v", err)
	}
	if _, err := led.Round("r-9"); err != nil {
		t.Fatalf("Round after the refusals: %v", err)
	}
	if _, err := led.Round("a,b"); err == nil {
		t.Fatal("Round of an invalid round id must error")
	}
}

// TestLedgerRoundAppointmentIsPerRound: Round(R) sets Slice.Appointment to
// the latest appointment whose Round is R, even when the bead was appointed
// to another round later.
func TestLedgerRoundAppointmentIsPerRound(t *testing.T) {
	led, dir, _ := bdEnv(t)
	bead := createBead(t, dir, "appointed twice")
	first := Appointment{Round: "r-7a", Tier: Heavy, MergeHash: repo.Hash(strings.Repeat("7", 40))}
	second := Appointment{Round: "r-7b", Tier: Light}
	for _, a := range []Appointment{first, second} {
		if _, err := led.Append(SliceID(bead), a); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []Appointment{first, second} {
		slices, err := led.Round(want.Round)
		if err != nil {
			t.Fatal(err)
		}
		if len(slices) != 1 || slices[0].Appointment == nil || *slices[0].Appointment != want {
			t.Fatalf("Round(%s) = %+v, want the one slice with appointment %+v", want.Round, slices, want)
		}
	}
}

// TestLedgerLargeRecordRoundTrips: a record of up to 1 MiB round-trips
// through Append and Get.
func TestLedgerLargeRecordRoundTrips(t *testing.T) {
	led, dir, _ := bdEnv(t)
	bead := createBead(t, dir, "large verdict")
	label(t, dir, bead, "r-10")
	v := sampleVerdict()
	v.Coverage = strings.Repeat("c", 1_000_000)
	rec, err := led.Append(SliceID(bead), v)
	if err != nil {
		t.Fatalf("Append of a 1,000,000-byte Coverage: %v", err)
	}
	got, err := led.Get(rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !equalBody(got.Body, v) {
		t.Fatalf("the large verdict did not round-trip (Coverage %d bytes back)", len(got.Body.(Verdict).Coverage))
	}
}

// requireUntouched fails the test unless bead has no labels and no comments.
func requireUntouched(t *testing.T, dir, bead string) {
	t.Helper()
	var shown []struct {
		Labels       []string `json:"labels"`
		CommentCount int      `json:"comment_count"`
	}
	if err := json.Unmarshal([]byte(bdRun(t, dir, "show", bead, "--json")), &shown); err != nil || len(shown) != 1 {
		t.Fatalf("bd show %s: %v", bead, err)
	}
	if len(shown[0].Labels) != 0 || shown[0].CommentCount != 0 {
		t.Fatalf("%s holds labels %v and %d comments, want none", bead, shown[0].Labels, shown[0].CommentCount)
	}
}

// TestLedgerRoundIDLengthBound: 242 characters fills bd's 255-character
// "auspex-slice:<id>" label exactly; one more is refused by Append (writes
// nothing) and by Round.
func TestLedgerRoundIDLengthBound(t *testing.T) {
	led, dir, _ := bdEnv(t)
	longest := RoundID(strings.Repeat("r0._-", 48) + "ab")
	if len(longest) != 242 {
		t.Fatalf("fixture id has %d characters, want 242", len(longest))
	}
	bead := createBead(t, dir, "longest round id")
	want := Appointment{Round: longest, Tier: Standard}
	if _, err := led.Append(SliceID(bead), want); err != nil {
		t.Fatal(err)
	}
	slices, err := led.Round(longest)
	if err != nil {
		t.Fatal(err)
	}
	if len(slices) != 1 || string(slices[0].ID) != bead || slices[0].Appointment == nil || *slices[0].Appointment != want {
		t.Fatalf("Round(<242 characters>) = %+v, want %s with %+v", slices, bead, want)
	}
	tooLong := longest + "x"
	other := createBead(t, dir, "round id one too long")
	if rec, err := led.Append(SliceID(other), Appointment{Round: tooLong, Tier: Standard}); err == nil {
		t.Fatalf("Append of a 243-character round id = %s with no error", rec.ID)
	}
	requireUntouched(t, dir, other)
	if _, err := led.Round(tooLong); err == nil {
		t.Fatal("Round of a 243-character round id must error")
	}
}

// TestLedgerAppendStagingFailureWritesNothing: when the comment text cannot
// be staged (TMPDIR unwritable), Append of an Appointment errors with no
// label and no comment on the bead.
func TestLedgerAppendStagingFailureWritesNothing(t *testing.T) {
	led, dir, _ := bdEnv(t)
	bead := createBead(t, dir, "unwritable TMPDIR")
	tmp := t.TempDir()
	if err := os.Chmod(tmp, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	if f, err := os.CreateTemp("", "probe-*"); err == nil {
		f.Close()
		t.Fatalf("TMPDIR %s is still writable (running as root?)", tmp)
	}
	if rec, err := led.Append(SliceID(bead), Appointment{Round: "r-11", Tier: Heavy}); err == nil {
		t.Fatalf("Append with TMPDIR unwritable = %s with no error", rec.ID)
	}
	requireUntouched(t, dir, bead)
}
