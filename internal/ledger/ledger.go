// Package ledger hides where round state lives. Callers never see that
// every record is one bead comment ("auspex:v1 " plus one JSON object), how
// records are encoded, which bd invocations read and write them, or that
// slice beads carry the label auspex-slice:<round>.
package ledger

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dpoage/auspex/internal/evidence"
	"github.com/dpoage/auspex/internal/repo"
)

// SliceID is a slice bead id, full or a unique prefix on input; records
// always carry the full id bd resolves it to.
type SliceID string

// RoundID names one oracle-gated round. Matches
// ^[A-Za-z0-9][A-Za-z0-9._-]{0,241}$: bd splits --label on commas, trims
// spaces, and caps labels at 255 characters (auspex-slice: plus 242), so
// the round id must fit in that budget.
type RoundID string

var validRound = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,241}$`)

// RecordID names one record: "<slice>/<12 random hex>".
type RecordID string

// Tier is a slice's review weight.
type Tier int

const (
	Light Tier = iota + 1
	Standard
	Heavy
)

// ParseTier reads the tier named in the appoint flags.
func ParseTier(s string) (Tier, error) {
	switch s {
	case "light":
		return Light, nil
	case "standard":
		return Standard, nil
	case "heavy":
		return Heavy, nil
	}
	return 0, fmt.Errorf("ledger: unknown tier %q (want light, standard, or heavy)", s)
}

func (t Tier) String() string {
	switch t {
	case Light:
		return "light"
	case Standard:
		return "standard"
	case Heavy:
		return "heavy"
	}
	return fmt.Sprintf("tier(%d)", int(t))
}

// Seats names the oracle seats this tier's gate requires.
func (t Tier) Seats() []Seat {
	if t == Light {
		return []Seat{SeatB}
	}
	return []Seat{SeatA, SeatB}
}

// Seat is an oracle seat in a round.
type Seat int

const (
	SeatNone Seat = iota
	SeatA
	SeatB
	SeatComposition
)

// ParseSeat reads a seat name; it never returns SeatNone.
func ParseSeat(s string) (Seat, error) {
	switch s {
	case "A", "a":
		return SeatA, nil
	case "B", "b":
		return SeatB, nil
	case "composition":
		return SeatComposition, nil
	}
	return SeatNone, fmt.Errorf("ledger: unknown seat %q (want A, B, or composition)", s)
}

func (s Seat) String() string {
	switch s {
	case SeatA:
		return "A"
	case SeatB:
		return "B"
	case SeatComposition:
		return "composition"
	}
	return "none"
}

// Outcome is a leg run's classified result.
type Outcome int

const (
	Green Outcome = iota + 1
	Red
	Killed
)

// Body is one recorded kind of round state. Sealed: exactly Appointment,
// Leg, Verdict, Ruling, and Lint implement it.
type Body interface{ body() }

// Appointment records a slice's round, tier, and (once known) merge hash. A
// later appointment supersedes an earlier one; both stay in the history.
type Appointment struct {
	Round     RoundID
	Tier      Tier
	MergeHash repo.Hash // "" until known
}

// LegRun is one leg's execution: what ran, what was expected, what happened.
type LegRun struct {
	Cmd        []string
	Want, Got  Outcome
	ExitCode   int
	KillReason string
	Executed   int
	Counted    bool
	Transcript evidence.Ref
	Duration   time.Duration
}

// Leg records the three legs run for one mutant. Leg (a): head with the
// mutant and the test paths restored from base, full suite, expected green.
// Leg (b): head with the mutant, the test's module, expected red. Leg (c):
// head with no mutant, full suite, expected green with a higher count.
type Leg struct {
	Base, Head repo.Hash
	Mutant     evidence.Ref
	Tests      []string
	Backend    string
	A, B, C    LegRun
}

// Faults is empty exactly when every leg has its expected result, legs
// (a), (b), and (c) were counted, and leg (c) executed more tests than leg
// (a) — the proof that CI collects a criterion's new test. One plain
// sentence per violation; a killed leg (b) names its kill reason.
func (l Leg) Faults() []string {
	var faults []string
	if l.A.Got != Green {
		faults = append(faults, "leg (a) did not pass")
	}
	switch {
	case l.B.Got == Killed:
		faults = append(faults, fmt.Sprintf("leg (b) was killed (%s), not run red", l.B.KillReason))
	case l.B.Got != Red:
		faults = append(faults, "leg (b) did not run red")
	}
	if l.C.Got != Green {
		faults = append(faults, "leg (c) did not pass")
	}
	if !l.A.Counted {
		faults = append(faults, "leg (a) recorded no executed-test count")
	}
	if !l.B.Counted {
		faults = append(faults, "leg (b) recorded no executed-test count")
	}
	if !l.C.Counted {
		faults = append(faults, "leg (c) recorded no executed-test count")
	}
	if l.C.Executed <= l.A.Executed {
		faults = append(faults, "leg (c) executed no more tests than leg (a)")
	}
	return faults
}

// ScopeItem is one SCOPE entry parsed from a verdict. ID is "S<k>" by
// position in the verdict; Text keeps the oracle's own label.
type ScopeItem struct {
	ID, Text string
}

// Verdict records one oracle's pronouncement, bound to the hash it judged.
type Verdict struct {
	Seat       Seat
	Hash       repo.Hash
	Model      string
	Approve    bool
	Coverage   string
	Matrix     string
	Scope      []ScopeItem
	Transcript evidence.Ref
}

// RulingKind is what a ruling rules on.
type RulingKind int

const (
	Scope RulingKind = iota + 1
	Triage
	Deviation
	Contract
)

// ParseRulingKind reads the --kind flag of decree.
func ParseRulingKind(s string) (RulingKind, error) {
	switch s {
	case "scope":
		return Scope, nil
	case "triage":
		return Triage, nil
	case "deviation":
		return Deviation, nil
	case "contract":
		return Contract, nil
	}
	return 0, fmt.Errorf("ledger: unknown ruling kind %q (want scope, triage, deviation, or contract)", s)
}

func (k RulingKind) String() string {
	switch k {
	case Scope:
		return "scope"
	case Triage:
		return "triage"
	case Deviation:
		return "deviation"
	case Contract:
		return "contract"
	}
	return fmt.Sprintf("ruling-kind(%d)", int(k))
}

// Ruling records a ruling. A triage names its seat (A or B); a contract
// revision names every slice it was re-issued to.
type Ruling struct {
	Kind RulingKind
	Item string
	Text string
	Seat Seat      // Triage only, A or B
	To   []SliceID // Contract only, non-empty
}

// LintKind is which judge-lint question set a lint record came from.
type LintKind int

const (
	Brief LintKind = iota + 1
	Fixlist
	Reply
	Blocker
)

// ParseLintKind reads a lint kind in the words omen takes on its command
// line: brief, fixlist, reply, or blocker.
func ParseLintKind(s string) (LintKind, error) {
	switch s {
	case "brief":
		return Brief, nil
	case "fixlist":
		return Fixlist, nil
	case "reply":
		return Reply, nil
	case "blocker":
		return Blocker, nil
	}
	return 0, fmt.Errorf("ledger: unknown lint kind %q", s)
}

func (k LintKind) String() string {
	switch k {
	case Brief:
		return "brief"
	case Fixlist:
		return "fixlist"
	case Reply:
		return "reply"
	case Blocker:
		return "blocker"
	}
	return fmt.Sprintf("lint-kind(%d)", int(k))
}

// Answer is one judge answer: a probability or a choice with its
// confidence, the threshold the lint flags at, and whether it flagged.
type Answer struct {
	Lint       string
	Question   string
	P          float64
	Choice     string
	Confidence float64
	Threshold  float64
	Flagged    bool
}

// Lint records one judge-lint run over an input.
type Lint struct {
	Kind    LintKind
	Input   evidence.Ref
	Model   string
	Answers []Answer
}

func (Appointment) body() {}
func (Leg) body()         {}
func (Verdict) body()     {}
func (Ruling) body()      {}
func (Lint) body()        {}

// Record is one appended record: its id, its slice bead, when it was
// appended, and its body.
type Record struct {
	ID    RecordID
	Slice SliceID
	At    time.Time
	Body  Body
}

// EvidenceRef is one evidence ref a record body stores, labeled by its role.
type EvidenceRef struct {
	Role string
	Ref  evidence.Ref
}

// Evidence returns every evidence ref the record's body stores, labeled by
// role, in a fixed order: for a Leg, "mutant", "leg a", "leg b", "leg c";
// for a Verdict, "transcript"; for a Lint, "lint input". An empty ref is
// listed like any other. An Appointment and a Ruling store none.
func (r Record) Evidence() []EvidenceRef {
	switch b := r.Body.(type) {
	case Leg:
		return []EvidenceRef{
			{"mutant", b.Mutant},
			{"leg a", b.A.Transcript},
			{"leg b", b.B.Transcript},
			{"leg c", b.C.Transcript},
		}
	case Verdict:
		return []EvidenceRef{{"transcript", b.Transcript}}
	case Lint:
		return []EvidenceRef{{"lint input", b.Input}}
	}
	return nil
}

// Slice is one slice bead of a round: its latest appointment (nil before
// any) and every record in time order.
type Slice struct {
	ID          SliceID
	Appointment *Appointment
	Records     []Record
}

// Ledger is the round-state store backed by bead comments.
type Ledger struct {
	dir string
	now func() time.Time
}

// New returns the ledger for the bd database found from dir. now stamps
// every record; main passes time.Now, tests inject a clock.
func New(dir string, now func() time.Time) *Ledger {
	return &Ledger{dir: dir, now: now}
}

const commentPrefix = "auspex:v1 "

const kindAppointment = "appointment"
const kindLeg = "leg"
const kindVerdict = "verdict"
const kindRuling = "ruling"
const kindLint = "lint"

// Append writes one record as one bead comment on slice and returns it with
// ID and At set. The first bd call resolves the bead: a missing bead is an
// error, and the record carries the full bead id bd returns. An Appointment
// also adds the label auspex-slice:<round>. Any other kind on a bead with no
// auspex-slice:* label is an error, as are an Appointment whose round id is
// invalid and any body whose encoding would not read back equal. Every
// check, and staging the comment text, happens before bd writes anything;
// an error there writes nothing. If bd fails to add the comment after adding
// an Appointment's label, the bead stays labeled with no appointment.
func (l *Ledger) Append(slice SliceID, b Body) (Record, error) {
	if b == nil {
		return Record{}, fmt.Errorf("ledger: no record body")
	}
	bead, labels, err := l.showBead(slice)
	if err != nil {
		return Record{}, err
	}
	switch body := b.(type) {
	case Appointment:
		if !validRound.MatchString(string(body.Round)) {
			return Record{}, fmt.Errorf("ledger: round id %q does not match %s", body.Round, validRound)
		}
	case Ruling:
		if err := requireAppointed(bead, labels); err != nil {
			return Record{}, err
		}
		resolved, err := l.resolveRuling(body)
		if err != nil {
			return Record{}, err
		}
		b = resolved
	default:
		if err := requireAppointed(bead, labels); err != nil {
			return Record{}, err
		}
	}
	rec := Record{
		ID:    RecordID(bead + "/" + randHex12()),
		Slice: SliceID(bead),
		At:    l.now().UTC(),
		Body:  b,
	}
	text, err := encodeVerified(rec)
	if err != nil {
		return Record{}, fmt.Errorf("ledger: refusing a %s record that would not read back: %w", KindOf(b), err)
	}
	staged, err := stageComment(commentPrefix + text)
	if err != nil {
		return Record{}, err
	}
	defer os.Remove(staged)
	if a, ok := b.(Appointment); ok {
		if err := l.run("label", "add", bead, "auspex-slice:"+string(a.Round)); err != nil {
			return Record{}, err
		}
	}
	if err := l.run("comments", "add", bead, "-f", staged); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// Appointed returns nil when slice names a bead that has been appointed, and
// otherwise the error Append would give a non-Appointment record for it: a
// missing bead, or a bead that was never appointed.
func (l *Ledger) Appointed(slice SliceID) error {
	bead, labels, err := l.showBead(slice)
	if err != nil {
		return err
	}
	return requireAppointed(bead, labels)
}

func requireAppointed(bead string, labels []string) error {
	if !hasSliceLabel(labels) {
		return fmt.Errorf("ledger: bead %s is not appointed (no auspex-slice:* label); run auspex appoint first", bead)
	}
	return nil
}

// stageComment writes text to a temp file for bd comments add -f: argv
// is capped at 128 KiB, so the staged path is the only safe channel.
// Returns an absolute path; the caller removes the file.
func stageComment(text string) (string, error) {
	f, err := os.CreateTemp("", "auspex-record-*")
	if err != nil {
		return "", fmt.Errorf("ledger: %w", err)
	}
	path, err := filepath.Abs(f.Name())
	if err == nil {
		_, err = f.WriteString(text)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("ledger: %w", err)
	}
	return path, nil
}

// checkRuling enforces Ruling field rules: a triage names seat A or B and
// takes no --to; a contract names at least one --to slice and takes no seat;
// every other kind takes neither.
func checkRuling(r Ruling) error {
	switch r.Kind {
	case Triage:
		if r.Seat != SeatA && r.Seat != SeatB {
			return fmt.Errorf("ledger: a triage ruling names seat A or B")
		}
		if r.To != nil {
			return fmt.Errorf("ledger: only a contract ruling takes --to")
		}
	case Contract:
		if r.Seat != SeatNone {
			return fmt.Errorf("ledger: only a triage ruling takes a seat")
		}
		if len(r.To) == 0 {
			return fmt.Errorf("ledger: a contract ruling names at least one slice with --to")
		}
	default:
		if r.Seat != SeatNone {
			return fmt.Errorf("ledger: only a triage ruling takes a seat")
		}
		if r.To != nil {
			return fmt.Errorf("ledger: only a contract ruling takes --to")
		}
	}
	return nil
}

// resolveRuling validates r and, for a contract ruling, resolves every To
// id the way bd does, so the record carries full bead ids. It writes
// nothing.
func (l *Ledger) resolveRuling(r Ruling) (Ruling, error) {
	if err := checkRuling(r); err != nil {
		return Ruling{}, err
	}
	if r.Kind != Contract {
		return r, nil
	}
	resolved := make([]SliceID, 0, len(r.To))
	for _, to := range r.To {
		bead, _, err := l.showBead(to)
		if err != nil {
			return Ruling{}, err
		}
		resolved = append(resolved, SliceID(bead))
	}
	r.To = resolved
	return r, nil
}

// Round returns every slice of the round -- every bead labeled
// auspex-slice:<id>, open or closed, with no list limit -- each with its
// records in time order and its latest appointment to this round. A
// prefixed comment that does not decode, or names an unknown kind, is an
// error; comments without the prefix are ignored. An invalid round id is an
// error.
func (l *Ledger) Round(id RoundID) ([]Slice, error) {
	if !validRound.MatchString(string(id)) {
		return nil, fmt.Errorf("ledger: round id %q does not match %s", id, validRound)
	}
	out, err := l.capture("list", "--json", "--all", "-n", "0", "--label", "auspex-slice:"+string(id))
	if err != nil {
		return nil, err
	}
	var beads []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &beads); err != nil {
		return nil, fmt.Errorf("ledger: bd list: %w", err)
	}
	ids := make([]SliceID, 0, len(beads))
	for _, bead := range beads {
		ids = append(ids, SliceID(bead.ID))
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	slices := make([]Slice, 0, len(ids))
	for _, sid := range ids {
		records, err := l.comments(sid)
		if err != nil {
			return nil, err
		}
		s := Slice{ID: sid, Records: records}
		var latest time.Time
		for _, r := range records {
			if a, ok := r.Body.(Appointment); ok && a.Round == id {
				if s.Appointment == nil || r.At.After(latest) {
					s.Appointment = &a
					latest = r.At
				}
			}
		}
		slices = append(slices, s)
	}
	return slices, nil
}

// Get returns one record by id. Comments on its slice that carry the prefix
// but fail to decode are errors; comments without the prefix are ignored.
func (l *Ledger) Get(id RecordID) (Record, error) {
	s := string(id)
	i := strings.LastIndex(s, "/")
	if i <= 0 || i == len(s)-1 {
		return Record{}, fmt.Errorf("ledger: malformed record id %q (want <slice>/<12 hex>)", s)
	}
	records, err := l.comments(SliceID(s[:i]))
	if err != nil {
		return Record{}, err
	}
	for _, r := range records {
		if r.ID == id {
			return r, nil
		}
	}
	return Record{}, fmt.Errorf("ledger: no record %s", s)
}

// comments reads one bead's comments and decodes every auspex record among
// them, in time order.
func (l *Ledger) comments(slice SliceID) ([]Record, error) {
	out, err := l.capture("comments", string(slice), "--json")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("ledger: bd comments: %w", err)
	}
	var records []Record
	for _, c := range raw {
		text, ok := strings.CutPrefix(c.Text, commentPrefix)
		if !ok {
			continue
		}
		r, err := decode(text)
		if err != nil {
			return nil, fmt.Errorf("ledger: bead %s has an unreadable auspex record: %w", slice, err)
		}
		r.Slice = slice
		records = append(records, r)
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].At.Before(records[j].At) })
	return records, nil
}

// showBead resolves a slice id to the full bead id bd returns and the bead's
// labels. A missing bead is an error.
func (l *Ledger) showBead(slice SliceID) (string, []string, error) {
	out, err := l.capture("show", string(slice), "--json")
	if err != nil {
		return "", nil, err
	}
	var beads []struct {
		ID     string   `json:"id"`
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal(out, &beads); err != nil {
		return "", nil, fmt.Errorf("ledger: bd show: %w", err)
	}
	if len(beads) != 1 {
		return "", nil, fmt.Errorf("ledger: no bead matches %q", string(slice))
	}
	return beads[0].ID, beads[0].Labels, nil
}

func hasSliceLabel(labels []string) bool {
	for _, l := range labels {
		if strings.HasPrefix(l, "auspex-slice:") {
			return true
		}
	}
	return false
}

func randHex12() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("ledger: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// run and capture execute bd in the ledger's directory. The only write
// subcommands are label add and comments add.
func (l *Ledger) run(args ...string) error {
	_, err := l.capture(args...)
	return err
}

func (l *Ledger) capture(args ...string) ([]byte, error) {
	cmd := exec.Command("bd", args...)
	cmd.Dir = l.dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("ledger: bd %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return nil, fmt.Errorf("ledger: bd %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}
