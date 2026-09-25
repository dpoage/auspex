package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dpoage/auspex/internal/ledger"
	"github.com/dpoage/auspex/internal/lint"
)

// omen asks a judge-lint question over a brief, fix list, reply, or
// blocker and records the answers. A flag is advice: omen exits 0 after
// recording regardless of whether anything flagged, and nonzero only on an
// error, in which case nothing is recorded.
//
//	auspex omen <brief|fixlist|reply|blocker> --slice <bead> <file|->
func init() {
	register(command{
		name:     "omen",
		synopsis: "run a judge lint over a brief, fix list, reply, or blocker (advice only)",
		run:      runOmen,
	})
}

// judgeFactory constructs the decide client omen judges through. It is
// main.go's newJudge in production; tests substitute a client pointed at an
// httptest server via decide.Config.BaseURL so the judge lints run
// hermetically.
var judgeFactory = newJudge

const omenUsage = "usage: auspex omen <brief|fixlist|reply|blocker> --slice <bead> <file|->"

func runOmen(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, omenUsage)
		return exitUsage
	}
	kind, err := ledger.ParseLintKind(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "auspex omen: unknown kind %q\n", args[0])
		fmt.Fprintln(stderr, omenUsage)
		return exitUsage
	}

	fs := flag.NewFlagSet("omen "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, omenUsage)
		fs.PrintDefaults()
	}
	slice := fs.String("slice", "", "slice bead id")
	if err := fs.Parse(args[1:]); err != nil {
		return exitUsage // flag printed the error and fs.Usage
	}
	usageError := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "auspex omen: "+format+"\n", a...)
		fs.Usage()
		return exitUsage
	}
	if fs.NArg() != 1 {
		return usageError("exactly one file argument (or -) is required, got %d", fs.NArg())
	}
	if *slice == "" {
		return usageError("--slice is required")
	}

	text, err := readOmenInput(fs.Arg(0))
	if err != nil {
		return usageError("%v", err)
	}
	if strings.TrimSpace(text) == "" {
		return usageError("input %s is empty: there is nothing to judge", fs.Arg(0))
	}

	// Skill routing judges the whole brief against every installed skill;
	// other kinds ask no skill questions.
	var skills []lint.Skill
	if kind == ledger.Brief {
		r, err := openRepo()
		if err != nil {
			return obnuntiatio(stderr, "omen: no repository here", []string{err.Error()})
		}
		if skills, err = lint.LoadSkills(r.Root()); err != nil {
			return obnuntiatio(stderr, "omen: cannot load installed skills", []string{err.Error()})
		}
	}

	judge, err := judgeFactory()
	if err != nil {
		return obnuntiatio(stderr, "omen: no judge available", []string{err.Error()})
	}

	result, err := lint.Ask(context.Background(), judge, kind, text, skills)
	if err != nil {
		return obnuntiatio(stderr, "omen: judge failed", []string{err.Error()})
	}

	store, err := openEvidence()
	if err != nil {
		return obnuntiatio(stderr, "omen: cannot open the evidence store", []string{err.Error()})
	}
	ref, err := store.Put([]byte(text))
	if err != nil {
		return obnuntiatio(stderr, "omen: cannot store the input", []string{err.Error()})
	}
	result.Input = ref

	led, err := openLedger()
	if err != nil {
		return obnuntiatio(stderr, "omen: cannot open the ledger", []string{err.Error()})
	}
	rec, err := led.Append(ledger.SliceID(*slice), result)
	if err != nil {
		return obnuntiatio(stderr, "omen failed", []string{err.Error()})
	}

	fmt.Fprintln(stdout, rec.ID)
	for _, a := range result.Answers {
		printOmen(stdout, a)
	}
	return exitSuccess
}

// printOmen prints what the orchestrator must see about one answer. A
// blocker always prints its class, or that it is unclassified and needs the
// orchestrator's class; any other answer prints only when it flagged. P is
// the value compared with the threshold for every answer.
func printOmen(w io.Writer, a ledger.Answer) {
	switch {
	case a.Choice == lint.Unclassified:
		fmt.Fprintf(w, "omen: %s unclassified at %v (threshold %v): needs the orchestrator's class\n", a.Lint, a.P, a.Threshold)
	case a.Choice != "":
		fmt.Fprintf(w, "omen: %s %s at %v\n", a.Lint, a.Choice, a.P)
	case a.Flagged:
		fmt.Fprintf(w, "omen: %s flagged at %v (threshold %v)\n", a.Lint, a.P, a.Threshold)
	}
}

// readOmenInput reads "-" from stdin, or the named file otherwise.
func readOmenInput(path string) (string, error) {
	if path == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("reading stdin: %w", err)
		}
		return string(b), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return string(b), nil
}
