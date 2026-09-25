package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/dpoage/auspex/internal/check"
	"github.com/dpoage/auspex/internal/ledger"
)

// inaugurate checks a checkpoint against recorded round state.
//
//	auspex inaugurate cp2 --round <round-id>
func init() {
	register(command{
		name:     "inaugurate",
		synopsis: "check a checkpoint (cp2) against recorded round state",
		run:      runInaugurate,
	})
}

func runInaugurate(args []string, stdout, stderr io.Writer) int {
	usage := func() {
		fmt.Fprintln(stderr, "usage: auspex inaugurate cp2 --round <round-id>")
	}
	if len(args) == 0 {
		usage()
		return exitUsage
	}
	checkpoint, rest := args[0], args[1:]
	if checkpoint != "cp2" {
		fmt.Fprintf(stderr, "auspex inaugurate: unknown checkpoint %q (want cp2)\n", checkpoint)
		usage()
		return exitUsage
	}

	fs := flag.NewFlagSet("inaugurate cp2", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = usage
	round := fs.String("round", "", "round id")
	if err := fs.Parse(rest); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "auspex inaugurate cp2: unexpected arguments %q\n", fs.Args())
		usage()
		return exitUsage
	}
	if *round == "" {
		fmt.Fprintln(stderr, "auspex inaugurate cp2: --round is required")
		usage()
		return exitUsage
	}

	led, err := openLedger()
	if err != nil {
		return obnuntiatio(stderr, "inaugurate cp2: cannot open the ledger", []string{err.Error()})
	}
	id := ledger.RoundID(*round)
	slices, err := led.Round(id)
	if err != nil {
		return obnuntiatio(stderr, fmt.Sprintf("cp2 fails for round %s", *round), []string{err.Error()})
	}
	store, err := openEvidence()
	if err != nil {
		return obnuntiatio(stderr, "inaugurate cp2: cannot open the evidence store", []string{err.Error()})
	}

	failures := check.CP2(id, slices, store)
	if len(failures) == 0 {
		records := 0
		for _, s := range slices {
			records += len(check.RoundRecords(id, s))
		}
		prospera(stdout, "cp2 holds for round %s (%d slices, %d records)", *round, len(slices), records)
		return exitSuccess
	}
	lines := make([]string, len(failures))
	for i, f := range failures {
		lines[i] = f.String()
	}
	return obnuntiatio(stderr, fmt.Sprintf("cp2 fails for round %s", *round), lines)
}
