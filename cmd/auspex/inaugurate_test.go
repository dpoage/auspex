package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dpoage/auspex/internal/evidence"
	"github.com/dpoage/auspex/internal/ledger"
)

// TestInaugurateCP2ProsperaOnHoldingRound is a wiring smoke test: a round
// whose one slice carries an appointment with a merge hash and a bound
// APPROVE from the tier's one required seat passes cp2.
func TestInaugurateCP2ProsperaOnHoldingRound(t *testing.T) {
	_, dir := gitBDEnv(t)
	led := ledger.New(dir, time.Now)
	bead := createBead(t, "holding slice")
	hash := fullHead(t)
	if _, err := led.Append(ledger.SliceID(bead), ledger.Appointment{Round: "r-hold", Tier: ledger.Light, MergeHash: hash}); err != nil {
		t.Fatal(err)
	}
	store, err := evidence.Open()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put([]byte("the seat B verdict transcript"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := led.Append(ledger.SliceID(bead), ledger.Verdict{Seat: ledger.SeatB, Hash: hash, Approve: true, Model: "m", Transcript: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runInaugurate([]string{"cp2", "--round", "r-hold"}, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("cp2 exited %d, want 0: stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "auspicia prospera: cp2 holds for round r-hold (1 slices, 2 records)") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

// TestInaugurateCP2HeadlineCountsRoundRecords: on a bead appointed to an
// earlier round and then to this one, the prospera headline counts only
// this round's records. Mutant: count every record on the slice.
func TestInaugurateCP2HeadlineCountsRoundRecords(t *testing.T) {
	_, dir := gitBDEnv(t)
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	led := ledger.New(dir, clockAt(t, base, base.Add(time.Hour), base.Add(2*time.Hour), base.Add(3*time.Hour)))
	bead := createBead(t, "reused holding slice")
	hash := fullHead(t)
	store, err := evidence.Open()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put([]byte("a seat B verdict transcript"))
	if err != nil {
		t.Fatal(err)
	}
	approve := ledger.Verdict{Seat: ledger.SeatB, Hash: hash, Approve: true, Model: "m", Transcript: ref}
	for _, b := range []ledger.Body{
		ledger.Appointment{Round: "r-old", Tier: ledger.Light, MergeHash: hash},
		approve,
		ledger.Appointment{Round: "r-new", Tier: ledger.Light, MergeHash: hash},
		approve,
	} {
		if _, err := led.Append(ledger.SliceID(bead), b); err != nil {
			t.Fatalf("Append(%T): %v", b, err)
		}
	}

	var stdout, stderr bytes.Buffer
	if code := runInaugurate([]string{"cp2", "--round", "r-new"}, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("cp2 exited %d, want 0: stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if want := "auspicia prospera: cp2 holds for round r-new (1 slices, 2 records)\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

// TestInaugurateCP2ObnuntiatioNamesSliceAndItem: a round missing the
// required APPROVE fails cp2, naming the slice and the item on stderr,
// and exits nonzero.
func TestInaugurateCP2ObnuntiatioNamesSliceAndItem(t *testing.T) {
	_, dir := gitBDEnv(t)
	led := ledger.New(dir, time.Now)
	bead := createBead(t, "unapproved slice")
	hash := fullHead(t)
	if _, err := led.Append(ledger.SliceID(bead), ledger.Appointment{Round: "r-fail", Tier: ledger.Light, MergeHash: hash}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runInaugurate([]string{"cp2", "--round", "r-fail"}, &stdout, &stderr); code != exitObnuntiatio {
		t.Fatalf("cp2 exited %d, want 1: stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "obnuntiatio: cp2 fails for round r-fail") {
		t.Fatalf("stderr = %q, want the obnuntiatio headline", stderr.String())
	}
	want := "slice " + bead + ": seat B has no APPROVE bound to " + string(hash)
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr = %q, want a line %q", stderr.String(), want)
	}
}

// TestInaugurateCP2FailsClosed is criterion C9: any ledger error, a round
// with zero slices, and a missing or corrupt transcript each print
// obnuntiatio and exit nonzero -- never a false pass. Mutant: treat a Get
// error as a pass.
func TestInaugurateCP2FailsClosed(t *testing.T) {
	t.Run("ledger error", func(t *testing.T) {
		gitBDEnv(t)
		var stdout, stderr bytes.Buffer
		// "a b" fails ledger's round-id rule (^[A-Za-z0-9][A-Za-z0-9._-]*$),
		// so Round itself returns an error before any slice is examined.
		if code := runInaugurate([]string{"cp2", "--round", "a b"}, &stdout, &stderr); code != exitObnuntiatio {
			t.Fatalf("cp2 exited %d, want 1: stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "obnuntiatio") {
			t.Fatalf("stderr = %q, want an obnuntiatio headline", stderr.String())
		}
	})

	t.Run("zero slices", func(t *testing.T) {
		gitBDEnv(t)
		var stdout, stderr bytes.Buffer
		if code := runInaugurate([]string{"cp2", "--round", "r-empty"}, &stdout, &stderr); code != exitObnuntiatio {
			t.Fatalf("cp2 exited %d, want 1: stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "round has no slices") {
			t.Fatalf("stderr = %q, want it to name the round has no slices", stderr.String())
		}
	})

	t.Run("evidence missing or corrupt", func(t *testing.T) {
		_, dir := gitBDEnv(t)
		led := ledger.New(dir, time.Now)
		bead := createBead(t, "corrupt transcript slice")
		hash := fullHead(t)
		store, err := evidence.Open()
		if err != nil {
			t.Fatal(err)
		}
		ref, err := store.Put([]byte("a verdict transcript body long enough to flip a byte in"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := led.Append(ledger.SliceID(bead), ledger.Appointment{Round: "r-corrupt", Tier: ledger.Light, MergeHash: hash}); err != nil {
			t.Fatal(err)
		}
		if _, err := led.Append(ledger.SliceID(bead), ledger.Verdict{Seat: ledger.SeatB, Hash: hash, Approve: true, Transcript: ref}); err != nil {
			t.Fatal(err)
		}
		path := store.Path(ref)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body[0] ^= 0xff
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}

		var stdout, stderr bytes.Buffer
		if code := runInaugurate([]string{"cp2", "--round", "r-corrupt"}, &stdout, &stderr); code != exitObnuntiatio {
			t.Fatalf("cp2 exited %d, want 1: stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), string(ref)) {
			t.Fatalf("stderr = %q, want it to name the corrupt ref %s", stderr.String(), ref)
		}
	})
}

// TestInaugurateUsageErrors: an unknown checkpoint, a missing --round, and
// unexpected arguments are exit 2 usage errors.
func TestInaugurateUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"cp3", "--round", "r-1"},
		{"cp2"},
		{"cp2", "--round", "r-1", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if code := runInaugurate(args, &stdout, &stderr); code != exitUsage {
			t.Errorf("runInaugurate(%v) = %d, want 2 (stderr %q)", args, code, stderr.String())
		}
	}
}
