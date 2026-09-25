// Package check hides the cp2 checkpoint predicates: what a round must
// carry in its ledger before a merge is allowed, and how a failed
// condition maps to a plain sentence naming the slice and the item.
package check

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/dpoage/auspex/internal/evidence"
	"github.com/dpoage/auspex/internal/ledger"
)

// Failure names one cp2 condition found false. Slice is empty for a
// round-level condition (a round with no slices at all).
type Failure struct {
	Slice ledger.SliceID
	Text  string

	cond condition // sort key after Slice
	at   time.Time // sort key after cond: the At of the record at fault
}

// String renders a Failure as the one plain line DESIGN.md prints under
// obnuntiatio: "slice <id>: <text>", or bare Text when Slice is empty.
func (f Failure) String() string {
	if f.Slice == "" {
		return f.Text
	}
	return fmt.Sprintf("slice %s: %s", f.Slice, f.Text)
}

// condition is a cp2 condition, in the order failures are reported.
type condition int

const (
	condAppointment condition = iota
	condSeats
	condRejectEndsApprove
	condScope
	condTriage
	condContract
	condLegs
	condEvidence
)

// seats are the oracle seats cp2 judges, in report order. Composition
// verdicts never reach a condition (plan R7: cp2 precedes composition).
var seats = []ledger.Seat{ledger.SeatA, ledger.SeatB}

// RoundRecords returns the records of s that belong to round: every record
// at or after s's first Appointment record whose Round is round, in the
// time order Slice.Records carries. A slice with no appointment to round
// has none, so a bead reused across rounds never leaks an earlier round's
// records into this one.
func RoundRecords(round ledger.RoundID, s ledger.Slice) []ledger.Record {
	for _, r := range s.Records {
		a, ok := r.Body.(ledger.Appointment)
		if !ok || a.Round != round {
			continue
		}
		i := sort.Search(len(s.Records), func(i int) bool { return !s.Records[i].At.Before(r.At) })
		return s.Records[i:]
	}
	return nil
}

// CP2 evaluates DESIGN.md's cp2 conditions for round over slices, using
// only each slice's RoundRecords minus composition verdicts, and
// re-opening every leg and verdict transcript through store to confirm its
// stored hash still matches its bytes. It returns every failed condition
// ordered by slice, then condition, then the At of the record at fault;
// nil means cp2 holds. CP2 never returns a Go error: a round with no
// slices, and a missing, empty, or corrupt transcript ref, are each
// themselves a Failure, so cp2 fails closed on every path.
func CP2(round ledger.RoundID, slices []ledger.Slice, store *evidence.Store) []Failure {
	if len(slices) == 0 {
		return []Failure{{Text: "round has no slices"}}
	}
	scoped := make([]ledger.Slice, len(slices))
	for i, s := range slices {
		scoped[i] = ledger.Slice{ID: s.ID, Appointment: s.Appointment, Records: cp2Records(round, s)}
	}
	var out []Failure
	out = append(out, cp2Appointment(scoped)...)
	out = append(out, cp2SeatApprove(scoped)...)
	out = append(out, cp2RejectEndsApprove(scoped)...)
	out = append(out, cp2ScopeRuled(scoped)...)
	out = append(out, cp2Triage(scoped)...)
	out = append(out, cp2Contract(scoped)...)
	out = append(out, cp2Legs(scoped)...)
	out = append(out, cp2Evidence(scoped, store)...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Slice != b.Slice {
			return a.Slice < b.Slice
		}
		if a.cond != b.cond {
			return a.cond < b.cond
		}
		return a.at.Before(b.at)
	})
	return out
}

// cp2Records is the record set every condition sees: the slice's records
// in round, with composition verdicts removed.
func cp2Records(round ledger.RoundID, s ledger.Slice) []ledger.Record {
	var out []ledger.Record
	for _, r := range RoundRecords(round, s) {
		if v, ok := r.Body.(ledger.Verdict); ok && v.Seat == ledger.SeatComposition {
			continue
		}
		out = append(out, r)
	}
	return out
}

// cp2Appointment: the round has at least one slice (checked by CP2 itself)
// and every slice has an appointment with a merge hash. A slice failing
// this is skipped by cp2SeatApprove and cp2Legs, which need the hash.
func cp2Appointment(slices []ledger.Slice) []Failure {
	var out []Failure
	for _, s := range slices {
		if !appointed(s) {
			out = append(out, Failure{Slice: s.ID, Text: "has no appointment with a merge hash", cond: condAppointment})
		}
	}
	return out
}

func appointed(s ledger.Slice) bool {
	return s.Appointment != nil && s.Appointment.MergeHash != ""
}

