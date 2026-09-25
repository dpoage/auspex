package verdict_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dpoage/auspex/internal/verdict"
)

// fixtureLocal stands in for a session's local/ directory: it holds a
// fixture file for every matrix the APPROVE fixtures name.
var fixtureLocal = filepath.Join("testdata", "local")

// exampleCoverage names testdata/local/oracle-example-b.md, which exists.
const exampleCoverage = "1 family, 2 probes, 0 skipped — local://oracle-example-b.md"

type expectedEntry struct {
	File    string   `json:"file"`
	Verdict string   `json:"verdict"`
	Scope   []string `json:"scope"`
}

func loadExpected(t *testing.T) []expectedEntry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "expected.json"))
	if err != nil {
		t.Fatalf("reading testdata/expected.json: %v", err)
	}
	var entries []expectedEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decoding testdata/expected.json: %v", err)
	}
	if len(entries) != 25 {
		t.Fatalf("testdata/expected.json has %d entries, want 25", len(entries))
	}
	return entries
}

// requireNotAVerdict fails the test unless err is a *NotAVerdict whose
// reason contains want.
func requireNotAVerdict(t *testing.T, err error, want string) {
	t.Helper()
	var nav *verdict.NotAVerdict
	if !errors.As(err, &nav) {
		t.Fatalf("Parse error = %v, want *NotAVerdict", err)
	}
	if !strings.Contains(nav.Reason, want) {
		t.Fatalf("NotAVerdict reason = %q, want it to mention %q", nav.Reason, want)
	}
}

// schemaPayload is a valid APPROVE payload carrying the oracle output
// schema, with fields overridden (a nil value deletes the field).
func schemaPayload(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	p := map[string]any{
		"verdict":  "VERDICT: APPROVE",
		"coverage": exampleCoverage,
		"matrix":   "local://oracle-example-b.md",
		"scope":    []any{},
		"reply":    "All held.\n\nCoverage: " + exampleCoverage + "\nVERDICT: APPROVE",
	}
	for k, v := range fields {
		if v == nil {
			delete(p, k)
		} else {
			p[k] = v
		}
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// scopeTexts returns the item texts of parsed, failing the test on a
// misnumbered id.
func scopeTexts(t *testing.T, parsed verdict.Parsed) []string {
	t.Helper()
	var got []string
	for i, item := range parsed.Scope {
		if want := fmt.Sprintf("S%d", i+1); item.ID != want {
			t.Fatalf("Scope[%d].ID = %q, want %q", i, item.ID, want)
		}
		got = append(got, item.Text)
	}
	return got
}

// TestParseRealCorpusOriginalsRefused defends H5: none of the 25 real
// round-1bk-1 payloads carries the full oracle output schema, so each is
// NotAVerdict; the orchestrator records them from testdata/normalized.
// Mutant to expose: read a scope entry's "finding" when it has no "text"
// (the round-2 form); OracleEdgeA2 and OracleEdgeA3 then parse.
func TestParseRealCorpusOriginalsRefused(t *testing.T) {
	for _, e := range loadExpected(t) {
		t.Run(e.File, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", e.File))
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := verdict.Parse(data, fixtureLocal)
			var nav *verdict.NotAVerdict
			if !errors.As(err, &nav) {
				t.Fatalf("Parse = %+v, %v; want *NotAVerdict", parsed, err)
			}
			t.Logf("refused: %s", nav.Reason)
		})
	}
}

// TestParseRealCorpusNormalized defends H5: each hand-normalized copy in
// testdata/normalized (built from the original and expected.json) parses
// to its labelled verdict, with one item per label, in order, and the
// matrix from its coverage line. No item text repeats its S<k> label.
// Mutant to expose: refuse a scope text that spans lines, as three real
// findings do.
func TestParseRealCorpusNormalized(t *testing.T) {
	for _, e := range loadExpected(t) {
		t.Run(e.File, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "normalized", e.File))
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := verdict.Parse(data, fixtureLocal)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := map[bool]string{true: "APPROVE", false: "REJECT"}[parsed.Approve]; got != e.Verdict {
				t.Errorf("verdict = %s, want %s", got, e.Verdict)
			}
			if !strings.HasSuffix(parsed.Coverage, "— "+parsed.Matrix) {
				t.Errorf("Matrix = %q, want the path ending Coverage %q", parsed.Matrix, parsed.Coverage)
			}
			got := scopeTexts(t, parsed)
			if len(got) != len(e.Scope) {
				t.Fatalf("scope has %d items, want %d: %q", len(got), len(e.Scope), got)
			}
			for i, text := range got {
				if doubledLabelRe.MatchString(text) {
					t.Errorf("scope[%d].Text %q repeats its S<k> label", i, text)
				}
			}
		})
	}
}

