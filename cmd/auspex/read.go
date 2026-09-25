package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/dpoage/auspex/internal/ledger"
)

// read prints one record and its evidence paths, each labeled by role; it
// exits 1 when any evidence ref the record stores is empty, missing, or
// corrupt.
//
//	auspex read <record-id>
func init() {
	register(command{
		name:     "read",
		synopsis: "print one record and its evidence paths",
		run:      runRead,
	})
}

func runRead(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: auspex read <record-id>")
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(stderr, "auspex read: want exactly one record id, got %q\n", fs.Args())
		fs.Usage()
		return exitUsage
	}
	id := ledger.RecordID(fs.Arg(0))

	led, err := openLedger()
	if err != nil {
		return obnuntiatio(stderr, "read: cannot open the ledger", []string{err.Error()})
	}
	rec, err := led.Get(id)
	if err != nil {
		return obnuntiatio(stderr, fmt.Sprintf("read: no record %s", id), []string{err.Error()})
	}
	store, err := openEvidence()
	if err != nil {
		return obnuntiatio(stderr, "read: cannot open the evidence store", []string{err.Error()})
	}

	printRecord(stdout, rec)
	var faults []string
	for _, e := range rec.Evidence() {
		if e.Ref == "" {
			faults = append(faults, fmt.Sprintf("%s: no evidence ref stored", e.Role))
			continue
		}
		fmt.Fprintf(stdout, "%s: %s\n", e.Role, store.Path(e.Ref))
		if _, err := store.Get(e.Ref); err != nil {
			faults = append(faults, fmt.Sprintf("%s %s: %v", e.Role, e.Ref, err))
		}
	}
	if len(faults) > 0 {
		return obnuntiatio(stderr, fmt.Sprintf("read: record %s references missing or corrupt evidence", id), faults)
	}
	return exitSuccess
}

// printRecord prints a record's identity and its body's fields, one per
// line, plain words.
func printRecord(w io.Writer, rec ledger.Record) {
	fmt.Fprintf(w, "record %s\n", rec.ID)
	fmt.Fprintf(w, "slice %s\n", rec.Slice)
	fmt.Fprintf(w, "at %s\n", rec.At.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Fprintf(w, "kind %s\n", ledger.KindOf(rec.Body))
	switch body := rec.Body.(type) {
	case ledger.Appointment:
		fmt.Fprintf(w, "round %s\n", body.Round)
		fmt.Fprintf(w, "tier %s\n", body.Tier)
		fmt.Fprintf(w, "merge-hash %s\n", body.MergeHash)
	case ledger.Leg:
		fmt.Fprintf(w, "base %s\n", body.Base)
		fmt.Fprintf(w, "head %s\n", body.Head)
		fmt.Fprintf(w, "backend %s\n", body.Backend)
		fmt.Fprintf(w, "tests %v\n", body.Tests)
		printLegRun(w, "a", body.A)
		printLegRun(w, "b", body.B)
		printLegRun(w, "c", body.C)
		if faults := body.Faults(); len(faults) > 0 {
			for _, f := range faults {
				fmt.Fprintf(w, "fault %s\n", f)
			}
		}
	case ledger.Verdict:
		fmt.Fprintf(w, "seat %s\n", body.Seat)
		fmt.Fprintf(w, "hash %s\n", body.Hash)
		fmt.Fprintf(w, "model %s\n", body.Model)
		fmt.Fprintf(w, "approve %v\n", body.Approve)
		fmt.Fprintf(w, "coverage %s\n", body.Coverage)
		fmt.Fprintf(w, "matrix %s\n", body.Matrix)
		for _, item := range body.Scope {
			fmt.Fprintf(w, "scope %s#%s: %s\n", rec.ID, item.ID, item.Text)
		}
	case ledger.Ruling:
		fmt.Fprintf(w, "ruling-kind %s\n", body.Kind)
		fmt.Fprintf(w, "item %s\n", body.Item)
		fmt.Fprintf(w, "text %s\n", body.Text)
		if body.Seat != ledger.SeatNone {
			fmt.Fprintf(w, "seat %s\n", body.Seat)
		}
		if len(body.To) > 0 {
			fmt.Fprintf(w, "to %v\n", body.To)
		}
	case ledger.Lint:
		fmt.Fprintf(w, "lint-kind %s\n", body.Kind)
		fmt.Fprintf(w, "input %s\n", body.Input)
		fmt.Fprintf(w, "model %s\n", body.Model)
		for _, a := range body.Answers {
			fmt.Fprintf(w, "answer %s: question=%q p=%v choice=%q confidence=%v threshold=%v flagged=%v\n", a.Lint, a.Question, a.P, a.Choice, a.Confidence, a.Threshold, a.Flagged)
		}
	}
}

func printLegRun(w io.Writer, name string, r ledger.LegRun) {
	fmt.Fprintf(w, "leg %s result: want=%s got=%s exit=%d executed=%d counted=%v duration=%s kill-reason=%q cmd=%q\n", name, r.Want, r.Got, r.ExitCode, r.Executed, r.Counted, r.Duration, r.KillReason, r.Cmd)
}
