// Package lint hides the judge-lint questions the oracle-rounds skill
// defines, their flag thresholds, the blocker classification classes, and
// that TypeSafe Jev (github.com/dpoage/llmkit/decide) answers them. Callers
// ask for a kind's judgment over a text and get back a ledger.Lint's
// answers; they never see the question wording or the threshold that
// decides a flag.
package lint

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/dpoage/auspex/internal/ledger"
	"github.com/dpoage/llmkit/decide"
)

// Question text, copied verbatim from the oracle-rounds skill's judge-lint
// table (~/.agents/skills/oracle-rounds/SKILL.md). SkillRoutingQuestion
// carries its two placeholders literally; skillQuestion substitutes them
// per skill.
const (
	SkillRoutingQuestion     = "A coding agent received this message. Should it load the skill '<name>' before acting? The skill's trigger: <its description>"
	MechanismClaimQuestion   = "This text is one unit of instructions sent to a coding implementer. Does it assert why something happens or how a system behaves internally (a mechanism, cause, or factual claim about system behaviour) as established fact, without quoting in this same text the command or probe output that establishes it?"
	UnevidencedClaimQuestion = "This text is one unit of a status report a coding implementer sent its orchestrator. Does it claim an edit landed, a test or mutant passed or failed, or something was verified, WITHOUT including raw evidence for that claim in this text — no command with its output, no diff hunk, no checksum, no quoted test output?"
	BlockerQuestion          = "What does the BLOCKER UNDER JUDGMENT concern?"
)

// Flag thresholds, from the same table. A lint flags when its measured
// value is at or above its threshold (never strictly above): the boundary
// case belongs to the flag.
const (
	SkillRoutingThreshold     = 0.8
	MechanismClaimThreshold   = 0.85
	UnevidencedClaimThreshold = 0.7
	// BlockerConfidence is the choice confidence a blocker classification
	// needs to be recorded as its reported class; below it, the class is
	// recorded as "unclassified" for the orchestrator to decide.
	BlockerConfidence = 0.8
)

// Blocker classes: the three a blocker is classified into, and the Choice
// recorded when the classification's confidence falls under
// BlockerConfidence.
const (
	Product       = "PRODUCT"
	TestMachinery = "TEST_MACHINERY"
	Prose         = "PROSE"
	Unclassified  = "unclassified"
)

// blockerOptions describes the three classes omen blocker chooses among.
var blockerOptions = map[string]any{
	Product:       "The blocker concerns the product code under review: a behavior, a defect, a missing case.",
	TestMachinery: "The blocker concerns test or harness machinery: a runner, a gate, a mutant, fixture setup, CI wiring.",
	Prose:         "The blocker concerns prose: brief wording, a doc, a commit message, a comment.",
}

// Skill is one installed skill available for brief routing: its front
// matter name and single-line description.
type Skill struct {
	Name        string
	Description string
}

// Ask runs kind's judge lint over text and returns the recorded answers.
// For ledger.Brief, skills routes: one question per entry, asked once over
// the whole text, plus the mechanism-claim question asked once per unit
// (Units). ledger.Fixlist and ledger.Reply ask their question once per
// unit. ledger.Blocker asks one choice question over the whole text. skills
// is ignored for every kind but Brief. The returned Lint has Kind, Model
// (every distinct model the judge reported, comma-joined), and Answers set;
// Input is the caller's to fill in once it has stored text as evidence —
// Ask does not know about the evidence store. Every answer's P is the value
// compared with its Threshold. Ask returns an error, and no Lint, when the
// judge fails or answers outside the question's range: a probability
// outside [0,1], a choice that is not a blocker class, or a choice without
// a confidence in (0,1].
func Ask(ctx context.Context, client *decide.Client, kind ledger.LintKind, text string, skills []Skill) (ledger.Lint, error) {
	var seen models
	var answers []ledger.Answer
	var err error
	switch kind {
	case ledger.Brief:
		answers, err = askSkillRouting(ctx, client, text, skills, &seen)
		if err == nil {
			var mechanism []ledger.Answer
			mechanism, err = askUnits(ctx, client, text, MechanismClaimQuestion, MechanismClaimThreshold, "mechanism claim", &seen)
			answers = append(answers, mechanism...)
		}
	case ledger.Fixlist:
		answers, err = askUnits(ctx, client, text, MechanismClaimQuestion, MechanismClaimThreshold, "mechanism claim", &seen)
	case ledger.Reply:
		answers, err = askUnits(ctx, client, text, UnevidencedClaimQuestion, UnevidencedClaimThreshold, "unevidenced claim", &seen)
	case ledger.Blocker:
		answers, err = askBlocker(ctx, client, text, &seen)
	default:
		return ledger.Lint{}, fmt.Errorf("lint: unknown kind %v", kind)
	}
	if err != nil {
		return ledger.Lint{}, err
	}
	return ledger.Lint{Kind: kind, Model: strings.Join(seen, ","), Answers: answers}, nil
}