// doubledLabelRe is an item text whose oracle id is followed by another
// S<k> label, as in "S1: **S1:** foo".
var doubledLabelRe = regexp.MustCompile(`^S\d+: [*_]*S\d+\b`)

// TestParseRequiresOutputSchema defends H1: only a JSON object carrying
// all five fields of the oracle output schema, with the right types, and a
// verdict of exactly "VERDICT: APPROVE" or "VERDICT: REJECT", is a verdict.
// Every refusal names the field. Mutants: accept a missing scope; accept
// plain text.
func TestParseRequiresOutputSchema(t *testing.T) {
	t.Run("not a JSON object", func(t *testing.T) {
		for _, data := range []string{
			"All held.\n\nCoverage: " + exampleCoverage + "\nVERDICT: APPROVE\n",
			"## SCOPE\n- S1: foo.\n\nCoverage: " + exampleCoverage + "\nVERDICT: REJECT",
			`[{"verdict":"VERDICT: REJECT"}]`,
			`null`,
			`"VERDICT: APPROVE"`,
			string(schemaPayload(t, nil)) + "\nVERDICT: APPROVE",
		} {
			t.Run(data, func(t *testing.T) {
				_, err := verdict.Parse([]byte(data), fixtureLocal)
				requireNotAVerdict(t, err, "not a JSON object")
			})
		}
	})
	for _, field := range []string{"verdict", "coverage", "matrix", "scope", "reply"} {
		t.Run("missing "+field, func(t *testing.T) {
			_, err := verdict.Parse(schemaPayload(t, map[string]any{field: nil}), fixtureLocal)
			requireNotAVerdict(t, err, "no "+field+" field")
		})
		wrong := []any{42, true, map[string]any{}, json.RawMessage("null")}
		if field == "scope" {
			wrong = append(wrong, "S1: foo", "")
		} else {
			wrong = append(wrong, []any{"VERDICT: APPROVE"})
		}
		for _, v := range wrong {
			t.Run(fmt.Sprintf("%s is %v", field, v), func(t *testing.T) {
				_, err := verdict.Parse(schemaPayload(t, map[string]any{field: v}), fixtureLocal)
				requireNotAVerdict(t, err, "the "+field+" field is")
			})
		}
	}
	for _, c := range []struct {
		scope  string
		reason string
	}{
		{`["S1: foo"]`, "scope[1] is a string"},
		{`[["S1","foo"]]`, "scope[1] is a list"},
		{`[{"text":"foo"}]`, "scope[1] has no id"},
		{`[{"id":"S1"}]`, "scope[1] has no text"},
		{`[{"id":"S1","text":"foo"},{"id":"S2"}]`, "scope[2] has no text"},
		{`[{"id":1,"text":"foo"}]`, "scope[1].id is a number"},
		{`[{"id":"S1","text":null}]`, "scope[1].text is null"},
		{`[{"id":"S1","text":""}]`, "scope[1].text is blank"},
		{`[{"id":"S1","text":"   "}]`, "scope[1].text is blank"},
		{`[{"id":"S1","text":"\u00a0\n"}]`, "scope[1].text is blank"},
		{`[{"id":"S1","text":"foo"},{"id":"S2","text":""}]`, "scope[2].text is blank"},
	} {
		t.Run("scope "+c.scope, func(t *testing.T) {
			_, err := verdict.Parse(schemaPayload(t, map[string]any{"scope": json.RawMessage(c.scope)}), fixtureLocal)
			requireNotAVerdict(t, err, c.reason)
		})
	}
	for _, v := range []string{
		"PLAN: PROCEED", "PLAN: REVISE", "APPROVE", "REJECT", "VERDICT: PENDING", "VERDICT: APPROVED",
		"verdict: approve", "VERDICT: approve", " VERDICT: APPROVE", "VERDICT: APPROVE\n", "VERDICT:  APPROVE", "",
	} {
		t.Run(fmt.Sprintf("verdict %q", v), func(t *testing.T) {
			_, err := verdict.Parse(schemaPayload(t, map[string]any{"verdict": v}), fixtureLocal)
			requireNotAVerdict(t, err, "the verdict field")
		})
	}
	t.Run("extra fields are allowed", func(t *testing.T) {
		parsed, err := verdict.Parse(schemaPayload(t, map[string]any{
			"model":    "m",
			"blocking": []any{},
			"scope":    json.RawMessage(`[{"id":"S1","text":"foo","severity":"low"}]`),
		}), fixtureLocal)
		if err != nil || !parsed.Approve || len(parsed.Scope) != 1 {
			t.Fatalf("Parse = %+v, %v; want APPROVE with 1 item", parsed, err)
		}
	})
	t.Run("REJECT parses", func(t *testing.T) {
		parsed, err := verdict.Parse(schemaPayload(t, map[string]any{
			"verdict": "VERDICT: REJECT", "reply": "B1 fails.\n\nVERDICT: REJECT",
		}), fixtureLocal)
		if err != nil || parsed.Approve {
			t.Fatalf("Parse = %+v, %v; want REJECT", parsed, err)
		}
	})
}