// cp2SeatApprove: every seat the slice's tier names has, as its latest
// verdict bound to the slice's merge hash (exact hash equality, latest by
// At), an APPROVE. An APPROVE at the merge hash that the same seat later
// overturned with a REJECT at that hash does not count.
func cp2SeatApprove(slices []ledger.Slice) []Failure {
	var out []Failure
	for _, s := range slices {
		if !appointed(s) {
			continue
		}
		hash := s.Appointment.MergeHash
		for _, seat := range s.Appointment.Tier.Seats() {
			var latest *ledger.Record
			for i, r := range s.Records {
				if v, ok := r.Body.(ledger.Verdict); ok && v.Seat == seat && v.Hash == hash {
					latest = &s.Records[i]
				}
			}
			switch {
			case latest == nil:
				out = append(out, Failure{Slice: s.ID, Text: fmt.Sprintf("seat %s has no APPROVE bound to %s", seat, hash), cond: condSeats})
			case !latest.Body.(ledger.Verdict).Approve:
				out = append(out, Failure{Slice: s.ID, Text: fmt.Sprintf("seat %s's latest verdict bound to %s is REJECT %s", seat, hash, latest.ID), cond: condSeats, at: latest.At})
			}
		}
	}
	return out
}

// seatVerdicts returns seat's Verdict records, in time order.
func seatVerdicts(records []ledger.Record, seat ledger.Seat) []ledger.Record {
	var out []ledger.Record
	for _, r := range records {
		if v, ok := r.Body.(ledger.Verdict); ok && v.Seat == seat {
			out = append(out, r)
		}
	}
	return out
}

// cp2RejectEndsApprove: every REJECT ends in an APPROVE from the same
// seat -- equivalently, for every seat with at least one verdict, the
// last verdict is an APPROVE.
func cp2RejectEndsApprove(slices []ledger.Slice) []Failure {
	var out []Failure
	for _, s := range slices {
		for _, seat := range seats {
			verdicts := seatVerdicts(s.Records, seat)
			if len(verdicts) == 0 {
				continue
			}
			last := verdicts[len(verdicts)-1]
			if !last.Body.(ledger.Verdict).Approve {
				out = append(out, Failure{Slice: s.ID, Text: fmt.Sprintf("seat %s's last verdict is a REJECT with no following APPROVE", seat), cond: condRejectEndsApprove, at: last.At})
			}
		}
	}
	return out
}

// cp2ScopeRuled: every SCOPE item a verdict carries has a ruling with the
// matching item id, "<verdict record id>#S<k>" -- the full string, never
// the bare "S<k>" position, so a re-review's restarted S1 numbering never
// inherits an earlier verdict's ruling. The failure names the full id,
// the one decree --item takes.
func cp2ScopeRuled(slices []ledger.Slice) []Failure {
	var out []Failure
	for _, s := range slices {
		ruled := map[string]bool{}
		for _, r := range s.Records {
			if rl, ok := r.Body.(ledger.Ruling); ok && rl.Kind == ledger.Scope {
				ruled[rl.Item] = true
			}
		}
		for _, r := range s.Records {
			v, ok := r.Body.(ledger.Verdict)
			if !ok {
				continue
			}
			for _, item := range v.Scope {
				if full := string(r.ID) + "#" + item.ID; !ruled[full] {
					out = append(out, Failure{Slice: s.ID, Text: fmt.Sprintf("SCOPE item %s has no ruling", full), cond: condScope, at: r.At})
				}
			}
		}
	}
	return out
}

// cp2Triage: every even-numbered consecutive REJECT from one seat (the
// 2nd, 4th, 6th, ...: each triage ruling buys one fix round) has a triage
// ruling for that seat recorded strictly after that REJECT and strictly
// before the seat's next verdict. A streak not yet followed by a next
// verdict cannot yet be judged and is not a failure.
func cp2Triage(slices []ledger.Slice) []Failure {
	var out []Failure
	for _, s := range slices {
		for _, seat := range seats {
			verdicts := seatVerdicts(s.Records, seat)
			streak := 0
			for i, r := range verdicts {
				if r.Body.(ledger.Verdict).Approve {
					streak = 0
					continue
				}
				streak++
				if streak%2 != 0 || i+1 >= len(verdicts) {
					continue
				}
				if !hasTriageBetween(s.Records, seat, r.At, verdicts[i+1].At) {
					out = append(out, Failure{Slice: s.ID, Text: fmt.Sprintf("seat %s's REJECT %s (%d consecutive) has no triage ruling before the seat's next verdict", seat, r.ID, streak), cond: condTriage, at: r.At})
				}
			}
		}
	}
	return out
}

func hasTriageBetween(records []ledger.Record, seat ledger.Seat, after, before time.Time) bool {
	for _, r := range records {
		rl, ok := r.Body.(ledger.Ruling)
		if !ok || rl.Kind != ledger.Triage || rl.Seat != seat {
			continue
		}
		if r.At.After(after) && r.At.Before(before) {
			return true
		}
	}
	return false
}

