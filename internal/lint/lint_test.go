package lint

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dpoage/auspex/internal/ledger"
	"github.com/dpoage/llmkit/decide"
)

// TestFlagsAtThreshold is criterion O1: each kind asks exactly the
// oracle-rounds table questions and flags iff p >= threshold (skill
// routing 0.8, mechanism claim 0.85, unevidenced claim 0.7) — the boundary
// case, exactly at the threshold, must flag. Hiding mutant: `>` instead
// of `>=`.
func TestFlagsAtThreshold(t *testing.T) {
	t.Run("skill routing at 0.8", func(t *testing.T) {
		judge := newFakeJudge(t, func(state string, questions map[string]json.RawMessage) map[string]fakeAnswer {
			out := map[string]fakeAnswer{}
			for id := range questions {
				out[id] = nounQuestion(SkillRoutingThreshold)
			}
			return out
		})
		skills := []Skill{{Name: "module-design", Description: "Use before writing code that creates a module boundary."}}
		lnt, err := Ask(context.Background(), judge, ledger.Brief, "a single paragraph brief with no bullets", skills)
		if err != nil {
			t.Fatalf("Ask: %v", err)
		}
		found := false
		for _, a := range lnt.Answers {
			if a.Lint == "module-design" {
				found = true
				if a.Question != skillQuestion(skills[0]) {
					t.Fatalf("skill question = %q, want the verbatim skill-routing question", a.Question)
				}
				if a.P != SkillRoutingThreshold || !a.Flagged {
					t.Fatalf("skill routing answer = %+v, want P=%v Flagged=true", a, SkillRoutingThreshold)
				}
			}
		}
		if !found {
			t.Fatal("no answer recorded for the routed skill")
		}
	})

	t.Run("mechanism claim at 0.85", func(t *testing.T) {
		judge := newFakeJudge(t, func(state string, questions map[string]json.RawMessage) map[string]fakeAnswer {
			return map[string]fakeAnswer{"q": nounQuestion(MechanismClaimThreshold)}
		})
		lnt, err := Ask(context.Background(), judge, ledger.Fixlist, "one lone paragraph, no bullets here.", nil)
		if err != nil {
			t.Fatalf("Ask: %v", err)
		}
		if len(lnt.Answers) != 1 {
			t.Fatalf("Answers = %v, want exactly 1", lnt.Answers)
		}
		a := lnt.Answers[0]
		if a.Question != MechanismClaimQuestion {
			t.Fatalf("question = %q, want the verbatim mechanism-claim question", a.Question)
		}
		if a.P != MechanismClaimThreshold || !a.Flagged {
			t.Fatalf("answer = %+v, want P=%v Flagged=true", a, MechanismClaimThreshold)
		}
	})

	t.Run("unevidenced claim at 0.7", func(t *testing.T) {
		judge := newFakeJudge(t, func(state string, questions map[string]json.RawMessage) map[string]fakeAnswer {
			return map[string]fakeAnswer{"q": nounQuestion(UnevidencedClaimThreshold)}
		})
		lnt, err := Ask(context.Background(), judge, ledger.Reply, "the fix landed and the suite is green.", nil)
		if err != nil {
			t.Fatalf("Ask: %v", err)
		}
		if len(lnt.Answers) != 1 {
			t.Fatalf("Answers = %v, want exactly 1", lnt.Answers)
		}
		a := lnt.Answers[0]
		if a.Question != UnevidencedClaimQuestion {
			t.Fatalf("question = %q, want the verbatim unevidenced-claim question", a.Question)
		}
		if a.P != UnevidencedClaimThreshold || !a.Flagged {
			t.Fatalf("answer = %+v, want P=%v Flagged=true", a, UnevidencedClaimThreshold)
		}
	})
}