// TestParseScopeOnlyFromScopeField defends H2: items come only from the
// scope field, in order, with ids S<k> by position and the oracle's own
// id at the front of the text. A SCOPE section in reply is never read.
// Mutant: fall back to the reply's text when scope is [].
func TestParseScopeOnlyFromScopeField(t *testing.T) {
	reply := "## SCOPE\n- S1: the reply's foo.\n- S2: the reply's bar.\n\nCoverage: " + exampleCoverage + "\nVERDICT: REJECT"
	parse := func(scope string) verdict.Parsed {
		t.Helper()
		parsed, err := verdict.Parse(schemaPayload(t, map[string]any{
			"verdict": "VERDICT: REJECT", "reply": reply, "scope": json.RawMessage(scope),
		}), fixtureLocal)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		return parsed
	}
	t.Run("scope [] beside a reply SCOPE section has no items", func(t *testing.T) {
		if got := scopeTexts(t, parse(`[]`)); len(got) != 0 {
			t.Fatalf("Scope texts = %q, want none", got)
		}
	})
	t.Run("scope field items in order", func(t *testing.T) {
		got := scopeTexts(t, parse(`[{"id":"S3","text":"foo has no callers."},{"id":"B-1","text":"bar duplicates baz."},{"id":"","text":"qux is dead."}]`))
		want := []string{"S3: foo has no callers.", "B-1: bar duplicates baz.", "qux is dead."}
		if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
			t.Fatalf("Scope texts = %q, want %q", got, want)
		}
	})
}

// TestParseEveryScopeEntryIsAnItem: under the schema every scope entry is
// an item, a none statement included; an oracle with no items yields
// scope: []. Mutant: drop none-style entries (the V5 rule the schema
// replaced).
func TestParseEveryScopeEntryIsAnItem(t *testing.T) {
	parsed, err := verdict.Parse(schemaPayload(t, map[string]any{
		"verdict": "VERDICT: REJECT", "reply": "VERDICT: REJECT",
		"scope": json.RawMessage(`[{"id":"S1","text":"none"},{"id":"S2","text":"None new."},{"id":"S3","text":"SCOPE: none"},{"id":"S4","text":"**None.**"}]`),
	}), fixtureLocal)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"S1: none", "S2: None new.", "S3: SCOPE: none", "S4: **None.**"}
	if got := scopeTexts(t, parsed); fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Fatalf("Scope texts = %q, want %q", got, want)
	}
}

// TestParsePayloadDisagreeingReplyToken is V4: a VERDICT: token in reply
// that disagrees with the verdict field is NotAVerdict. Mutant: ignore
// reply tokens.
func TestParsePayloadDisagreeingReplyToken(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "payload-disagreeing-reply.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = verdict.Parse(data, fixtureLocal)
	requireNotAVerdict(t, err, "disagreeing")
}

