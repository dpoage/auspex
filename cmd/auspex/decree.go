package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/dpoage/auspex/internal/ledger"
)

// decree records a ruling on a SCOPE item, a loop-cap triage, a deviation,
// or a contract ruling. A triage names its seat with --seat; a contract
// ruling names the slices it is addressed to with --to.
//
//	auspex decree --slice <bead> --kind <scope|triage|deviation|contract> --item <id> --ruling <text> [--seat <A|B>] [--to <bead>...]
func init() {
	register(command{
		name:     "decree",
		synopsis: "record a ruling on a scope item, triage, deviation, or contract",
		run:      runDecree,
	})
}

// sliceIDList collects repeated --to flags in order.
type sliceIDList []ledger.SliceID

func (l *sliceIDList) String() string {
	if l == nil {
		return ""
	}
	return fmt.Sprint([]ledger.SliceID(*l))
}

// Set refuses an empty or whitespace-only id, which flag reports as a usage
// error.
func (l *sliceIDList) Set(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("a slice bead id, not empty")
	}
	*l = append(*l, ledger.SliceID(s))
	return nil
}

func runDecree(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("decree", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: auspex decree --slice <bead> --kind <scope|triage|deviation|contract> --item <id> --ruling <text> [--seat <A|B>] [--to <bead>...]")
		fs.PrintDefaults()
	}
	slice := fs.String("slice", "", "slice bead id")
	kindFlag := fs.String("kind", "", "scope, triage, deviation, or contract")
	item := fs.String("item", "", "the item this ruling rules on")
	rulingText := fs.String("ruling", "", "the ruling's text")
	seatFlag := fs.String("seat", "", "A or B (triage only)")
	var to sliceIDList
	fs.Var(&to, "to", "slices this contract ruling is addressed to (consumers for a revision; the owner for an acknowledgement)")
	if err := fs.Parse(args); err != nil {
		return exitUsage // flag printed the error and fs.Usage
	}
	usageError := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "auspex decree: "+format+"\n", a...)
		fs.Usage()
		return exitUsage
	}
	if fs.NArg() != 0 {
		return usageError("unexpected arguments %q", fs.Args())
	}
	if *slice == "" || *kindFlag == "" || strings.TrimSpace(*item) == "" || strings.TrimSpace(*rulingText) == "" {
		return usageError("--slice, --kind, --item, and --ruling are required")
	}
	kind, err := ledger.ParseRulingKind(*kindFlag)
	if err != nil {
		return usageError("%v", err)
	}
	seat := ledger.SeatNone
	if *seatFlag != "" {
		seat, err = ledger.ParseSeat(*seatFlag)
		if err != nil {
			return usageError("%v", err)
		}
	}
	switch kind {
	case ledger.Triage:
		if seat != ledger.SeatA && seat != ledger.SeatB {
			return usageError("--seat A or B is required for a triage ruling")
		}
		if len(to) != 0 {
			return usageError("--to is only for a contract ruling")
		}
	case ledger.Contract:
		if seat != ledger.SeatNone {
			return usageError("--seat is only for a triage ruling")
		}
		if len(to) == 0 {
			return usageError("--to is required for a contract ruling")
		}
	default: // Scope, Deviation
		if seat != ledger.SeatNone {
			return usageError("--seat is only for a triage ruling")
		}
		if len(to) != 0 {
			return usageError("--to is only for a contract ruling")
		}
	}

	led, err := openLedger()
	if err != nil {
		return obnuntiatio(stderr, "decree: cannot open the ledger", []string{err.Error()})
	}
	rec, err := led.Append(ledger.SliceID(*slice), ledger.Ruling{
		Kind: kind,
		Item: *item,
		Text: *rulingText,
		Seat: seat,
		To:   to,
	})
	if err != nil {
		return obnuntiatio(stderr, "decree failed", []string{err.Error()})
	}
	fmt.Fprintln(stdout, rec.ID)
	return exitSuccess
}
