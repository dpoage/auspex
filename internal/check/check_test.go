package check_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dpoage/auspex/internal/check"
	"github.com/dpoage/auspex/internal/evidence"
	"github.com/dpoage/auspex/internal/ledger"
	"github.com/dpoage/auspex/internal/repo"
)

const round = ledger.RoundID("r")

var (
	tAppt = t0.Add(-time.Hour)
	t0    = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1    = t0.Add(time.Hour)
	t2    = t0.Add(2 * time.Hour)
	t3    = t0.Add(3 * time.Hour)
	t4    = t0.Add(4 * time.Hour)
	t5    = t0.Add(5 * time.Hour)
	t6    = t0.Add(6 * time.Hour)
)

// fixture builds hand-made rounds whose verdicts and legs carry real
// stored evidence, so only the condition under test can fail.
type fixture struct {
	t     *testing.T
	store *evidence.Store
	n     int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s, err := evidence.Open()
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, store: s}
}

// ref stores a fresh evidence body, distinct from every other, and
// returns its ref.
func (f *fixture) ref() evidence.Ref {
	f.t.Helper()
	f.n++
	ref, err := f.store.Put([]byte(fmt.Sprintf("stored evidence body %d, long enough to flip a byte in", f.n)))
	if err != nil {
		f.t.Fatal(err)
	}
	return ref
}