// cp2Contract: every contract revision reached every consumer named in its
// To. Contract rulings are classified in time order across the round: a
// ruling R (slice S, Item I, To T) is an acknowledgement only when T names
// exactly one slice O, O is not S, and an earlier revision (At strictly
// before R) on slice O with Item I lists S in its To. Every other contract
// ruling is a revision, checked on its own: every slice in its To other
// than its own carries a contract ruling with Item I at or after the
// revision's At. An acknowledgement's own To is not checked, so the
// convention of addressing it back to the owner never demands a reply.
// Accepted limit: a single-consumer revision recorded after a stale
// acknowledgement from that consumer is itself read as an acknowledgement.
func cp2Contract(slices []ledger.Slice) []Failure {
	var rulings []ledger.Record
	bySlice := make(map[ledger.SliceID][]ledger.Record, len(slices))
	for _, s := range slices {
		bySlice[s.ID] = s.Records
		for _, r := range s.Records {
			if rl, ok := r.Body.(ledger.Ruling); ok && rl.Kind == ledger.Contract {
				rulings = append(rulings, r)
			}
		}
	}
	sort.SliceStable(rulings, func(i, j int) bool { return rulings[i].At.Before(rulings[j].At) })

	var revisions []ledger.Record
	var out []Failure
	for _, r := range rulings {
		rl := r.Body.(ledger.Ruling)
		if acknowledges(revisions, r) {
			continue
		}
		revisions = append(revisions, r)
		for _, to := range rl.To {
			if to != r.Slice && !hasContractSince(bySlice[to], rl.Item, r.At) {
				out = append(out, Failure{Slice: to, Text: fmt.Sprintf("has not recorded the contract revision %q of record %s", rl.Item, r.ID), cond: condContract, at: r.At})
			}
		}
	}
	return out
}

// acknowledges reports whether contract ruling r answers an earlier
// revision: r names exactly one slice, not its own, and a revision on that
// slice with r's Item, recorded strictly before r, names r's slice in its
// To.
func acknowledges(revisions []ledger.Record, r ledger.Record) bool {
	rl := r.Body.(ledger.Ruling)
	if len(rl.To) != 1 || rl.To[0] == r.Slice {
		return false
	}
	owner := rl.To[0]
	for _, rev := range revisions {
		body := rev.Body.(ledger.Ruling)
		if rev.Slice != owner || body.Item != rl.Item || !rev.At.Before(r.At) {
			continue
		}
		if slices.Contains(body.To, r.Slice) {
			return true
		}
	}
	return false
}

func hasContractSince(records []ledger.Record, item string, since time.Time) bool {
	for _, r := range records {
		if rl, ok := r.Body.(ledger.Ruling); ok && rl.Kind == ledger.Contract && rl.Item == item && !r.At.Before(since) {
			return true
		}
	}
	return false
}

// cp2Legs: a slice with any leg records has at least one bound to its
// merge hash (by exact Leg.Head equality), and every leg record bound to
// the merge hash has no Faults. A slice with no leg records at all
// (deletion and type-shape criteria take no legs) passes with none.
func cp2Legs(slices []ledger.Slice) []Failure {
	var out []Failure
	for _, s := range slices {
		if !appointed(s) {
			continue
		}
		legs, bound := 0, 0
		for _, r := range s.Records {
			l, ok := r.Body.(ledger.Leg)
			if !ok {
				continue
			}
			legs++
			if l.Head != s.Appointment.MergeHash {
				continue
			}
			bound++
			for _, fault := range l.Faults() {
				out = append(out, Failure{Slice: s.ID, Text: fmt.Sprintf("leg %s at %s: %s", r.ID, l.Head, fault), cond: condLegs, at: r.At})
			}
		}
		if legs > 0 && bound == 0 {
			out = append(out, Failure{Slice: s.ID, Text: fmt.Sprintf("has no leg record bound to its merge hash %s", s.Appointment.MergeHash), cond: condLegs})
		}
	}
	return out
}

// cp2Evidence: every leg and verdict carries its evidence, and every
// stored hash still matches its file. A lint's input is advice, which cp2
// does not gate. An empty ref is a Failure naming the record and the
// missing role; a set ref is re-opened through store, and a missing or
// corrupt one is a Failure naming its ref, never a silently ignored error.
func cp2Evidence(slices []ledger.Slice, store *evidence.Store) []Failure {
	var out []Failure
	seen := map[evidence.Ref]bool{}
	for _, s := range slices {
		for _, r := range s.Records {
			if _, ok := r.Body.(ledger.Lint); ok {
				continue
			}
			for _, e := range r.Evidence() {
				if e.Ref == "" {
					out = append(out, Failure{Slice: s.ID, Text: fmt.Sprintf("record %s has no stored %s evidence", r.ID, e.Role), cond: condEvidence, at: r.At})
					continue
				}
				if seen[e.Ref] {
					continue
				}
				seen[e.Ref] = true
				if _, err := store.Get(e.Ref); err != nil {
					out = append(out, Failure{Slice: s.ID, Text: fmt.Sprintf("stored %s evidence %s of record %s failed integrity: %v", e.Role, e.Ref, r.ID, err), cond: condEvidence, at: r.At})
				}
			}
		}
	}
	return out
}
