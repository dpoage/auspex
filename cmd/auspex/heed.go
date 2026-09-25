package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/dpoage/auspex/internal/ledger"
	"github.com/dpoage/auspex/internal/verdict"
)

// heed reads an oracle's payload (verdict, coverage, matrix, scope, reply),
// stores the transcript as evidence, and records the verdict bound to --hash.
//
//	auspex heed --slice <bead> --seat <A|B|composition> --hash <rev> --model <model> [--local <dir>] <transcript-file|->
func init() {
	register(command{
		name:     "heed",
		synopsis: "record an oracle verdict from a transcript",
		run:      runHeed,
	})
}

func runHeed(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("heed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: auspex heed --slice <bead> --seat <A|B|composition> --hash <rev> --model <model> [--local <dir>] <transcript-file|->")
		fs.PrintDefaults()
	}
	slice := fs.String("slice", "", "slice bead id")
	seatFlag := fs.String("seat", "", "A, B, or composition")
	hashFlag := fs.String("hash", "", "rev the verdict binds")
	model := fs.String("model", "", "the seat's resolved model")
	local := fs.String("local", "", "directory a local:// matrix path resolves against; when given, an APPROVE's matrix must resolve inside it after symlinks and may not be an absolute path")
	if err := fs.Parse(args); err != nil {
		return exitUsage // flag printed the error and fs.Usage
	}
	usageError := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "auspex heed: "+format+"\n", a...)
		fs.Usage()
		return exitUsage
	}
	if fs.NArg() != 1 {
		return usageError("want exactly one transcript-file argument, or - for stdin")
	}
	if *slice == "" || *seatFlag == "" || *hashFlag == "" || *model == "" {
		return usageError("--slice, --seat, --hash, and --model are required")
	}
	seat, err := ledger.ParseSeat(*seatFlag)
	if err != nil {
		return usageError("%v", err)
	}
	r, err := openRepo()
	if err != nil {
		return obnuntiatio(stderr, "heed: no repository here", []string{err.Error()})
	}
	hash, err := r.Resolve(*hashFlag)
	if err != nil {
		return usageError("%v", err)
	}

	var data []byte
	if fs.Arg(0) == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(fs.Arg(0))
	}
	if err != nil {
		return obnuntiatio(stderr, "heed: cannot read the transcript", []string{err.Error()})
	}

	parsed, err := verdict.Parse(data, *local)
	if err != nil {
		var nav *verdict.NotAVerdict
		if errors.As(err, &nav) {
			return obnuntiatio(stderr, "heed: not a verdict", []string{nav.Reason})
		}
		return obnuntiatio(stderr, "heed: cannot parse the transcript", []string{err.Error()})
	}

	store, err := openEvidence()
	if err != nil {
		return obnuntiatio(stderr, "heed: cannot open the evidence store", []string{err.Error()})
	}
	ref, err := store.Put(data)
	if err != nil {
		return obnuntiatio(stderr, "heed: cannot store the transcript", []string{err.Error()})
	}

	led, err := openLedger()
	if err != nil {
		return obnuntiatio(stderr, "heed: cannot open the ledger", []string{err.Error()})
	}
	rec, err := led.Append(ledger.SliceID(*slice), ledger.Verdict{
		Seat:       seat,
		Hash:       hash,
		Model:      *model,
		Approve:    parsed.Approve,
		Coverage:   parsed.Coverage,
		Matrix:     parsed.Matrix,
		Scope:      parsed.Scope,
		Transcript: ref,
	})
	if err != nil {
		return obnuntiatio(stderr, "heed failed", []string{err.Error()})
	}
	fmt.Fprintln(stdout, rec.ID)
	for _, item := range parsed.Scope {
		fmt.Fprintf(stdout, "%s#%s\n", rec.ID, item.ID)
	}
	return exitSuccess
}
