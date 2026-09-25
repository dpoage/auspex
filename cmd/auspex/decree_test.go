package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dpoage/auspex/internal/ledger"
)

// TestDecreeFlagValidation defends D1: --to is required for contract and
// refused for other kinds; --seat A|B is required for triage and refused for
// other kinds; an unknown kind is exit 2; nothing is recorded on error.
// Mutants: accept contract without --to; accept triage without --seat.
func TestDecreeFlagValidation(t *testing.T) {
	led, _ := gitBDEnv(t)
	bead := appointedBead(t, led, "r-1", "decree flags")
	other := appointedBead(t, led, "r-1", "decree contract target")

	cases := []struct {
		name string
		args []string
	}{
		{"contract without --to", []string{"--slice", bead, "--kind", "contract", "--item", "x", "--ruling", "text"}},
		{"scope with --to", []string{"--slice", bead, "--kind", "scope", "--item", "x", "--ruling", "text", "--to", other}},
		{"deviation with --to", []string{"--slice", bead, "--kind", "deviation", "--item", "x", "--ruling", "text", "--to", other}},
		{"triage without --seat", []string{"--slice", bead, "--kind", "triage", "--item", "x", "--ruling", "text"}},
		{"triage with --seat composition", []string{"--slice", bead, "--kind", "triage", "--item", "x", "--ruling", "text", "--seat", "composition"}},
		{"scope with --seat", []string{"--slice", bead, "--kind", "scope", "--item", "x", "--ruling", "text", "--seat", "A"}},
		{"deviation with --seat", []string{"--slice", bead, "--kind", "deviation", "--item", "x", "--ruling", "text", "--seat", "B"}},
		{"contract with --seat", []string{"--slice", bead, "--kind", "contract", "--item", "x", "--ruling", "text", "--seat", "A", "--to", other}},
		{"unknown kind", []string{"--slice", bead, "--kind", "colossal", "--item", "x", "--ruling", "text"}},
		{"whitespace-only --item", []string{"--slice", bead, "--kind", "scope", "--item", "  \t", "--ruling", "text"}},
		{"whitespace-only --ruling", []string{"--slice", bead, "--kind", "scope", "--item", "x", "--ruling", "   "}},
		{"contract with --to \"\"", []string{"--slice", bead, "--kind", "contract", "--item", "x", "--ruling", "text", "--to", ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runDecree(c.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("exit = %d, want 2: stdout %q stderr %q", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "usage") && !strings.Contains(stderr.String(), "auspex decree:") {
				t.Fatalf("stderr = %q, want usage text", stderr.String())
			}
		})
	}

	slices, err := led.Round("r-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range slices {
		if string(s.ID) == bead && len(s.Records) != 1 { // just the Appointment
			t.Fatalf("bead %s recorded %d records from refused decrees, want 1 (the appointment)", bead, len(s.Records))
		}
	}
}

// TestDecreeAcceptsValidRulings is the positive control for D1: a
// well-formed ruling of each kind records and round-trips intact.
func TestDecreeAcceptsValidRulings(t *testing.T) {
	led, _ := gitBDEnv(t)
	bead := appointedBead(t, led, "r-1", "decree valid")
	other := appointedBead(t, led, "r-1", "decree valid target")

	run := func(args []string) ledger.Ruling {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := runDecree(args, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("decree %q exited %d: %s", args, code, stderr.String())
		}
		recordID := ledger.RecordID(strings.TrimSpace(stdout.String()))
		rec, err := led.Get(recordID)
		if err != nil {
			t.Fatal(err)
		}
		rul, ok := rec.Body.(ledger.Ruling)
		if !ok {
			t.Fatalf("record body is %T, want Ruling", rec.Body)
		}
		return rul
	}

	if r := run([]string{"--slice", bead, "--kind", "scope", "--item", "abc/def#S1", "--ruling", "resolved"}); r.Kind != ledger.Scope || r.Item != "abc/def#S1" {
		t.Fatalf("scope ruling = %+v", r)
	}
	if r := run([]string{"--slice", bead, "--kind", "triage", "--item", "x", "--ruling", "ok", "--seat", "A"}); r.Kind != ledger.Triage || r.Seat != ledger.SeatA {
		t.Fatalf("triage ruling = %+v", r)
	}
	if r := run([]string{"--slice", bead, "--kind", "deviation", "--item", "x", "--ruling", "accepted"}); r.Kind != ledger.Deviation {
		t.Fatalf("deviation ruling = %+v", r)
	}
	if r := run([]string{"--slice", bead, "--kind", "contract", "--item", "x", "--ruling", "revised", "--to", other}); r.Kind != ledger.Contract || len(r.To) != 1 || string(r.To[0]) != other {
		t.Fatalf("contract ruling = %+v", r)
	}
}
