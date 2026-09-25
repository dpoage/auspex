package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dpoage/auspex/internal/ledger"
	"github.com/dpoage/auspex/internal/legs"
)

// observe runs every mutant leg for one or more mutant/test pairs.
//
//	auspex observe --slice <bead> --base <rev> --head <rev> --mutant <patch> --test <path>... [--mutant ... --test ...]
func init() {
	register(command{
		name:     "observe",
		synopsis: "run the mutant legs for one or more mutant/test pairs",
		run:      runObserve,
	})
}

// pairFlags accumulates --mutant and --test flags in flag.Parse call order.
// Each --mutant starts a new pair; --test entries up to the next --mutant
// belong to the current pair.
type pairFlags struct {
	mutants []string
	tests   [][]string
}

func (p *pairFlags) addMutant(v string) {
	p.mutants = append(p.mutants, v)
	p.tests = append(p.tests, nil)
}

func (p *pairFlags) addTest(v string) error {
	if len(p.mutants) == 0 {
		return fmt.Errorf("--test %q given before any --mutant", v)
	}
	i := len(p.mutants) - 1
	p.tests[i] = append(p.tests[i], v)
	return nil
}

type mutantFlag struct{ p *pairFlags }

func (mutantFlag) String() string       { return "" }
func (f mutantFlag) Set(v string) error { f.p.addMutant(v); return nil }

type testFlag struct{ p *pairFlags }

func (testFlag) String() string       { return "" }
func (f testFlag) Set(v string) error { return f.p.addTest(v) }

func runObserve(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("observe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: auspex observe --slice <bead> --base <rev> --head <rev> --mutant <patch> --test <path>... [--mutant ... --test ...]")
		fs.PrintDefaults()
	}
	slice := fs.String("slice", "", "slice bead id")
	base := fs.String("base", "", "base revision")
	head := fs.String("head", "", "head revision")
	var pf pairFlags
	fs.Var(mutantFlag{&pf}, "mutant", "mutant patch file (repeatable; starts a new pair)")
	fs.Var(testFlag{&pf}, "test", "test path restored from base for the current pair (repeatable)")
	if err := fs.Parse(args); err != nil {
		return exitUsage // flag printed the error and fs.Usage
	}
	usageError := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "auspex observe: "+format+"\n", a...)
		fs.Usage()
		return exitUsage
	}
	if fs.NArg() != 0 {
		return usageError("unexpected arguments %q", fs.Args())
	}
	if *slice == "" || *base == "" || *head == "" {
		return usageError("--slice, --base, and --head are required")
	}
	if len(pf.mutants) == 0 {
		return usageError("at least one --mutant/--test pair is required")
	}
	for i, tests := range pf.tests {
		if len(tests) == 0 {
			return usageError("--mutant %q has no --test paths", pf.mutants[i])
		}
	}

	r, err := openRepo()
	if err != nil {
		return obnuntiatio(stderr, "observe: no repository here", []string{err.Error()})
	}
	baseHash, err := r.Resolve(*base)
	if err != nil {
		return usageError("--base: %v", err)
	}
	headHash, err := r.Resolve(*head)
	if err != nil {
		return usageError("--head: %v", err)
	}

	cfg, err := legs.LoadConfig(filepath.Join(r.Root(), ".agents", "auspex.toml"))
	if err != nil {
		return obnuntiatio(stderr, "observe: cannot load the repository configuration", []string{err.Error()})
	}

	led, err := openLedger()
	if err != nil {
		return obnuntiatio(stderr, "observe: cannot open the ledger", []string{err.Error()})
	}
	if err := led.Appointed(ledger.SliceID(*slice)); err != nil {
		return obnuntiatio(stderr, "observe: the slice is not appointed", []string{err.Error()})
	}

	store, err := openEvidence()
	if err != nil {
		return obnuntiatio(stderr, "observe: cannot open the evidence store", []string{err.Error()})
	}

	pairs := make([]legs.Pair, len(pf.mutants))
	for i, path := range pf.mutants {
		patch, err := os.ReadFile(path)
		if err != nil {
			return usageError("--mutant %q: %v", path, err)
		}
		pairs[i] = legs.Pair{MutantPatch: patch, Tests: pf.tests[i]}
	}

	sb, backend := newSandbox()
	results, err := legs.Run(context.Background(), sb, backend, cfg, r, store, baseHash, headHash, pairs)
	if err != nil {
		return obnuntiatio(stderr, "observe failed", []string{err.Error()})
	}

	var faults []string
	for _, leg := range results {
		rec, err := led.Append(ledger.SliceID(*slice), leg)
		if err != nil {
			return obnuntiatio(stderr, "observe: recording a leg failed", []string{err.Error()})
		}
		fmt.Fprintln(stdout, rec.ID)
		for _, f := range leg.Faults() {
			faults = append(faults, fmt.Sprintf("%s: %s", rec.ID, f))
		}
	}
	if len(faults) > 0 {
		return obnuntiatio(stderr, "observe found faulty legs", faults)
	}
	return exitSuccess
}