// TestUnitsJudgedSeparately is criterion O2: brief (mechanism claim),
// fixlist, and reply texts are split into paragraphs and top-level
// bullets, and each unit is judged on its own — the kind flags when any
// unit flags, and a unit that never flags on its own must not be masked by
// one that does. Hiding mutant: judge the whole text as one unit.
func TestUnitsJudgedSeparately(t *testing.T) {
	quiet := "This paragraph never triggers the mechanism-claim question on its own."
	loud := "This paragraph always triggers the mechanism-claim question on its own."
	text := quiet + "\n\n" + loud

	seenStates := map[string]bool{}
	judge := newFakeJudge(t, func(state string, questions map[string]json.RawMessage) map[string]fakeAnswer {
		seenStates[state] = true
		p := 0.0
		if state == loud {
			p = 1.0
		}
		return map[string]fakeAnswer{"q": nounQuestion(p)}
	})

	lnt, err := Ask(context.Background(), judge, ledger.Fixlist, text, nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(lnt.Answers) != 2 {
		t.Fatalf("Answers = %v, want exactly 2 (one per unit); the whole text was judged as one unit", lnt.Answers)
	}
	if !seenStates[quiet] || !seenStates[loud] {
		t.Fatalf("judge saw states %v, want each paragraph judged on its own", seenStates)
	}
	if seenStates[text] {
		t.Fatalf("judge saw the whole unsplit text as a state; units were not split")
	}
	flaggedCount := 0
	for _, a := range lnt.Answers {
		if a.Flagged {
			flaggedCount++
		}
	}
	if flaggedCount != 1 {
		t.Fatalf("flagged answers = %d, want exactly 1 (the loud paragraph)", flaggedCount)
	}
}

// TestBlockerUnclassifiedBelowConfidence is criterion O3: omen blocker
// records a PRODUCT/TEST_MACHINERY/PROSE class only when confidence is at
// least 0.8; below that, the recorded class is "unclassified", flagged for
// the orchestrator to classify by hand. P carries the confidence, the value
// compared with the threshold. Hiding mutant: record the low-confidence
// choice as the class.
func TestBlockerUnclassifiedBelowConfidence(t *testing.T) {
	cases := []struct {
		name       string
		confidence float64
		wantChoice string
		wantFlag   bool
	}{
		{"below threshold", 0.79, Unclassified, true},
		{"at threshold", 0.8, Product, false},
		{"above threshold", 0.95, Product, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			judge := newFakeJudge(t, func(state string, questions map[string]json.RawMessage) map[string]fakeAnswer {
				return map[string]fakeAnswer{"blocker": choiceQuestion("PRODUCT", c.confidence)}
			})
			lnt, err := Ask(context.Background(), judge, ledger.Blocker, "the mutant survived because the fix never touched the guard clause", nil)
			if err != nil {
				t.Fatalf("Ask: %v", err)
			}
			if len(lnt.Answers) != 1 {
				t.Fatalf("Answers = %v, want exactly 1", lnt.Answers)
			}
			a := lnt.Answers[0]
			if a.Choice != c.wantChoice {
				t.Fatalf("Choice = %q, want %q", a.Choice, c.wantChoice)
			}
			if a.Flagged != c.wantFlag {
				t.Fatalf("Flagged = %v, want %v", a.Flagged, c.wantFlag)
			}
			if a.P != c.confidence || a.Confidence != c.confidence || a.Threshold != BlockerConfidence {
				t.Fatalf("answer = %+v, want P = Confidence = %v and Threshold %v", a, c.confidence, BlockerConfidence)
			}
			if a.Question != BlockerQuestion {
				t.Fatalf("question = %q, want the verbatim blocker question", a.Question)
			}
		})
	}
}

