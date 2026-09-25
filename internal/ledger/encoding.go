package ledger

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/dpoage/auspex/internal/evidence"
	"github.com/dpoage/auspex/internal/repo"
)

// The comment encoding: the fixed prefix "auspex:v1 " followed by one JSON
// object {"id","kind","at",...}. The kind field discriminates which fields
// carry the body; every field is flattened onto the one object.

type scopeItemJSON struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type answerJSON struct {
	Lint       string  `json:"lint"`
	Question   string  `json:"question"`
	P          float64 `json:"p"`
	Choice     string  `json:"choice,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Threshold  float64 `json:"threshold"`
	Flagged    bool    `json:"flagged"`
}

type legRunJSON struct {
	Cmd        []string `json:"cmd,omitempty"`
	Want       string   `json:"want,omitempty"`
	Got        string   `json:"got,omitempty"`
	ExitCode   int      `json:"exit_code,omitempty"`
	KillReason string   `json:"kill_reason,omitempty"`
	Executed   int      `json:"executed,omitempty"`
	Counted    bool     `json:"counted"`
	Transcript string   `json:"transcript,omitempty"`
	DurationNS int64    `json:"duration_ns,omitempty"`
}

type recordJSON struct {
	ID   RecordID  `json:"id"`
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`

	// Appointment
	Round     string    `json:"round,omitempty"`
	Tier      string    `json:"tier,omitempty"`
	MergeHash repo.Hash `json:"merge_hash,omitempty"`

	// Leg
	Base    string      `json:"base,omitempty"`
	Head    string      `json:"head,omitempty"`
	Mutant  string      `json:"mutant,omitempty"`
	Tests   []string    `json:"tests,omitempty"`
	Backend string      `json:"backend,omitempty"`
	A       *legRunJSON `json:"a,omitempty"`
	B       *legRunJSON `json:"b,omitempty"`
	C       *legRunJSON `json:"c,omitempty"`

	// Verdict
	Seat       string          `json:"seat,omitempty"`
	Hash       string          `json:"hash,omitempty"`
	Model      string          `json:"model,omitempty"`
	Approve    bool            `json:"approve"`
	Coverage   string          `json:"coverage,omitempty"`
	Matrix     string          `json:"matrix,omitempty"`
	Scope      []scopeItemJSON `json:"scope,omitempty"`
	Transcript string          `json:"transcript,omitempty"`

	// Ruling
	RulingKind string   `json:"ruling,omitempty"`
	Item       string   `json:"item,omitempty"`
	Text       string   `json:"text,omitempty"`
	To         []string `json:"to,omitempty"`

	// Lint
	LintKind string       `json:"lint_kind,omitempty"`
	Input    string       `json:"input,omitempty"`
	Answers  []answerJSON `json:"answers,omitempty"`
}