// corrupt flips the first byte of ref's stored file.
func (f *fixture) corrupt(ref evidence.Ref) {
	f.t.Helper()
	path := f.store.Path(ref)
	body, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	body[0] ^= 0xff
	if err := os.WriteFile(path, body, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// slice builds a slice appointed to round at tAppt (before every other
// record), followed by recs, which must be in time order.
func (f *fixture) slice(id ledger.SliceID, tier ledger.Tier, hash repo.Hash, recs ...ledger.Record) ledger.Slice {
	appt := ledger.Appointment{Round: round, Tier: tier, MergeHash: hash}
	return ledger.Slice{
		ID:          id,
		Appointment: &appt,
		Records:     append([]ledger.Record{rec(id, "appt", tAppt, appt)}, recs...),
	}
}

func (f *fixture) verdict(seat ledger.Seat, hash repo.Hash, approve bool, scope ...ledger.ScopeItem) ledger.Verdict {
	return ledger.Verdict{Seat: seat, Hash: hash, Approve: approve, Scope: scope, Transcript: f.ref()}
}

// leg is a leg record at head with correct results and stored evidence.
func (f *fixture) leg(head repo.Hash) ledger.Leg {
	return ledger.Leg{
		Base: repo.Hash(strings.Repeat("0", 40)), Head: head, Tests: []string{"pkg/x_test.go"}, Backend: "host",
		Mutant: f.ref(),
		A:      ledger.LegRun{Want: ledger.Green, Got: ledger.Green, Counted: true, Executed: 10, Transcript: f.ref()},
		B:      ledger.LegRun{Want: ledger.Red, Got: ledger.Red, Counted: true, Executed: 1, Transcript: f.ref()},
		C:      ledger.LegRun{Want: ledger.Green, Got: ledger.Green, Counted: true, Executed: 11, Transcript: f.ref()},
	}
}

func rec(slice ledger.SliceID, name string, at time.Time, body ledger.Body) ledger.Record {
	return ledger.Record{ID: ledger.RecordID(string(slice) + "/" + name), Slice: slice, At: at, Body: body}
}

func hashOf(c string) repo.Hash { return repo.Hash(strings.Repeat(c, 40)) }

// wantFailures asserts CP2's lines are exactly want, in order.
func wantFailures(t *testing.T, failures []check.Failure, want ...string) {
	t.Helper()
	got := make([]string, len(failures))
	for i, f := range failures {
		got[i] = f.String()
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("CP2 =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestCP2AppointmentRequiresMergeHash is criterion C1: the round has at
// least one slice, and every slice has an appointment with a merge hash.
// Hiding mutant: delete the predicate.
func TestCP2AppointmentRequiresMergeHash(t *testing.T) {
	f := newFixture(t)
	hash := hashOf("1")
	ok := f.slice("ok-slice", ledger.Light, hash, rec("ok-slice", "v", t0, f.verdict(ledger.SeatB, hash, true)))
	noAppointment := ledger.Slice{ID: "no-appt"}
	noHash := f.slice("no-hash", ledger.Light, "")

	wantFailures(t, check.CP2(round, []ledger.Slice{ok, noAppointment, noHash}, f.store),
		"slice no-appt: has no appointment with a merge hash",
		"slice no-hash: has no appointment with a merge hash",
	)
	wantFailures(t, check.CP2(round, []ledger.Slice{}, f.store), "round has no slices")
}

// TestCP2SeatApproveBoundToMergeHash is criterion C2: every seat the
// slice's tier names has an APPROVE bound to the slice's merge hash.
// Hiding mutant: delete the predicate.
func TestCP2SeatApproveBoundToMergeHash(t *testing.T) {
	f := newFixture(t)
	hash := hashOf("2")
	s := f.slice("s1", ledger.Heavy, hash, rec("s1", "a1", t0, f.verdict(ledger.SeatA, hash, true)))
	wantFailures(t, check.CP2(round, []ledger.Slice{s}, f.store),
		fmt.Sprintf("slice s1: seat B has no APPROVE bound to %s", hash))
}

// TestCP2SeatLatestBoundVerdictDecides: a tier seat is satisfied only when
// its latest verdict bound to the merge hash is an APPROVE; an APPROVE at
// the merge hash that the seat later overturned with a REJECT at that
// hash, followed by an APPROVE elsewhere, does not count. Hiding mutant:
// any APPROVE at the merge hash satisfies the seat.
func TestCP2SeatLatestBoundVerdictDecides(t *testing.T) {
	f := newFixture(t)
	hash, later := hashOf("2"), hashOf("3")

	t.Run("heavy", func(t *testing.T) {
		s := f.slice("heavy", ledger.Heavy, hash,
			rec("heavy", "a1", t0, f.verdict(ledger.SeatA, hash, true)),
			rec("heavy", "b1", t1, f.verdict(ledger.SeatB, hash, true)),
			rec("heavy", "b2", t2, f.verdict(ledger.SeatB, hash, false)),
			rec("heavy", "b3", t3, f.verdict(ledger.SeatB, later, true)),
		)
		wantFailures(t, check.CP2(round, []ledger.Slice{s}, f.store),
			fmt.Sprintf("slice heavy: seat B's latest verdict bound to %s is REJECT heavy/b2", hash))
	})
	t.Run("light", func(t *testing.T) {
		s := f.slice("light", ledger.Light, hash,
			rec("light", "b1", t1, f.verdict(ledger.SeatB, hash, true)),
			rec("light", "b2", t2, f.verdict(ledger.SeatB, hash, false)),
			rec("light", "b3", t3, f.verdict(ledger.SeatB, later, true)),
		)
		wantFailures(t, check.CP2(round, []ledger.Slice{s}, f.store),
			fmt.Sprintf("slice light: seat B's latest verdict bound to %s is REJECT light/b2", hash))
	})
}

// TestCP2RejectEndsInApprove is criterion C3: every REJECT ends in an
// APPROVE from the same seat. Hiding mutant: delete the predicate.
func TestCP2RejectEndsInApprove(t *testing.T) {
	f := newFixture(t)
	hash := hashOf("3")
	s := f.slice("s1", ledger.Heavy, hash,
		rec("s1", "a1", t0, f.verdict(ledger.SeatA, hash, true)),
		rec("s1", "b1", t0, f.verdict(ledger.SeatB, hash, true)),
		rec("s1", "b2", t1, f.verdict(ledger.SeatB, hashOf("f"), false)),
	)
	wantFailures(t, check.CP2(round, []ledger.Slice{s}, f.store),
		"slice s1: seat B's last verdict is a REJECT with no following APPROVE")
}

// TestCP2ScopeItemsRuledByFullItemID is criterion C4: every SCOPE item a
// verdict carries has a ruling with the matching item id, "<verdict
// record id>#S<k>" -- the full string, never the bare "S<k>" suffix -- and
// the failure names that full id, so two unruled S1 items on different
// verdicts print two different lines. The cases mirror five real re-review
// shapes from guitartime round 1bk-1 (a new verdict's S1 following an earlier
// verdict's ruled S1), each on its own seat and on both tiers.
// Hiding mutants: delete the predicate; print the bare item id.
func TestCP2ScopeItemsRuledByFullItemID(t *testing.T) {
	for _, tc := range []struct {
		name string
		seat ledger.Seat
		tier ledger.Tier
	}{
		{"BillingA2", ledger.SeatA, ledger.Heavy},
		{"RegistryB2", ledger.SeatB, ledger.Light},
		{"EdgeA2", ledger.SeatA, ledger.Light},
		{"ErrorsA2", ledger.SeatA, ledger.Heavy},
		{"ErrorsB2", ledger.SeatB, ledger.Heavy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			hash := hashOf("4")
			id := ledger.SliceID(tc.name)
			var recs []ledger.Record
			// Every tier seat other than the colliding one approves cleanly.
			for _, seat := range tc.tier.Seats() {
				if seat != tc.seat {
					recs = append(recs, rec(id, "other", t0, f.verdict(seat, hash, true)))
				}
			}
			recs = append(recs,
				rec(id, "v1", t1, f.verdict(tc.seat, hash, true, ledger.ScopeItem{ID: "S1", Text: "first review finding"})),
				// The first verdict's S1 is ruled.
				rec(id, "r1", t2, ledger.Ruling{Kind: ledger.Scope, Item: tc.name + "/v1#S1", Text: "addressed"}),
				// Two re-reviews restart numbering at S1, each with a different finding, unruled.
				rec(id, "v2", t3, f.verdict(tc.seat, hash, true, ledger.ScopeItem{ID: "S1", Text: "second review finding"})),
				rec(id, "v3", t4, f.verdict(tc.seat, hash, true, ledger.ScopeItem{ID: "S1", Text: "third review finding"})),
			)
			s := f.slice(id, tc.tier, hash, recs...)
			wantFailures(t, check.CP2(round, []ledger.Slice{s}, f.store),
				fmt.Sprintf("slice %s: SCOPE item %s/v2#S1 has no ruling", tc.name, tc.name),
				fmt.Sprintf("slice %s: SCOPE item %s/v3#S1 has no ruling", tc.name, tc.name),
			)
		})
	}
}

// TestCP2TriagePerSeat is criterion C5: every second consecutive REJECT
// from one seat has a triage ruling for that seat recorded before the
// seat's next verdict. Hiding mutant: delete the predicate.
func TestCP2TriagePerSeat(t *testing.T) {
	f := newFixture(t)
	hash := hashOf("5")
	untriaged := f.slice("untriaged", ledger.Heavy, hash,
		rec("untriaged", "a1", t0, f.verdict(ledger.SeatA, hash, false)),
		rec("untriaged", "b1", t0, f.verdict(ledger.SeatB, hash, true)),
		rec("untriaged", "a2", t1, f.verdict(ledger.SeatA, hash, false)),
		rec("untriaged", "a3", t2, f.verdict(ledger.SeatA, hash, true)),
	)
	triaged := f.slice("triaged", ledger.Heavy, hash,
		rec("triaged", "a1", t0, f.verdict(ledger.SeatA, hash, false)),
		rec("triaged", "b1", t0, f.verdict(ledger.SeatB, hash, true)),
		rec("triaged", "a2", t1, f.verdict(ledger.SeatA, hash, false)),
		rec("triaged", "tri", t1.Add(time.Minute), ledger.Ruling{Kind: ledger.Triage, Item: "loop-cap", Text: "reviewed", Seat: ledger.SeatA}),
		rec("triaged", "a3", t2, f.verdict(ledger.SeatA, hash, true)),
	)
	wantFailures(t, check.CP2(round, []ledger.Slice{untriaged, triaged}, f.store),
		"slice untriaged: seat A's REJECT untriaged/a2 (2 consecutive) has no triage ruling before the seat's next verdict")
}

// TestCP2TriageRequiresTimingBeforeNextVerdict targets criterion C12's
// first mutant: accept a triage ruling recorded at any time, instead of
// strictly before the seat's next verdict.
func TestCP2TriageRequiresTimingBeforeNextVerdict(t *testing.T) {
	f := newFixture(t)
	hash := hashOf("6")
	s := f.slice("s1", ledger.Heavy, hash,
		rec("s1", "a1", t0, f.verdict(ledger.SeatA, hash, false)),
		rec("s1", "b1", t0, f.verdict(ledger.SeatB, hash, true)),
		rec("s1", "a2", t1, f.verdict(ledger.SeatA, hash, false)),
		rec("s1", "a3", t2, f.verdict(ledger.SeatA, hash, true)),
		// Triage recorded AFTER the next verdict (t2): too late.
		rec("s1", "tri", t3, ledger.Ruling{Kind: ledger.Triage, Item: "loop-cap", Text: "late", Seat: ledger.SeatA}),
	)
	wantFailures(t, check.CP2(round, []ledger.Slice{s}, f.store),
		"slice s1: seat A's REJECT s1/a2 (2 consecutive) has no triage ruling before the seat's next verdict")
}

// TestCP2TriageRequiresMatchingSeat targets criterion C12's second
// mutant: ignore the ruling's seat, so a seat-B triage would wrongly
// satisfy seat A's requirement.
func TestCP2TriageRequiresMatchingSeat(t *testing.T) {
	f := newFixture(t)
	hash := hashOf("7")
	s := f.slice("s1", ledger.Heavy, hash,
		rec("s1", "a1", t0, f.verdict(ledger.SeatA, hash, false)),
		rec("s1", "b1", t0, f.verdict(ledger.SeatB, hash, true)),
		rec("s1", "a2", t1, f.verdict(ledger.SeatA, hash, false)),
		// A seat-B triage ruling, correctly timed, but the wrong seat.
		rec("s1", "tri", t1.Add(time.Minute), ledger.Ruling{Kind: ledger.Triage, Item: "loop-cap", Text: "wrong seat", Seat: ledger.SeatB}),
		rec("s1", "a3", t2, f.verdict(ledger.SeatA, hash, true)),
	)
	wantFailures(t, check.CP2(round, []ledger.Slice{s}, f.store),
		"slice s1: seat A's REJECT s1/a2 (2 consecutive) has no triage ruling before the seat's next verdict")
}

// TestCP2TriageEveryEvenStreak: each triage ruling buys one fix round, so
// the 2nd, 4th, 6th, ... consecutive REJECT from a seat each need their
// own triage, recorded strictly after that REJECT and strictly before the
// seat's next verdict. Hiding mutants: require a triage only at streak 2;
// make the bounds inclusive; drop the lower bound.
func TestCP2TriageEveryEvenStreak(t *testing.T) {
	hash := hashOf("5")
	triage := func(name string, at time.Time) ledger.Record {
		return rec("s1", name, at, ledger.Ruling{Kind: ledger.Triage, Item: "loop-cap", Text: "reviewed", Seat: ledger.SeatA})
	}
	for _, tc := range []struct {
		name  string
		build func(f *fixture) []ledger.Record
		want  []string
	}{
		{
			name: "fourth REJECT without its own triage",
			build: func(f *fixture) []ledger.Record {
				return []ledger.Record{
					rec("s1", "a1", t1, f.verdict(ledger.SeatA, hash, false)),
					rec("s1", "a2", t2, f.verdict(ledger.SeatA, hash, false)),
					triage("tri1", t2.Add(time.Minute)),
					rec("s1", "a3", t3, f.verdict(ledger.SeatA, hash, false)),
					rec("s1", "a4", t4, f.verdict(ledger.SeatA, hash, false)),
					rec("s1", "a5", t5, f.verdict(ledger.SeatA, hash, true)),
				}
			},
			want: []string{"slice s1: seat A's REJECT s1/a4 (4 consecutive) has no triage ruling before the seat's next verdict"},
		},
		{
			name: "fourth REJECT with its own triage",
			build: func(f *fixture) []ledger.Record {
				return []ledger.Record{
					rec("s1", "a1", t1, f.verdict(ledger.SeatA, hash, false)),
					rec("s1", "a2", t2, f.verdict(ledger.SeatA, hash, false)),
					triage("tri1", t2.Add(time.Minute)),
					rec("s1", "a3", t3, f.verdict(ledger.SeatA, hash, false)),
					rec("s1", "a4", t4, f.verdict(ledger.SeatA, hash, false)),
					triage("tri2", t4.Add(time.Minute)),
					rec("s1", "a5", t5, f.verdict(ledger.SeatA, hash, true)),
				}
			},
		},
		{
			name: "triage at the REJECT's own At",
			build: func(f *fixture) []ledger.Record {
				return []ledger.Record{
					rec("s1", "a1", t1, f.verdict(ledger.SeatA, hash, false)),
					rec("s1", "a2", t2, f.verdict(ledger.SeatA, hash, false)),
					triage("tri", t2),
					rec("s1", "a3", t3, f.verdict(ledger.SeatA, hash, true)),
				}
			},
			want: []string{"slice s1: seat A's REJECT s1/a2 (2 consecutive) has no triage ruling before the seat's next verdict"},
		},
		{
			name: "triage at the next verdict's At",
			build: func(f *fixture) []ledger.Record {
				return []ledger.Record{
					rec("s1", "a1", t1, f.verdict(ledger.SeatA, hash, false)),
					rec("s1", "a2", t2, f.verdict(ledger.SeatA, hash, false)),
					triage("tri", t3),
					rec("s1", "a3", t3, f.verdict(ledger.SeatA, hash, true)),
				}
			},
			want: []string{"slice s1: seat A's REJECT s1/a2 (2 consecutive) has no triage ruling before the seat's next verdict"},
		},
		{
			name: "triage before the 2nd REJECT",
			build: func(f *fixture) []ledger.Record {
				return []ledger.Record{
					rec("s1", "a1", t1, f.verdict(ledger.SeatA, hash, false)),
					triage("tri", t1.Add(time.Minute)),
					rec("s1", "a2", t2, f.verdict(ledger.SeatA, hash, false)),
					rec("s1", "a3", t3, f.verdict(ledger.SeatA, hash, true)),
				}
			},
			want: []string{"slice s1: seat A's REJECT s1/a2 (2 consecutive) has no triage ruling before the seat's next verdict"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			recs := append([]ledger.Record{rec("s1", "b1", t0, f.verdict(ledger.SeatB, hash, true))}, tc.build(f)...)
			wantFailures(t, check.CP2(round, []ledger.Slice{f.slice("s1", ledger.Heavy, hash, recs...)}, f.store), tc.want...)
		})
	}
}

// TestCP2ContractReachesEveryConsumer is criterion C6: every contract
// revision reached the owner and every consumer named in --to. Hiding
// mutant: delete the predicate.
func TestCP2ContractReachesEveryConsumer(t *testing.T) {
	f := newFixture(t)
	hash := hashOf("8")
	appt := func(id ledger.SliceID, recs ...ledger.Record) ledger.Slice {
		return f.slice(id, ledger.Light, hash, append([]ledger.Record{rec(id, "v", t0, f.verdict(ledger.SeatB, hash, true))}, recs...)...)
	}
	owner := appt("owner", rec("owner", "c1", t1, ledger.Ruling{Kind: ledger.Contract, Item: "seam-x", Text: "revised", To: []ledger.SliceID{"consumer-ok", "consumer-missing"}}))
	// Addressed to itself, so the ack holds whether or not it is classed as
	// an acknowledgement; the classification is TestCP2ContractAcknowledgedAfterEachRevision's.
	consumerOK := appt("consumer-ok", rec("consumer-ok", "c1", t2, ledger.Ruling{Kind: ledger.Contract, Item: "seam-x", Text: "acknowledged", To: []ledger.SliceID{"consumer-ok"}}))
	consumerMissing := appt("consumer-missing")

	wantFailures(t, check.CP2(round, []ledger.Slice{owner, consumerOK, consumerMissing}, f.store),
		`slice consumer-missing: has not recorded the contract revision "seam-x" of record owner/c1`)
}

// TestCP2ContractAcknowledgedAfterEachRevision: contract rulings are
// classified in time order. A ruling R is an acknowledgement only when its
// --to names exactly one slice O, not R's own, and an earlier revision on
// O with R's Item names R's slice in --to; every other contract ruling is
// a revision, and each consumer it names must carry a contract ruling with
// that Item at or after it. Hiding mutants: treat every contract ruling
// as a revision; accept an earlier revision on any slice, R's own included;
// accept any slice of a multi-slice --to as O; ignore the Item when
// classifying; accept a later ruling with any Item; require the later
// ruling strictly after the revision.
func TestCP2ContractAcknowledgedAfterEachRevision(t *testing.T) {
	hash := hashOf("8")
	ruling := func(item string, slice ledger.SliceID, name string, at time.Time, to ...ledger.SliceID) ledger.Record {
		return rec(slice, name, at, ledger.Ruling{Kind: ledger.Contract, Item: item, Text: "contract", To: to})
	}
	contract := func(slice ledger.SliceID, name string, at time.Time, to ...ledger.SliceID) ledger.Record {
		return ruling("seam-x", slice, name, at, to...)
	}
	for _, tc := range []struct {
		name          string
		owner, c1, c2 []ledger.Record
		want          []string
	}{
		{
			name:  "each consumer acknowledges back to the owner",
			owner: []ledger.Record{contract("owner", "rev1", t1, "c1", "c2")},
			c1:    []ledger.Record{contract("c1", "ack1", t2, "owner")},
			c2:    []ledger.Record{contract("c2", "ack1", t2, "owner")},
		},
		{
			name:  "a consumer never acknowledges",
			owner: []ledger.Record{contract("owner", "rev1", t1, "c1", "c2")},
			c1:    []ledger.Record{contract("c1", "ack1", t2, "owner")},
			want:  []string{`slice c2: has not recorded the contract revision "seam-x" of record owner/rev1`},
		},
		{
			name:  "an acknowledgement older than the revision it is measured against",
			owner: []ledger.Record{contract("owner", "rev1", t1, "c1", "c2"), contract("owner", "rev2", t3, "c1", "c2")},
			c1:    []ledger.Record{contract("c1", "ack1", t2, "owner"), contract("c1", "ack2", t4, "owner")},
			c2:    []ledger.Record{contract("c2", "ack1", t2, "owner")},
			want:  []string{`slice c2: has not recorded the contract revision "seam-x" of record owner/rev2`},
		},
		{
			name:  "a second revision after the acknowledgements",
			owner: []ledger.Record{contract("owner", "rev1", t1, "c1", "c2"), contract("owner", "rev2", t3, "c1", "c2")},
			c1:    []ledger.Record{contract("c1", "ack1", t2, "owner")},
			c2:    []ledger.Record{contract("c2", "ack1", t2, "owner")},
			want: []string{
				`slice c1: has not recorded the contract revision "seam-x" of record owner/rev2`,
				`slice c2: has not recorded the contract revision "seam-x" of record owner/rev2`,
			},
		},
		{
			name:  "a second revision acknowledged again",
			owner: []ledger.Record{contract("owner", "rev1", t1, "c1", "c2"), contract("owner", "rev2", t3, "c1", "c2")},
			c1:    []ledger.Record{contract("c1", "ack1", t2, "owner"), contract("c1", "ack2", t4, "owner")},
			c2:    []ledger.Record{contract("c2", "ack1", t2, "owner"), contract("c2", "ack2", t4, "owner")},
		},
		{
			// Seat A B3: the owner's first revision names the owner itself,
			// which must not make its re-revision read as an acknowledgement.
			name:  "an owner listing itself re-revises and the consumer never acknowledges again",
			owner: []ledger.Record{contract("owner", "rev1", t1, "owner", "c1"), contract("owner", "rev2", t3, "c1")},
			c1:    []ledger.Record{contract("c1", "ack1", t2, "owner")},
			want:  []string{`slice c1: has not recorded the contract revision "seam-x" of record owner/rev2`},
		},
		{
			// Seat A L1: a revision naming two slices is never an
			// acknowledgement, even after a stale ruling from one of them.
			name:  "a stale acknowledgement precedes a two-consumer revision",
			owner: []ledger.Record{contract("owner", "rev1", t2, "c1", "c2")},
			c1:    []ledger.Record{contract("c1", "ack0", t1, "owner")},
			want: []string{
				`slice c1: has not recorded the contract revision "seam-x" of record owner/rev1`,
				`slice c2: has not recorded the contract revision "seam-x" of record owner/rev1`,
			},
		},
		{
			name:  "acknowledgements at the same instant as the revision",
			owner: []ledger.Record{contract("owner", "rev1", t1, "c1", "c2")},
			c1:    []ledger.Record{contract("c1", "ack1", t1, "owner")},
			c2:    []ledger.Record{contract("c2", "ack1", t1, "owner")},
		},
		{
			name:  "a consumer revises a second Item the owner never acknowledges",
			owner: []ledger.Record{contract("owner", "rev1", t1, "c1")},
			c1:    []ledger.Record{contract("c1", "ack1", t2, "owner"), ruling("seam-y", "c1", "rev2", t3, "owner")},
			want:  []string{`slice owner: has not recorded the contract revision "seam-y" of record c1/rev2`},
		},
		{
			name:  "a consumer rules only on a different Item",
			owner: []ledger.Record{contract("owner", "rev1", t1, "c1"), ruling("seam-y", "owner", "ack1", t3, "c1")},
			c1:    []ledger.Record{ruling("seam-y", "c1", "rev1", t2, "owner")},
			want:  []string{`slice c1: has not recorded the contract revision "seam-x" of record owner/rev1`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			build := func(id ledger.SliceID, recs []ledger.Record) ledger.Slice {
				return f.slice(id, ledger.Light, hash, append([]ledger.Record{rec(id, "v", t0, f.verdict(ledger.SeatB, hash, true))}, recs...)...)
			}
			slices := []ledger.Slice{build("c1", tc.c1), build("c2", tc.c2), build("owner", tc.owner)}
			wantFailures(t, check.CP2(round, slices, f.store), tc.want...)
		})
	}
}

// TestCP2LegsBoundToMergeHash is criterion C7: a slice with leg records
// has at least one bound to its merge hash, and every leg record bound to
// the merge hash has the correct results; a slice with no legs at all
// passes with none. Hiding mutant: delete the predicate.
func TestCP2LegsBoundToMergeHash(t *testing.T) {
	f := newFixture(t)
	hash := hashOf("9")
	other := hashOf("f")
	withApprove := func(id ledger.SliceID, recs ...ledger.Record) ledger.Slice {
		return f.slice(id, ledger.Light, hash, append([]ledger.Record{rec(id, "v", t0, f.verdict(ledger.SeatB, hash, true))}, recs...)...)
	}
	brokenLeg := f.leg(hash)
	brokenLeg.B.Got = ledger.Green // leg (b) did not run red

	wantFailures(t, check.CP2(round, []ledger.Slice{
		withApprove("no-leg-bound", rec("no-leg-bound", "l1", t1, f.leg(other))),
		withApprove("faulted-leg", rec("faulted-leg", "l1", t1, brokenLeg)),
		withApprove("clean", rec("clean", "l1", t1, f.leg(hash))),
		withApprove("no-legs"),
	}, f.store),
		fmt.Sprintf("slice faulted-leg: leg faulted-leg/l1 at %s: leg (b) did not run red", hash),
		fmt.Sprintf("slice no-leg-bound: has no leg record bound to its merge hash %s", hash),
	)
}

// TestCP2StaleHashNotAccepted is criterion C11: an APPROVE bound to a hash
// other than the merge hash does not satisfy the seat condition, and a
// leg record at a non-merge head does not satisfy the leg condition, even
// when the two hashes share a common prefix. Hiding mutant: compare 7-char
// prefixes.
func TestCP2StaleHashNotAccepted(t *testing.T) {
	f := newFixture(t)
	hash := hashOf("a")
	stale := repo.Hash("aaaaaaa" + strings.Repeat("b", 33)) // shares hash's first 7 characters
	if hash[:7] != stale[:7] {
		t.Fatalf("fixture hashes do not share a 7-character prefix: %s / %s", hash, stale)
	}
	staleSeat := f.slice("stale-seat", ledger.Light, hash, rec("stale-seat", "v", t0, f.verdict(ledger.SeatB, stale, true)))
	staleLeg := f.slice("stale-leg", ledger.Light, hash,
		rec("stale-leg", "v", t0, f.verdict(ledger.SeatB, hash, true)),
		rec("stale-leg", "l", t1, f.leg(stale)),
	)
	wantFailures(t, check.CP2(round, []ledger.Slice{staleSeat, staleLeg}, f.store),
		fmt.Sprintf("slice stale-leg: has no leg record bound to its merge hash %s", hash),
		fmt.Sprintf("slice stale-seat: seat B has no APPROVE bound to %s", hash),
	)
}

// TestCP2EvidenceGetErrorIsAFailureNotIgnored is criterion C9's evidence
// half: a missing stored transcript makes cp2 fail closed instead of
// treating the Get error as a pass. Hiding mutant: treat a Get error as a
// pass.
func TestCP2EvidenceGetErrorIsAFailureNotIgnored(t *testing.T) {
	f := newFixture(t)
	hash := hashOf("c")
	missing := evidence.Ref(strings.Repeat("d", 64)) // never stored
	v := f.verdict(ledger.SeatB, hash, true)
	v.Transcript = missing
	s := f.slice("s1", ledger.Light, hash, rec("s1", "v", t0, v))
	failures := check.CP2(round, []ledger.Slice{s}, f.store)
	if len(failures) != 1 || !strings.Contains(failures[0].String(), string(missing)) {
		t.Fatalf("CP2 = %v, want one failure naming the missing ref %s", failures, missing)
	}
}

// TestCP2EmptyEvidenceRefFails: a verdict with no transcript, and a leg
// with no mutant or no leg (a), (b), or (c) transcript, each fail cp2 with
// a line naming the record; no empty ref passes by default. A lint with no
// input is advice and fails nothing. Hiding mutant: skip an empty ref.
func TestCP2EmptyEvidenceRefFails(t *testing.T) {
	f := newFixture(t)
	hash := hashOf("c")
	v := f.verdict(ledger.SeatB, hash, true)
	v.Transcript = ""
	l := f.leg(hash)
	l.Mutant, l.A.Transcript, l.B.Transcript, l.C.Transcript = "", "", "", ""
	s := f.slice("s1", ledger.Light, hash, rec("s1", "v", t0, v), rec("s1", "l", t1, l), rec("s1", "n", t2, ledger.Lint{Kind: ledger.Brief}))
	wantFailures(t, check.CP2(round, []ledger.Slice{s}, f.store),
		"slice s1: record s1/v has no stored transcript evidence",
		"slice s1: record s1/l has no stored mutant evidence",
		"slice s1: record s1/l has no stored leg a evidence",
		"slice s1: record s1/l has no stored leg b evidence",
		"slice s1: record s1/l has no stored leg c evidence",
	)
}

// TestCP2VerdictTranscriptIntegrity is criterion C10's verdict half
// (acceptance 5): a one-byte change to a stored verdict transcript is
// reported with its ref. Hiding mutant: skip Get for verdict transcripts.
func TestCP2VerdictTranscriptIntegrity(t *testing.T) {
	f := newFixture(t)
	hash := hashOf("e")
	v := f.verdict(ledger.SeatB, hash, true)
	s := f.slice("s1", ledger.Light, hash, rec("s1", "v", t0, v))
	wantFailures(t, check.CP2(round, []ledger.Slice{s}, f.store))

	f.corrupt(v.Transcript)
	failures := check.CP2(round, []ledger.Slice{s}, f.store)
	if len(failures) != 1 || !strings.Contains(failures[0].String(), string(v.Transcript)) {
		t.Fatalf("CP2 after corruption = %v, want one failure naming ref %s", failures, v.Transcript)
	}
}

// TestCP2LegEvidenceIntegrity is criterion C10's leg half: a one-byte
// change to a stored mutant or leg (a), (b), or (c) transcript is reported
// with its ref and its role. Hiding mutant: skip Get for leg refs.
func TestCP2LegEvidenceIntegrity(t *testing.T) {
	for _, tc := range []struct {
		role string
		pick func(l ledger.Leg) evidence.Ref
	}{
		{"mutant", func(l ledger.Leg) evidence.Ref { return l.Mutant }},
		{"leg a", func(l ledger.Leg) evidence.Ref { return l.A.Transcript }},
		{"leg b", func(l ledger.Leg) evidence.Ref { return l.B.Transcript }},
		{"leg c", func(l ledger.Leg) evidence.Ref { return l.C.Transcript }},
	} {
		t.Run(tc.role, func(t *testing.T) {
			f := newFixture(t)
			hash := hashOf("e")
			l := f.leg(hash)
			s := f.slice("s1", ledger.Light, hash, rec("s1", "v", t0, f.verdict(ledger.SeatB, hash, true)), rec("s1", "l", t1, l))
			wantFailures(t, check.CP2(round, []ledger.Slice{s}, f.store))

			ref := tc.pick(l)
			f.corrupt(ref)
			failures := check.CP2(round, []ledger.Slice{s}, f.store)
			prefix := fmt.Sprintf("slice s1: stored %s evidence %s of record s1/l failed integrity: ", tc.role, ref)
			if len(failures) != 1 || !strings.HasPrefix(failures[0].String(), prefix) {
				t.Fatalf("CP2 after corruption = %v, want one failure starting %q", failures, prefix)
			}
		})
	}
}

// TestCP2IgnoresCompositionVerdicts: composition verdicts reach no cp2
// condition (plan R7: cp2 precedes composition) -- not their SCOPE items,
// not a REJECT, not a missing transcript. Hiding mutant: include them.
func TestCP2IgnoresCompositionVerdicts(t *testing.T) {
	f := newFixture(t)
	hash := hashOf("b")
	comp := ledger.Verdict{Seat: ledger.SeatComposition, Hash: hash, Approve: false, Scope: []ledger.ScopeItem{{ID: "S1", Text: "composition finding"}}}
	s := f.slice("s1", ledger.Light, hash,
		rec("s1", "v", t0, f.verdict(ledger.SeatB, hash, true)),
		rec("s1", "comp1", t1, comp),
		rec("s1", "comp2", t2, comp),
		rec("s1", "comp3", t3, comp),
	)
	wantFailures(t, check.CP2(round, []ledger.Slice{s}, f.store))
}

// TestCP2ScopesRecordsToRound: cp2 for a round sees only a slice's records
// at or after its first appointment to that round, so a bead reused from
// an earlier round is not held to that round's unruled items and
// untriaged REJECTs. Hiding mutant: use all records.
func TestCP2ScopesRecordsToRound(t *testing.T) {
	f := newFixture(t)
	old, hash := hashOf("1"), hashOf("2")
	r0 := ledger.Appointment{Round: "r0", Tier: ledger.Light, MergeHash: old}
	r1 := ledger.Appointment{Round: "r1", Tier: ledger.Light, MergeHash: hash}
	s := ledger.Slice{ID: "reused", Appointment: &r1, Records: []ledger.Record{
		rec("reused", "appt0", t0, r0),
		rec("reused", "b1", t1, f.verdict(ledger.SeatB, old, false, ledger.ScopeItem{ID: "S1", Text: "old finding"})),
		rec("reused", "b2", t2, f.verdict(ledger.SeatB, old, false)),
		rec("reused", "b3", t3, f.verdict(ledger.SeatB, old, false)),
		rec("reused", "appt1", t4, r1),
		rec("reused", "b4", t5, f.verdict(ledger.SeatB, hash, true)),
	}}
	wantFailures(t, check.CP2("r1", []ledger.Slice{s}, f.store))
	if got := check.RoundRecords("r1", s); len(got) != 2 || got[0].ID != "reused/appt1" {
		t.Fatalf("RoundRecords(r1) = %v, want [reused/appt1 reused/b4]", got)
	}
}

// TestCP2FailureOrderIsDeterministic: failures come out by slice, then
// condition, then the At of the record at fault, identically on every
// run (no map iteration reaches the output). The fixture uses only
// conditions whose wording other items do not pin.
func TestCP2FailureOrderIsDeterministic(t *testing.T) {
	f := newFixture(t)
	hash := hashOf("3")
	// Seat B's REJECT precedes seat A's, so At order differs from seat order.
	sb := f.slice("s-b", ledger.Heavy, hash,
		rec("s-b", "b1", t1, f.verdict(ledger.SeatB, hash, false)),
		rec("s-b", "a1", t2, f.verdict(ledger.SeatA, hash, false)),
	)
	sa := f.slice("s-a", ledger.Light, "", rec("s-a", "v", t1, f.verdict(ledger.SeatB, hash, false)))
	want := []string{
		"slice s-a: has no appointment with a merge hash",
		"slice s-a: seat B's last verdict is a REJECT with no following APPROVE",
		fmt.Sprintf("slice s-b: seat B's latest verdict bound to %s is REJECT s-b/b1", hash),
		fmt.Sprintf("slice s-b: seat A's latest verdict bound to %s is REJECT s-b/a1", hash),
		"slice s-b: seat B's last verdict is a REJECT with no following APPROVE",
		"slice s-b: seat A's last verdict is a REJECT with no following APPROVE",
	}
	for i := 0; i < 20; i++ {
		wantFailures(t, check.CP2(round, []ledger.Slice{sb, sa}, f.store), want...)
	}
}