// models collects the distinct model ids the judge reported across one
// invocation's decide calls, in first-seen order.
type models []string

func (m *models) add(id string) {
	if !slices.Contains(*m, id) {
		*m = append(*m, id)
	}
}

// noul returns resp's probability for question id, refusing one outside
// [0,1] (NaN included): decide v0.5.0 does not range-check it, and an
// out-of-range value is a broken response, not a judgment to record.
func noul(resp decide.Response, id string) (float64, error) {
	p := resp.Nouls[id]
	if !(p >= 0 && p <= 1) {
		return 0, fmt.Errorf("lint: judge answered question %q with probability %v, outside [0,1]", id, p)
	}
	return p, nil
}

// askUnits asks question once per unit of text (Units), flagging each
// answer at threshold; label names the answers for printing (e.g.
// "mechanism claim"). The kind flags overall when any unit's answer flags —
// callers read that off the returned Answers, no unit is judged as part of
// a larger whole.
func askUnits(ctx context.Context, client *decide.Client, text, question string, threshold float64, label string, seen *models) ([]ledger.Answer, error) {
	units := Units(text)
	answers := make([]ledger.Answer, 0, len(units))
	const questionID = "q"
	for _, unit := range units {
		resp, err := client.Ask(ctx, unit, decide.Questions{questionID: decide.Noul{Instructions: question}})
		if err != nil {
			return nil, err
		}
		seen.add(resp.Model)
		p, err := noul(resp, questionID)
		if err != nil {
			return nil, err
		}
		answers = append(answers, ledger.Answer{
			Lint:      label,
			Question:  question,
			P:         p,
			Threshold: threshold,
			Flagged:   p >= threshold,
		})
	}
	return answers, nil
}

// askSkillRouting asks one routing question per skill, all in a single Ask
// call over the whole brief text (skill routing judges the whole brief, not
// its units).
func askSkillRouting(ctx context.Context, client *decide.Client, text string, skills []Skill, seen *models) ([]ledger.Answer, error) {
	if len(skills) == 0 {
		return nil, nil
	}
	questions := make(decide.Questions, len(skills))
	for i, s := range skills {
		id := fmt.Sprintf("skill%d", i)
		questions[id] = decide.Noul{Instructions: skillQuestion(s)}
	}
	resp, err := client.Ask(ctx, text, questions)
	if err != nil {
		return nil, err
	}
	seen.add(resp.Model)
	answers := make([]ledger.Answer, 0, len(skills))
	for i, s := range skills {
		p, err := noul(resp, fmt.Sprintf("skill%d", i))
		if err != nil {
			return nil, err
		}
		answers = append(answers, ledger.Answer{
			Lint:      s.Name,
			Question:  skillQuestion(s),
			P:         p,
			Threshold: SkillRoutingThreshold,
			Flagged:   p >= SkillRoutingThreshold,
		})
	}
	return answers, nil
}

// skillQuestion substitutes s's name and description into
// SkillRoutingQuestion's two placeholders.
func skillQuestion(s Skill) string {
	r := strings.NewReplacer("<name>", s.Name, "<its description>", s.Description)
	return r.Replace(SkillRoutingQuestion)
}

// askBlocker asks the blocker classification question once over the whole
// text. The answer's P and Confidence are the choice confidence. The
// recorded Choice is the reported class only when that confidence is at
// least BlockerConfidence; otherwise it is Unclassified, and Flagged is set
// so the orchestrator sees it needs to classify by hand. A choice that is
// not one of the three classes, or whose confidence is not in (0,1], is
// refused: decide v0.5.0 checks neither, and reads an absent confidence as
// 0.
func askBlocker(ctx context.Context, client *decide.Client, text string, seen *models) ([]ledger.Answer, error) {
	const questionID = "blocker"
	resp, err := client.Ask(ctx, text, decide.Questions{questionID: decide.Choice{Instructions: BlockerQuestion, Options: blockerOptions}})
	if err != nil {
		return nil, err
	}
	seen.add(resp.Model)
	ans := resp.Choices[questionID]
	if _, ok := blockerOptions[ans.Choice]; !ok {
		return nil, fmt.Errorf("lint: judge chose %q, not one of %s, %s, %s", ans.Choice, Product, TestMachinery, Prose)
	}
	conf := ans.Confidence
	if !(conf > 0 && conf <= 1) {
		return nil, fmt.Errorf("lint: judge chose %s with confidence %v, want a confidence in (0,1]", ans.Choice, conf)
	}
	a := ledger.Answer{
		Lint:       "blocker",
		Question:   BlockerQuestion,
		Choice:     ans.Choice,
		P:          conf,
		Confidence: conf,
		Threshold:  BlockerConfidence,
	}
	if conf < BlockerConfidence {
		a.Choice = Unclassified
		a.Flagged = true
	}
	return []ledger.Answer{a}, nil
}