// encode renders one record as the JSON object that follows the comment
// prefix. It fails only on values JSON cannot carry (NaN, ±Inf).
func encode(r Record) (string, error) {
	j := recordJSON{ID: r.ID, Kind: KindOf(r.Body), At: r.At}
	switch b := r.Body.(type) {
	case Appointment:
		j.Round = string(b.Round)
		j.Tier = b.Tier.String()
		j.MergeHash = b.MergeHash
	case Leg:
		j.Base = string(b.Base)
		j.Head = string(b.Head)
		j.Mutant = string(b.Mutant)
		j.Tests = b.Tests
		j.Backend = b.Backend
		j.A = encodeLegRun(b.A)
		j.B = encodeLegRun(b.B)
		j.C = encodeLegRun(b.C)
	case Verdict:
		if b.Seat != SeatNone {
			j.Seat = b.Seat.String()
		}
		j.Hash = string(b.Hash)
		j.Model = b.Model
		j.Approve = b.Approve
		j.Coverage = b.Coverage
		j.Matrix = b.Matrix
		j.Transcript = string(b.Transcript)
		for _, s := range b.Scope {
			j.Scope = append(j.Scope, scopeItemJSON{ID: s.ID, Text: s.Text})
		}
	case Ruling:
		j.RulingKind = b.Kind.String()
		j.Item = b.Item
		j.Text = b.Text
		if b.Seat != SeatNone {
			j.Seat = b.Seat.String()
		}
		for _, to := range b.To {
			j.To = append(j.To, string(to))
		}
	case Lint:
		j.LintKind = b.Kind.String()
		j.Input = string(b.Input)
		j.Model = b.Model
		for _, a := range b.Answers {
			j.Answers = append(j.Answers, answerJSON{
				Lint: a.Lint, Question: a.Question, P: a.P, Choice: a.Choice,
				Confidence: a.Confidence, Threshold: a.Threshold, Flagged: a.Flagged,
			})
		}
	}
	raw, err := json.Marshal(j)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// encodeVerified encodes rec and decodes the result. A body that would not
// read back equal is an error, so Append never writes a record that Round
// and Get cannot read or would read differently.
func encodeVerified(rec Record) (string, error) {
	text, err := encode(rec)
	if err != nil {
		return "", err
	}
	back, err := decode(text)
	if err != nil {
		return "", err
	}
	if !reflect.DeepEqual(back.Body, withNilSlices(rec.Body)) {
		return "", fmt.Errorf("it reads back as %+v", back.Body)
	}
	return text, nil
}

// withNilSlices returns b with every empty slice set to nil: the encoding
// omits empty slices, so an empty slice and nil both read back as nil.
func withNilSlices(b Body) Body {
	switch v := b.(type) {
	case Leg:
		v.Tests = nilIfEmpty(v.Tests)
		v.A.Cmd, v.B.Cmd, v.C.Cmd = nilIfEmpty(v.A.Cmd), nilIfEmpty(v.B.Cmd), nilIfEmpty(v.C.Cmd)
		return v
	case Verdict:
		v.Scope = nilIfEmpty(v.Scope)
		return v
	case Ruling:
		v.To = nilIfEmpty(v.To)
		return v
	case Lint:
		v.Answers = nilIfEmpty(v.Answers)
		return v
	}
	return b
}

func nilIfEmpty[T any](s []T) []T {
	if len(s) == 0 {
		return nil
	}
	return s
}

// decode parses the JSON object after the prefix into a Record. Unknown
// kinds and malformed objects are errors.
func decode(text string) (Record, error) {
	var j recordJSON
	if err := json.Unmarshal([]byte(text), &j); err != nil {
		return Record{}, err
	}
	if j.ID == "" {
		return Record{}, fmt.Errorf(`no "id"`)
	}
	if j.At.IsZero() {
		return Record{}, fmt.Errorf(`no "at"`)
	}
	r := Record{ID: j.ID, At: j.At}
	var body Body
	switch j.Kind {
	case kindAppointment:
		tier, err := ParseTier(j.Tier)
		if err != nil {
			return Record{}, err
		}
		body = Appointment{Round: RoundID(j.Round), Tier: tier, MergeHash: repo.Hash(j.MergeHash)}
	case kindLeg:
		var runs [3]LegRun
		for i, rj := range []*legRunJSON{j.A, j.B, j.C} {
			run, err := decodeLegRun(rj)
			if err != nil {
				return Record{}, err
			}
			runs[i] = run
		}
		body = Leg{
			Base: repo.Hash(j.Base), Head: repo.Hash(j.Head),
			Mutant: evidence.Ref(j.Mutant), Tests: j.Tests, Backend: j.Backend,
			A: runs[0], B: runs[1], C: runs[2],
		}
	case kindVerdict:
		seat, err := ParseSeat(j.Seat)
		if err != nil {
			return Record{}, err
		}
		v := Verdict{
			Seat: seat, Hash: repo.Hash(j.Hash), Model: j.Model, Approve: j.Approve,
			Coverage: j.Coverage, Matrix: j.Matrix, Transcript: evidence.Ref(j.Transcript),
		}
		for _, s := range j.Scope {
			v.Scope = append(v.Scope, ScopeItem{ID: s.ID, Text: s.Text})
		}
		body = v
	case kindRuling:
		kind, err := ParseRulingKind(j.RulingKind)
		if err != nil {
			return Record{}, err
		}
		seat := SeatNone
		if j.Seat != "" {
			seat, err = ParseSeat(j.Seat)
			if err != nil {
				return Record{}, err
			}
		}
		rul := Ruling{Kind: kind, Item: j.Item, Text: j.Text, Seat: seat}
		for _, to := range j.To {
			rul.To = append(rul.To, SliceID(to))
		}
		body = rul
	case kindLint:
		kind, err := ParseLintKind(j.LintKind)
		if err != nil {
			return Record{}, err
		}
		l := Lint{Kind: kind, Input: evidence.Ref(j.Input), Model: j.Model}
		for _, a := range j.Answers {
			l.Answers = append(l.Answers, Answer{
				Lint: a.Lint, Question: a.Question, P: a.P, Choice: a.Choice,
				Confidence: a.Confidence, Threshold: a.Threshold, Flagged: a.Flagged,
			})
		}
		body = l
	default:
		return Record{}, fmt.Errorf("unknown record kind %q", j.Kind)
	}
	r.Body = body
	return r, nil
}

func encodeLegRun(r LegRun) *legRunJSON {
	j := &legRunJSON{
		Cmd: r.Cmd, ExitCode: r.ExitCode, KillReason: r.KillReason,
		Executed: r.Executed, Counted: r.Counted,
		Transcript: string(r.Transcript), DurationNS: int64(r.Duration),
	}
	if r.Want != 0 {
		j.Want = r.Want.String()
	}
	if r.Got != 0 {
		j.Got = r.Got.String()
	}
	return j
}

// decodeLegRun reads one leg run; an absent outcome is the zero value and an
// unknown one is an error.
func decodeLegRun(j *legRunJSON) (LegRun, error) {
	if j == nil {
		return LegRun{}, nil
	}
	r := LegRun{
		Cmd: j.Cmd, ExitCode: j.ExitCode, KillReason: j.KillReason,
		Executed: j.Executed, Counted: j.Counted,
		Transcript: evidence.Ref(j.Transcript), Duration: time.Duration(j.DurationNS),
	}
	var err error
	if r.Want, err = optionalOutcome(j.Want); err != nil {
		return LegRun{}, err
	}
	if r.Got, err = optionalOutcome(j.Got); err != nil {
		return LegRun{}, err
	}
	return r, nil
}

// optionalOutcome reads an outcome that encodeLegRun omits when zero.
func optionalOutcome(s string) (Outcome, error) {
	if s == "" {
		return 0, nil
	}
	return parseOutcome(s)
}

func parseOutcome(s string) (Outcome, error) {
	switch s {
	case "green":
		return Green, nil
	case "red":
		return Red, nil
	case "killed":
		return Killed, nil
	}
	return 0, fmt.Errorf("ledger: unknown outcome %q", s)
}

func (o Outcome) String() string {
	switch o {
	case Green:
		return "green"
	case Red:
		return "red"
	case Killed:
		return "killed"
	}
	return fmt.Sprintf("outcome(%d)", int(o))
}

// KindOf names a record body's kind: appointment, leg, verdict, ruling, or
// lint.
func KindOf(b Body) string {
	switch b.(type) {
	case Appointment:
		return kindAppointment
	case Leg:
		return kindLeg
	case Verdict:
		return kindVerdict
	case Ruling:
		return kindRuling
	case Lint:
		return kindLint
	}
	return "unknown"
}
