package main

import (
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/dpoage/auspex/internal/ledger"
	"github.com/dpoage/auspex/internal/repo"
)

// appoint records a slice's appointment: its round, tier, and (once known)
// the merge hash, and adds the slice label auspex-slice:<round>.
//
//	auspex appoint --slice <bead> --round <round-id> --tier <light|standard|heavy> [--merge-hash <rev>]
func init() {
	register(command{
		name:     "appoint",
		synopsis: "record a slice's tier and merge hash (adds the slice label)",
		run:      runAppoint,
	})
}

// roundID is the ledger's round-id rule (ledger.RoundID); appoint checks it
// first so that a bad id is a usage error rather than a ledger failure.
var roundID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,241}$`)

func runAppoint(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("appoint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: auspex appoint --slice <bead> --round <round-id> --tier <light|standard|heavy> [--merge-hash <rev>]")
		fs.PrintDefaults()
	}
	slice := fs.String("slice", "", "slice bead id")
	round := fs.String("round", "", "round id (letters, digits, '.', '_', '-'; starts with a letter or digit; at most 242 characters)")
	tier := fs.String("tier", "", "light, standard, or heavy")
	mergeHash := fs.String("merge-hash", "", "rev the slice will merge (optional)")
	if err := fs.Parse(args); err != nil {
		return exitUsage // flag printed the error and fs.Usage
	}
	usageError := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "auspex appoint: "+format+"\n", a...)
		fs.Usage()
		return exitUsage
	}
	if fs.NArg() != 0 {
		return usageError("unexpected arguments %q", fs.Args())
	}
	if *slice == "" || *round == "" || *tier == "" {
		return usageError("--slice, --round, and --tier are required")
	}
	if !roundID.MatchString(*round) {
		return usageError("round id %q does not match %s", *round, roundID)
	}
	t, err := ledger.ParseTier(*tier)
	if err != nil {
		return usageError("%v", err)
	}
	mergeHashSet := false
	fs.Visit(func(f *flag.Flag) { mergeHashSet = mergeHashSet || f.Name == "merge-hash" })
	var hash string
	if mergeHashSet {
		if strings.TrimSpace(*mergeHash) == "" {
			return usageError("--merge-hash is empty; omit the flag when the merge hash is not known yet")
		}
		r, err := openRepo()
		if err != nil {
			return obnuntiatio(stderr, "appoint: no repository here", []string{err.Error()})
		}
		resolved, err := r.Resolve(*mergeHash)
		if err != nil {
			return usageError("%v", err)
		}
		hash = string(resolved)
	}
	led, err := openLedger()
	if err != nil {
		return obnuntiatio(stderr, "appoint: cannot open the ledger", []string{err.Error()})
	}
	rec, err := led.Append(ledger.SliceID(*slice), ledger.Appointment{
		Round:     ledger.RoundID(*round),
		Tier:      t,
		MergeHash: repo.Hash(hash),
	})
	if err != nil {
		return obnuntiatio(stderr, "appoint failed", []string{err.Error()})
	}
	fmt.Fprintln(stdout, rec.ID)
	return exitSuccess
}
