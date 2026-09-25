package main

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/dpoage/auspex/internal/check"
	"github.com/dpoage/auspex/internal/ledger"
	"github.com/dpoage/auspex/internal/lint"
)

// augury prints a round's timeline in time order, the wall time per
// DESIGN.md phase, and the omen summary, from each slice's records in the
// round (check.RoundRecords). A round with no slices is obnuntiatio.
//
//	auspex augury <round-id>
func init() {
	register(command{
		name:     "augury",
		synopsis: "print a round's timeline, wall time per phase, and the omen summary",
		run:      runAugury,
	})
}

func runAugury(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("augury", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: auspex augury <round-id>")
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(stderr, "auspex augury: want exactly one round id, got %q\n", fs.Args())
		fs.Usage()
		return exitUsage
	}
	round := ledger.RoundID(fs.Arg(0))

	led, err := openLedger()
	if err != nil {
		return obnuntiatio(stderr, "augury: cannot open the ledger", []string{err.Error()})
	}
	slices, err := led.Round(round)
	if err != nil {
		return obnuntiatio(stderr, fmt.Sprintf("augury: cannot read round %s", round), []string{err.Error()})
	}
	if len(slices) == 0 {
		return obnuntiatio(stderr, fmt.Sprintf("round %s has no slices", round), nil)
	}
	for i := range slices {
		slices[i].Records = check.RoundRecords(round, slices[i])
	}

	for _, rec := range mergedRecords(slices) {
		fmt.Fprintf(stdout, "%s slice %s %s %s\n", rec.At.Format(time.RFC3339), rec.Slice, ledger.KindOf(rec.Body), rec.ID)
	}
	for _, s := range slices {
		for _, p := range slicePhases(s) {
			fmt.Fprintf(stdout, "phase slice %s: %s: %s\n", s.ID, p.name, p.duration())
		}
	}
	printOmenSummary(stdout, slices)
	return exitSuccess
}

// mergedRecords flattens every slice's records into one round-wide list
// in time order. A RecordID's suffix is 12 random hex characters and
// carries no time signal, so it is not a sort key.
func mergedRecords(slices []ledger.Slice) []ledger.Record {
	var out []ledger.Record
	for _, s := range slices {
		out = append(out, s.Records...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// phase is one named interval of a slice's timeline.
type phase struct {
	name       string
	start, end time.Time
}

// duration renders the phase's wall time, or "n/a (<reason>)" when its
// end precedes its start (a verdict recorded before the first leg, say);
// never a negative duration.
func (p phase) duration() string {
	if p.end.Before(p.start) {
		return fmt.Sprintf("n/a (ends %s before it starts)", p.start.Sub(p.end))
	}
	return p.end.Sub(p.start).String()
}

// slicePhases computes DESIGN.md's phases for one slice from its records
// in the round, in the time order Slice.Records already carries: appoint to
// first leg record, first leg record to each seat's first verdict, each
// REJECT to that seat's next verdict (one fix round per pair), and to the
// composition verdict. The first phase starts at the slice's first
// (earliest) appointment record -- never Slice.Appointment, which
// ledger.go documents as the latest and would silently use a slice's
// CP2-time re-appointment as the phase start instead of its slicing-time
// one. Premortem and polish leave no record and are not phases here.
func slicePhases(s ledger.Slice) []phase {
	var firstAppointment, firstLeg *ledger.Record
	verdicts := map[ledger.Seat][]ledger.Record{}
	for i := range s.Records {
		r := &s.Records[i]
		switch v := r.Body.(type) {
		case ledger.Appointment:
			if firstAppointment == nil {
				firstAppointment = r
			}
		case ledger.Leg:
			if firstLeg == nil {
				firstLeg = r
			}
		case ledger.Verdict:
			verdicts[v.Seat] = append(verdicts[v.Seat], *r)
		}
	}

	var phases []phase
	if firstAppointment != nil && firstLeg != nil {
		phases = append(phases, phase{name: "appoint to first leg", start: firstAppointment.At, end: firstLeg.At})
	}
	for _, seat := range []ledger.Seat{ledger.SeatA, ledger.SeatB} {
		seatVerdicts := verdicts[seat]
		if len(seatVerdicts) == 0 || firstLeg == nil {
			continue
		}
		phases = append(phases, phase{
			name:  fmt.Sprintf("first leg to seat %s's first verdict", seat),
			start: firstLeg.At, end: seatVerdicts[0].At,
		})
		for i := 0; i+1 < len(seatVerdicts); i++ {
			if seatVerdicts[i].Body.(ledger.Verdict).Approve {
				continue
			}
			phases = append(phases, phase{
				name:  fmt.Sprintf("seat %s reject to next verdict", seat),
				start: seatVerdicts[i].At, end: seatVerdicts[i+1].At,
			})
		}
	}
	if comp := verdicts[ledger.SeatComposition]; len(comp) > 0 {
		compAt := comp[0].At
		start, found := time.Time{}, false
		for seat, seatVerdicts := range verdicts {
			if seat == ledger.SeatComposition {
				continue
			}
			for _, v := range seatVerdicts {
				if !v.At.After(compAt) && (!found || v.At.After(start)) {
					start, found = v.At, true
				}
			}
		}
		if !found && firstLeg != nil {
			start = firstLeg.At
		} else if !found {
			start = compAt
		}
		phases = append(phases, phase{name: "to composition verdict", start: start, end: compAt})
	}
	return phases
}

// printOmenSummary prints one line per flagged Answer across every Lint
// record of the round: "omen: <lint> flagged at <p> (threshold <t>)", or,
// for an unclassified blocker, "omen: <lint> unclassified at <p> (threshold
// <t>)". Then the non-product share of classified Blocker answers -- the
// skill's Overscope signal -- and the count of unclassified blockers,
// which the share never counts.
func printOmenSummary(w io.Writer, slices []ledger.Slice) {
	var classified, nonProduct, unclassifiedCount int
	for _, r := range mergedRecords(slices) {
		rec, ok := r.Body.(ledger.Lint)
		if !ok {
			continue
		}
		for _, a := range rec.Answers {
			isUnclassified := rec.Kind == ledger.Blocker && a.Choice == lint.Unclassified
			if a.Flagged {
				verb := "flagged"
				if isUnclassified {
					verb = lint.Unclassified
				}
				fmt.Fprintf(w, "omen: %s %s at %v (threshold %v)\n", a.Lint, verb, a.P, a.Threshold)
			}
			if rec.Kind != ledger.Blocker {
				continue
			}
			if isUnclassified {
				unclassifiedCount++
				continue
			}
			classified++
			if a.Choice != lint.Product {
				nonProduct++
			}
		}
	}
	if classified > 0 {
		fmt.Fprintf(w, "non-product blocker share: %d/%d\n", nonProduct, classified)
	}
	if unclassifiedCount > 0 {
		fmt.Fprintf(w, "unclassified blockers: %d\n", unclassifiedCount)
	}
}