// TestUnitsSplitAtEveryTopLevelMarker pins the unit shape of O2: a list
// item (-, *, +, N. or N)) starts a unit unless it is nested inside the open
// item, that is, indented at least to the open item's content column, so a
// child 2 or 3 columns deeper stays with its parent; a lead-in or heading
// line above a list is a unit of its own; a heading directly after an item
// starts a unit; a fenced code block is never split, even across a blank
// line. Each case uses O2's exact-state technique: the judge flags only a
// state that is exactly the loud unit, so a loud unit judged inside a larger
// one, or split from its nested children, is lost. Hiding mutants: split
// bullets only when a block's first line is one; nest by absolute
// indentation (an item indented 0-3 spaces always starts a unit); no
// heading break.
func TestUnitsSplitAtEveryTopLevelMarker(t *testing.T) {
	quiet := "This item never triggers the mechanism-claim question on its own."
	loud := "This item always triggers the mechanism-claim question on its own."
	fence := "```sh\ngo test ./...\n\n- not an item\n```"
	cases := []struct {
		name string
		text string
		want []string // the states the judge must see, in order
		loud string   // the one unit that flags
	}{
		{
			name: "paragraph lead-in",
			text: "Two items follow.\n- " + quiet + "\n- " + loud,
			want: []string{"Two items follow.", "- " + quiet, "- " + loud},
			loud: "- " + loud,
		},
		{
			name: "heading lead-in",
			text: "## Place\n- " + quiet + "\n- " + loud,
			want: []string{"## Place", "- " + quiet, "- " + loud},
			loud: "- " + loud,
		},
		{
			name: "N) markers",
			text: "Steps:\n1) " + quiet + "\n2) " + loud,
			want: []string{"Steps:", "1) " + quiet, "2) " + loud},
			loud: "2) " + loud,
		},
		{
			name: "N) item at its parent's content column nests",
			text: "Steps:\n1) " + loud + "\n   2) a nested detail",
			want: []string{"Steps:", "1) " + loud + "\n   2) a nested detail"},
			loud: "1) " + loud + "\n   2) a nested detail",
		},
		{
			name: "items nested two spaces stay with their parent",
			text: "- " + loud + "\n  - n1\n  - n2\n- " + quiet,
			want: []string{"- " + loud + "\n  - n1\n  - n2", "- " + quiet},
			loud: "- " + loud + "\n  - n1\n  - n2",
		},
		{
			name: "item nested three spaces under N. stays with its parent",
			text: "1. " + loud + "\n   - n1\n2. " + quiet,
			want: []string{"1. " + loud + "\n   - n1", "2. " + quiet},
			loud: "1. " + loud + "\n   - n1",
		},
		{
			name: "indented siblings are separate units",
			text: "  - " + quiet + "\n  - " + loud,
			want: []string{"- " + quiet, "- " + loud},
			loud: "- " + loud,
		},
		{
			name: "heading directly after an item",
			text: "- " + quiet + "\n## Next\n" + loud,
			want: []string{"- " + quiet, "## Next\n" + loud},
			loud: "## Next\n" + loud,
		},
		{
			name: "nested item stays with its parent",
			text: "- " + loud + "\n    - a nested detail\n- " + quiet,
			want: []string{"- " + loud + "\n    - a nested detail", "- " + quiet},
			loud: "- " + loud + "\n    - a nested detail",
		},
		{
			name: "fence with an inner blank line",
			text: "- " + quiet + "\n\n" + fence,
			want: []string{"- " + quiet, fence},
			loud: fence,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var states []string
			judge := newFakeJudge(t, func(state string, questions map[string]json.RawMessage) map[string]fakeAnswer {
				states = append(states, state)
				p := 0.0
				if state == c.loud {
					p = 1.0
				}
				return map[string]fakeAnswer{"q": nounQuestion(p)}
			})
			lnt, err := Ask(context.Background(), judge, ledger.Fixlist, c.text, nil)
			if err != nil {
				t.Fatalf("Ask: %v", err)
			}
			if !slices.Equal(states, c.want) {
				t.Fatalf("judge saw states %q, want %q", states, c.want)
			}
			flagged := 0
			for _, a := range lnt.Answers {
				if a.Flagged {
					flagged++
				}
			}
			if len(lnt.Answers) != len(c.want) || flagged != 1 {
				t.Fatalf("answers = %+v, want %d answers with exactly the loud unit flagged", lnt.Answers, len(c.want))
			}
		})
	}
}