// TestParsePayloadDisagreeingTokenAnywhere: a VERDICT token that disagrees
// with the verdict field makes the payload NotAVerdict wherever it sits
// (reply, coverage, a scope id or text, any extra field, list entry, or
// nested object) and however it is written (mixed case, markdown
// decoration). Mutant: scan only reply and report.
func TestParsePayloadDisagreeingTokenAnywhere(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
	}{
		{"final_line", map[string]any{"final_line": "VERDICT: REJECT"}},
		{"report", map[string]any{"report": "Findings below.\n\nVERDICT: REJECT"}},
		{"summary", map[string]any{"summary": "VERDICT: REJECT until B1 is fixed"}},
		{"nested object in a list", map[string]any{"blocking": json.RawMessage(`[{"id":"B1","detail":{"note":"so VERDICT: REJECT"}}]`)}},
		{"scope text", map[string]any{"scope": json.RawMessage(`[{"id":"S1","text":"round 0 VERDICT: REJECT stands"}]`)}},
		{"scope id", map[string]any{"scope": json.RawMessage(`[{"id":"VERDICT: REJECT","text":"foo"}]`)}},
		{"coverage", map[string]any{"coverage": "verdict reject, 1 family — local://oracle-example-b.md"}},
		{"lowercase word", map[string]any{"notes": "verdict: reject"}},
		{"mixed case", map[string]any{"reply": "All held.\n\nVerdict: REJECT"}},
		{"bold label", map[string]any{"reply": "**VERDICT:** REJECT"}},
		{"bold word", map[string]any{"reply": "VERDICT: **REJECT**"}},
		{"code word", map[string]any{"reply": "VERDICT: `REJECT`"}},
		{"uppercase non-verdict word", map[string]any{"reply": "VERDICT: PENDING"}},
		{"REJECT payload, APPROVE final_line", map[string]any{"verdict": "VERDICT: REJECT", "reply": "VERDICT: REJECT", "final_line": "VERDICT: APPROVE"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := verdict.Parse(schemaPayload(t, c.fields), fixtureLocal)
			requireNotAVerdict(t, err, "disagreeing")
		})
	}
	t.Run("agreeing tokens and prose parse", func(t *testing.T) {
		parsed, err := verdict.Parse(schemaPayload(t, map[string]any{
			"final_line": "VERDICT: APPROVE", "reply": "**Verdict:** approve", "notes": "The round 0 verdict: approval was withheld.",
		}), fixtureLocal)
		if err != nil || !parsed.Approve {
			t.Fatalf("Parse = %+v, %v; want APPROVE", parsed, err)
		}
	})
}

// TestParseDisagreementFoldedForms: after folding (Unicode spaces to a
// space, fullwidth to ASCII, markdown and quote characters dropped), a
// "verdict" followed within 3 words by a word beginning approv or reject of
// the other polarity is NotAVerdict, in any payload string. Mutant: the
// round-1 token regexp.
func TestParseDisagreementFoldedForms(t *testing.T) {
	forms := []string{
		"_VERDICT: REJECT_",
		"__VERDICT:__ REJECT",
		"FINAL_VERDICT: REJECT",
		"seat_B_VERDICT: REJECT",
		`VERDICT: "REJECT"`,
		"VERDICT: [REJECT]",
		"VERDICT:\u00a0REJECT",
		"\uff36\uff25\uff32\uff24\uff29\uff23\uff34\uff1a\uff32\uff25\uff2a\uff25\uff23\uff34",
		"VERDICT: Rejected",
		"<b>VERDICT:</b> REJECT",
		"VERDICT — REJECT",
		"VERDICT:\nREJECT",
	}
	for _, form := range forms {
		for _, key := range []string{"reply", "final_line"} {
			t.Run(key+" "+form, func(t *testing.T) {
				_, err := verdict.Parse(schemaPayload(t, map[string]any{key: "Round 0 said " + form}), fixtureLocal)
				requireNotAVerdict(t, err, "disagreeing")
			})
		}
	}
	t.Run("agreeing folded forms parse", func(t *testing.T) {
		parsed, err := verdict.Parse(schemaPayload(t, map[string]any{
			"final_line": "_VERDICT:\u00a0Approved_", "reply": "Verdict: I think this is fine.",
		}), fixtureLocal)
		if err != nil || !parsed.Approve {
			t.Fatalf("Parse = %+v, %v; want APPROVE", parsed, err)
		}
	})
}

// approveWithMatrix is a schema APPROVE payload whose coverage line names
// matrix, so the matrix field agrees with it.
func approveWithMatrix(t *testing.T, matrix string) []byte {
	t.Helper()
	return schemaPayload(t, map[string]any{"coverage": "1 family, 2 probes, 0 skipped — " + matrix, "matrix": matrix})
}