// TestReplyClaimKeepsItsNestedEvidence pins F1' on a reply: each claim's
// evidence is nested two spaces under it, so the claim and its evidence form
// one unit and the unevidenced-claim question sees the evidence. The judge
// flags exactly a claim judged without its evidence. Hiding mutant: nest by
// absolute indentation, which splits the evidence from its claim.
func TestReplyClaimKeepsItsNestedEvidence(t *testing.T) {
	first := "- The fix landed.\n  - Evidence: `go test -count=1 ./...` exited 0 with 112 PASS lines."
	second := "- The mutant is red.\n  - Evidence: leg (b) FAILs at lint_test.go:244."
	bare := map[string]bool{"- The fix landed.": true, "- The mutant is red.": true}
	var states []string
	judge := newFakeJudge(t, func(state string, questions map[string]json.RawMessage) map[string]fakeAnswer {
		states = append(states, state)
		p := 0.0
		if bare[state] {
			p = 1.0
		}
		return map[string]fakeAnswer{"q": nounQuestion(p)}
	})
	lnt, err := Ask(context.Background(), judge, ledger.Reply, first+"\n"+second, nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if want := []string{first, second}; !slices.Equal(states, want) {
		t.Fatalf("judge saw states %q, want each claim with its nested evidence %q", states, want)
	}
	for _, a := range lnt.Answers {
		if a.Flagged {
			t.Fatalf("answer %+v flagged: a claim was judged without its evidence", a)
		}
	}
}

// wireQuestion is one question as it crossed the wire to the judge.
type wireQuestion struct {
	Type         string                     `json:"type"`
	Instructions string                     `json:"instructions"`
	Criteria     map[string]json.RawMessage `json:"criteria"`
}

// wireCall is one request the judge received: its state and questions.
type wireCall struct {
	state     string
	questions map[string]wireQuestion
}

// recordingJudge is a fake judge that records every request as it crossed
// the wire and answers every Noul with p and every Choice with PRODUCT at
// confidence 0.9.
func recordingJudge(t *testing.T, p float64) (*decide.Client, *[]wireCall) {
	t.Helper()
	var calls []wireCall
	judge := newFakeJudge(t, func(state string, questions map[string]json.RawMessage) map[string]fakeAnswer {
		call := wireCall{state: state, questions: map[string]wireQuestion{}}
		out := map[string]fakeAnswer{}
		for id, raw := range questions {
			var q wireQuestion
			if err := json.Unmarshal(raw, &q); err != nil {
				t.Errorf("question %s: %v", id, err)
			}
			call.questions[id] = q
			if q.Type == "choice" {
				out[id] = choiceQuestion(Product, 0.9)
			} else {
				out[id] = nounQuestion(p)
			}
		}
		calls = append(calls, call)
		return out
	})
	return judge, &calls
}

// instructionsOf returns the sorted instruction texts of call's questions of
// type typ, failing on any other type.
func instructionsOf(t *testing.T, call wireCall, typ string) []string {
	t.Helper()
	var out []string
	for id, q := range call.questions {
		if q.Type != typ {
			t.Fatalf("question %s has type %q, want %q", id, q.Type, typ)
		}
		out = append(out, q.Instructions)
	}
	slices.Sort(out)
	return out
}

// TestQuestionsOnTheWire pins, on the wire through the httptest fake, what
// each kind sends the judge: the question text equals the table constant;
// brief asks the mechanism-claim question once per unit and skill routing
// once per skill in one request whose state is the whole brief. Hiding
// mutants: never ask the mechanism question for a brief; route over the
// first unit only; send a different question text.
func TestQuestionsOnTheWire(t *testing.T) {
	t.Run("brief", func(t *testing.T) {
		first := "The first paragraph of the brief."
		second := "The second paragraph of the brief."
		brief := first + "\n\n" + second
		skills := []Skill{
			{Name: "alpha", Description: "Use for alpha work."},
			{Name: "beta", Description: "Use for beta work."},
		}
		judge, calls := recordingJudge(t, 0.1)
		lnt, err := Ask(context.Background(), judge, ledger.Brief, brief, skills)
		if err != nil {
			t.Fatalf("Ask: %v", err)
		}

		var routing, mechanism []wireCall
		for _, c := range *calls {
			if len(c.questions) == len(skills) {
				routing = append(routing, c)
			} else {
				mechanism = append(mechanism, c)
			}
		}
		if len(routing) != 1 || routing[0].state != brief {
			t.Fatalf("routing requests = %+v, want exactly one whose state is the whole brief", routing)
		}
		wantRouting := []string{
			"A coding agent received this message. Should it load the skill 'alpha' before acting? The skill's trigger: Use for alpha work.",
			"A coding agent received this message. Should it load the skill 'beta' before acting? The skill's trigger: Use for beta work.",
		}
		if got := instructionsOf(t, routing[0], "noul"); !slices.Equal(got, wantRouting) {
			t.Fatalf("routing questions on the wire = %q, want %q", got, wantRouting)
		}
		var mechanismStates []string
		for _, c := range mechanism {
			mechanismStates = append(mechanismStates, c.state)
			if got := instructionsOf(t, c, "noul"); !slices.Equal(got, []string{MechanismClaimQuestion}) {
				t.Fatalf("mechanism request questions on the wire = %q, want exactly the mechanism-claim question", got)
			}
		}
		if !slices.Equal(mechanismStates, []string{first, second}) {
			t.Fatalf("mechanism-claim requests had states %q, want one per unit %q", mechanismStates, []string{first, second})
		}
		if len(lnt.Answers) != 4 {
			t.Fatalf("answers = %+v, want 2 routing plus 2 mechanism-claim", lnt.Answers)
		}
	})

	for _, c := range []struct {
		kind  ledger.LintKind
		typ   string
		text  string
		want  string
		label string
	}{
		{ledger.Fixlist, "noul", "One fix list unit.", MechanismClaimQuestion, "fixlist"},
		{ledger.Reply, "noul", "One reply unit.", UnevidencedClaimQuestion, "reply"},
		{ledger.Blocker, "choice", "One blocker.", BlockerQuestion, "blocker"},
	} {
		t.Run(c.label, func(t *testing.T) {
			judge, calls := recordingJudge(t, 0.1)
			if _, err := Ask(context.Background(), judge, c.kind, c.text, nil); err != nil {
				t.Fatalf("Ask: %v", err)
			}
			if len(*calls) != 1 || (*calls)[0].state != c.text {
				t.Fatalf("requests = %+v, want one whose state is the text", *calls)
			}
			call := (*calls)[0]
			if got := instructionsOf(t, call, c.typ); !slices.Equal(got, []string{c.want}) {
				t.Fatalf("questions on the wire = %q, want exactly %q", got, c.want)
			}
			if c.kind == ledger.Blocker {
				var options []string
				for _, q := range call.questions {
					for o := range q.Criteria {
						options = append(options, o)
					}
				}
				slices.Sort(options)
				if want := []string{Product, Prose, TestMachinery}; !slices.Equal(options, want) {
					t.Fatalf("blocker options on the wire = %q, want %q", options, want)
				}
			}
		})
	}
}

// TestAskRefusesOutOfRangeAnswers: decide v0.5.0 records whatever the
// server returns, so Ask itself refuses a probability outside [0,1], a
// choice other than the three blocker classes, and a choice without a
// confidence. Hiding mutants: drop the probability range check; drop the
// class membership check; drop the confidence check.
func TestAskRefusesOutOfRangeAnswers(t *testing.T) {
	noConfidence := fakeAnswer{Type: "choice", Choice: func() *string { s := Product; return &s }()}
	cases := []struct {
		name   string
		kind   ledger.LintKind
		skills []Skill
		answer fakeAnswer
	}{
		{"noul above 1", ledger.Fixlist, nil, nounQuestion(1.5)},
		{"noul below 0", ledger.Reply, nil, nounQuestion(-0.2)},
		{"routing noul above 1", ledger.Brief, []Skill{{Name: "alpha", Description: "Use for alpha."}}, nounQuestion(1.01)},
		{"choice outside the classes", ledger.Blocker, nil, choiceQuestion("OTHER", 0.9)},
		{"choice in the wrong case", ledger.Blocker, nil, choiceQuestion("product", 0.9)},
		{"empty choice", ledger.Blocker, nil, choiceQuestion("", 0.9)},
		{"choice without a confidence", ledger.Blocker, nil, noConfidence},
		{"confidence above 1", ledger.Blocker, nil, choiceQuestion(Prose, 1.2)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			judge := newFakeJudge(t, func(state string, questions map[string]json.RawMessage) map[string]fakeAnswer {
				out := map[string]fakeAnswer{}
				for id := range questions {
					if strings.HasPrefix(id, "skill") || c.kind != ledger.Brief {
						out[id] = c.answer
					} else {
						out[id] = nounQuestion(0.1)
					}
				}
				return out
			})
			lnt, err := Ask(context.Background(), judge, c.kind, "One unit of text.", c.skills)
			if err == nil {
				t.Fatalf("Ask accepted %+v and returned %+v, want an error", c.answer, lnt)
			}
			if lnt.Answers != nil || lnt.Model != "" {
				t.Fatalf("Ask returned %+v alongside its error, want the zero Lint", lnt)
			}
		})
	}
}

// TestLintModelRecordsEveryReportedModel: Lint.Model is every distinct
// model the judge reported across the invocation, comma-joined in
// first-seen order. Hiding mutant: record only the last response's model.
func TestLintModelRecordsEveryReportedModel(t *testing.T) {
	model := map[string]string{"First unit.": "jev-a", "Second unit.": "jev-b", "Third unit.": "jev-a"}
	judge := newFakeJudgeModel(t, func(state string, questions map[string]json.RawMessage) (string, map[string]fakeAnswer) {
		return model[state], map[string]fakeAnswer{"q": nounQuestion(0.1)}
	})
	lnt, err := Ask(context.Background(), judge, ledger.Fixlist, "First unit.\n\nSecond unit.\n\nThird unit.", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if lnt.Model != "jev-a,jev-b" {
		t.Fatalf("Model = %q, want %q", lnt.Model, "jev-a,jev-b")
	}
}