// TestParseMatrixMatchesCoverage is H4: the matrix field must be exactly
// the path after the last em-dash of the coverage field, for APPROVE and
// REJECT alike. Mutant: skip the comparison.
func TestParseMatrixMatchesCoverage(t *testing.T) {
	for _, c := range []struct {
		name, verdict, coverage, matrix string
	}{
		{"REJECT naming another matrix", "VERDICT: REJECT", "1 family — local://oracle-x-A.md", "local://oracle-x-B.md"},
		{"APPROVE naming an existing matrix the coverage does not", "VERDICT: APPROVE", "1 family — local://oracle-router-B.md", "local://oracle-example-b.md"},
		{"coverage with no dash", "VERDICT: REJECT", "1 family, 2 probes, 0 skipped", "local://oracle-x.md"},
		{"coverage with a hyphen, not an em-dash", "VERDICT: APPROVE", "1 family - local://oracle-example-b.md", "local://oracle-example-b.md"},
		{"the path after the first of two dashes", "VERDICT: APPROVE", "carried from — local://oracle-example-b.md — local://oracle-router-B.md", "local://oracle-example-b.md"},
		{"matrix with a trailing space", "VERDICT: APPROVE", exampleCoverage, "local://oracle-example-b.md "},
		{"empty matrix beside a coverage path", "VERDICT: REJECT", exampleCoverage, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := verdict.Parse(schemaPayload(t, map[string]any{
				"verdict": c.verdict, "reply": c.verdict, "coverage": c.coverage, "matrix": c.matrix,
			}), fixtureLocal)
			requireNotAVerdict(t, err, "differs from the path")
		})
	}
	t.Run("a REJECT's agreeing matrix is not checked on disk", func(t *testing.T) {
		parsed, err := verdict.Parse(schemaPayload(t, map[string]any{
			"verdict": "VERDICT: REJECT", "reply": "VERDICT: REJECT",
			"coverage": "1 family — local://absent.md", "matrix": "local://absent.md",
		}), fixtureLocal)
		if err != nil || parsed.Approve || parsed.Matrix != "local://absent.md" {
			t.Fatalf("Parse = %+v, %v; want REJECT with matrix local://absent.md", parsed, err)
		}
	})
}

// TestParseMatrixMustBeRegularFileInsideLocal is V2's rule as the parser
// owns it: an APPROVE's matrix must resolve (local:// strictly inside the
// local directory) to a regular file. Mutant: a check that accepts
// directories.
func TestParseMatrixMustBeRegularFileInsideLocal(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "local")
	if err := os.MkdirAll(filepath.Join(local, "dir.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(local, "m.md"), filepath.Join(root, "outside.md")} {
		if err := os.WriteFile(f, []byte("matrix"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := verdict.Parse(approveWithMatrix(t, "local://m.md"), local); err != nil {
		t.Fatalf("a regular local:// matrix: %v", err)
	}
	cases := []struct {
		name, matrix, local, reason string
	}{
		{"local:// directory", "local://dir.md", local, "not a regular file"},
		{"absolute directory without --local", local, "", "not a regular file"},
		{"the local directory itself", "local://", local, "inside"},
		{"local://./", "local://./", local, "inside"},
		{"escaping the local directory", "local://../outside.md", local, "inside"},
		{"missing file", "local://absent.md", local, "does not exist"},
		{"local:// without a local directory", "local://m.md", "", "--local"},
		{"no matrix", "", local, "no matrix"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := verdict.Parse(approveWithMatrix(t, c.matrix), c.local)
			requireNotAVerdict(t, err, c.reason)
		})
	}
}

// TestParseMatrixResolvesInsideLocal: with --local, the matrix must
// resolve inside it after following symlinks, and an absolute matrix path
// is refused; without --local an absolute path is accepted. Mutants: skip
// symlink resolution; accept an absolute path with --local.
func TestParseMatrixResolvesInsideLocal(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "local")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(local, "m.md"), filepath.Join(root, "outside.md")} {
		if err := os.WriteFile(f, []byte("matrix"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		filepath.Join(local, "link-out.md"): filepath.Join(root, "outside.md"),
		filepath.Join(local, "link-root"):   root,
		filepath.Join(local, "link-in.md"):  "m.md",
		filepath.Join(root, "local-link"):   local,
	}
	for link, target := range links {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ name, matrix, local, reason string }{
		{"symlink to a file outside --local", "local://link-out.md", local, "not inside"},
		{"through a symlinked directory", "local://link-root/outside.md", local, "not inside"},
		{"symlink outside, --local itself a symlink", "local://link-out.md", filepath.Join(root, "local-link"), "not inside"},
		{"absolute path inside --local", filepath.Join(local, "m.md"), local, "absolute"},
		{"absolute path outside --local", filepath.Join(root, "outside.md"), local, "absolute"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := verdict.Parse(approveWithMatrix(t, c.matrix), c.local)
			requireNotAVerdict(t, err, c.reason)
		})
	}
	for _, c := range []struct{ name, matrix, local string }{
		{"symlink inside --local", "local://link-in.md", local},
		{"--local itself a symlink", "local://m.md", filepath.Join(root, "local-link")},
		{"absolute path without --local", filepath.Join(root, "outside.md"), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := verdict.Parse(approveWithMatrix(t, c.matrix), c.local); err != nil {
				t.Fatalf("Parse: %v", err)
			}
		})
	}
}
