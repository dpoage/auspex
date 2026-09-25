// Package verdict is the oracle verdict contract: the oracle output schema
// heed accepts (verdict, coverage, matrix, scope, reply), the rule that no
// other payload string names a disagreeing verdict, the matrix path
// convention, and the rule that an APPROVE names a matrix file that
// exists. Callers do not parse oracle transcripts themselves.
package verdict

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/dpoage/auspex/internal/ledger"
)

// Parsed is what heed needs from a transcript once it is known to be a
// verdict. Matrix is the path as the oracle wrote it (still local:// when
// the oracle used that scheme).
type Parsed struct {
	Approve  bool
	Coverage string
	Matrix   string
	Scope    []ledger.ScopeItem
}

// NotAVerdict reports a transcript heed refuses to record: anything but a
// JSON object carrying the oracle output schema, a verdict other than
// "VERDICT: APPROVE" or "VERDICT: REJECT", a payload string naming a
// disagreeing verdict, a matrix that differs from the coverage line's, or
// an APPROVE whose matrix is absent, missing, not a regular file or outside
// the local directory. The reason is a plain sentence naming the field;
// heed prints it under obnuntiatio and records nothing.
type NotAVerdict struct {
	Reason string
}

func (e *NotAVerdict) Error() string { return e.Reason }

func notAVerdict(format string, a ...any) error {
	return &NotAVerdict{Reason: fmt.Sprintf(format, a...)}
}

// The two verdict field values heed records. A premortem seat's
// "PLAN: PROCEED" and "PLAN: REVISE" match planVerdictRe instead.
const (
	approveVerdict = "VERDICT: APPROVE"
	rejectVerdict  = "VERDICT: REJECT"
)

var planVerdictRe = regexp.MustCompile(`^PLAN: (?:PROCEED|REVISE)$`)

// schemaField is one required field of the oracle output schema.
type schemaField struct {
	name string
	kind string // "a string" or "a list"
}

// schema is the oracle output schema's required fields, in the order Parse
// checks them.
var schema = []schemaField{
	{"verdict", "a string"},
	{"coverage", "a string"},
	{"matrix", "a string"},
	{"scope", "a list"},
	{"reply", "a string"},
}

// Parse reads an oracle transcript, which must be a JSON object carrying the
// oracle output schema, and returns the verdict it names or a *NotAVerdict.
// localDir is the directory a local:// matrix path resolves against; when
// it is given, an APPROVE's matrix must resolve inside it. It may be empty
// when no transcript names a local:// matrix.
func Parse(data []byte, localDir string) (Parsed, error) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil || payload == nil {
		return Parsed{}, notAVerdict("the transcript is not a JSON object carrying the oracle output schema (verdict, coverage, matrix, scope, reply)")
	}
	for _, f := range schema {
		v, ok := payload[f.name]
		if !ok {
			return Parsed{}, notAVerdict("the payload has no %s field", f.name)
		}
		if jsonKind(v) != f.kind {
			return Parsed{}, notAVerdict("the %s field is %s, not %s", f.name, jsonKind(v), f.kind)
		}
	}
	entries, err := scopeEntries(payload["scope"].([]any))
	if err != nil {
		return Parsed{}, err
	}

	rawVerdict := payload["verdict"].(string)
	var token string
	switch {
	case rawVerdict == approveVerdict:
		token = "APPROVE"
	case rawVerdict == rejectVerdict:
		token = "REJECT"
	case planVerdictRe.MatchString(rawVerdict):
		return Parsed{}, notAVerdict("the verdict field %q is a premortem plan verdict, which heed does not record", rawVerdict)
	default:
		return Parsed{}, notAVerdict("the verdict field %q is not %q or %q", rawVerdict, approveVerdict, rejectVerdict)
	}
	disagreeing := func(path, s string) bool {
		if word, found := disagreeingWord(fold(s), token); found {
			err = notAVerdict("the %s field contains the verdict word %s, disagreeing with the verdict field's %s", path, word, token)
		}
		return err != nil
	}
	if walkPayloadStrings(payload, "verdict", disagreeing) {
		return Parsed{}, err
	}

	coverage := payload["coverage"].(string)
	matrix := payload["matrix"].(string)
	if fromCoverage := matrixFromCoverage(coverage); matrix != fromCoverage {
		return Parsed{}, notAVerdict("the matrix field %q differs from the path %q after the dash in the coverage field", matrix, fromCoverage)
	}
	approve := token == "APPROVE"
	if approve {
		if err := checkMatrix(matrix, localDir); err != nil {
			return Parsed{}, err
		}
	}
	return Parsed{Approve: approve, Coverage: coverage, Matrix: matrix, Scope: itemsFrom(entries)}, nil
}

// scopeEntry is one {id, text} object of the scope field.
type scopeEntry struct {
	id, text string
}

// scopeEntries reads the scope list: every entry is an object with an id
// string and a non-blank text string. Other keys are ignored.
func scopeEntries(list []any) ([]scopeEntry, error) {
	entries := make([]scopeEntry, 0, len(list))
	for i, raw := range list {
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, notAVerdict("scope[%d] is %s, not an object with id and text", i+1, jsonKind(raw))
		}
		var e scopeEntry
		for _, f := range []struct {
			key string
			dst *string
		}{{"id", &e.id}, {"text", &e.text}} {
			v, ok := obj[f.key]
			if !ok {
				return nil, notAVerdict("scope[%d] has no %s", i+1, f.key)
			}
			s, ok := v.(string)
			if !ok {
				return nil, notAVerdict("scope[%d].%s is %s, not a string", i+1, f.key, jsonKind(v))
			}
			*f.dst = s
		}
		if strings.TrimSpace(e.text) == "" {
			return nil, notAVerdict("scope[%d].text is blank", i+1)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// itemsFrom makes every scope entry an item, with ids S<k> by position.
// The oracle's own id leads the item text.
func itemsFrom(entries []scopeEntry) []ledger.ScopeItem {
	var items []ledger.ScopeItem
	for i, e := range entries {
		text := e.text
		if id := strings.TrimSpace(e.id); id != "" {
			text = id + ": " + text
		}
		items = append(items, ledger.ScopeItem{ID: fmt.Sprintf("S%d", i+1), Text: text})
	}
	return items
}

func jsonKind(v any) string {
	switch v.(type) {
	case string:
		return "a string"
	case map[string]any:
		return "an object"
	case []any:
		return "a list"
	case float64:
		return "a number"
	case bool:
		return "a boolean"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", v)
}

// verdictWordRe finds the word "verdict" in folded text, in any case and
// inside a longer word ("FINAL_VERDICT" folds to "FINALVERDICT").
var verdictWordRe = regexp.MustCompile(`(?i)verdict`)

// verdictTokenWindow is how many words after "verdict" may carry the
// verdict word it names.
const verdictTokenWindow = 3

// foldDropped are the markdown, quoting and markup characters fold removes.
const foldDropped = "*_\"'[]<>`"

// fold normalizes a string for the disagreement rule: every Unicode space
// becomes a plain space, fullwidth ASCII (U+FF01-U+FF5E) becomes ASCII,
// and the characters of foldDropped are removed. It is a fold for the
// shapes oracles write, not full NFKC.
func fold(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0
		}
		switch {
		case unicode.IsSpace(r) || unicode.Is(unicode.Zs, r):
			return ' '
		case strings.ContainsRune(foldDropped, r):
			return -1
		}
		return r
	}, s)
}

// disagreeingWord looks through folded text for "verdict" followed, within
// verdictTokenWindow words, by a word beginning approv or reject whose
// polarity is not token. It also refuses an uppercase "VERDICT:" whose
// next word is some other all-uppercase word ("VERDICT: PENDING").
func disagreeingWord(folded, token string) (word string, found bool) {
	for _, loc := range verdictWordRe.FindAllStringIndex(folded, -1) {
		after := folded[loc[1]:]
		words := leadingWords(after, verdictTokenWindow)
		for _, w := range words {
			if p := polarity(w); p != "" && p != token {
				return w, true
			}
		}
		if folded[loc[0]:loc[1]] == "VERDICT" && strings.HasPrefix(strings.TrimLeft(after, " "), ":") &&
			len(words) > 0 && polarity(words[0]) == "" && isUpperWord(words[0]) {
			return words[0], true
		}
	}
	return "", false
}

// leadingWords returns up to n words (runs of letters and digits) from the
// start of s.
func leadingWords(s string, n int) []string {
	var words []string
	start := -1
	for i, r := range s {
		inWord := unicode.IsLetter(r) || unicode.IsDigit(r)
		switch {
		case inWord && start == -1:
			start = i
		case !inWord && start != -1:
			words = append(words, s[start:i])
			start = -1
			if len(words) == n {
				return words
			}
		}
	}
	if start != -1 {
		words = append(words, s[start:])
	}
	return words
}

// polarity is APPROVE for a word beginning approv, REJECT for one beginning
// reject (any case), else "".
func polarity(word string) string {
	w := strings.ToLower(word)
	switch {
	case strings.HasPrefix(w, "approv"):
		return "APPROVE"
	case strings.HasPrefix(w, "reject"):
		return "REJECT"
	}
	return ""
}

func isUpperWord(w string) bool {
	if len(w) < 2 {
		return false
	}
	for _, r := range w {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// walkPayloadStrings calls visit on every string value in the payload,
// except under the top-level key skip, with its path ("reply",
// "scope[1].text"), in a fixed order. It stops and returns true at the
// first visit that returns true.
func walkPayloadStrings(payload map[string]any, skip string, visit func(path, s string) bool) bool {
	for _, k := range sortedKeys(payload) {
		if k != skip && walkStrings(payload[k], k, visit) {
			return true
		}
	}
	return false
}

func walkStrings(v any, path string, visit func(path, s string) bool) bool {
	switch v := v.(type) {
	case string:
		return visit(path, v)
	case []any:
		for i, e := range v {
			if walkStrings(e, fmt.Sprintf("%s[%d]", path, i+1), visit) {
				return true
			}
		}
	case map[string]any:
		for _, k := range sortedKeys(v) {
			if walkStrings(v[k], path+"."+k, visit) {
				return true
			}
		}
	}
	return false
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// matrixFromCoverage is the path after the last em-dash in a coverage
// string, e.g. "13 families, 49 probes, 1 skipped — local://oracle-x.md",
// or "" when there is no em-dash.
func matrixFromCoverage(coverage string) string {
	const dash = "—"
	idx := strings.LastIndex(coverage, dash)
	if idx == -1 {
		return ""
	}
	return strings.TrimSpace(coverage[idx+len(dash):])
}

// checkMatrix is the APPROVE rule: the matrix is named and is a regular
// file. local:// needs localDir. When localDir is given, an absolute path
// is refused, and the matrix must resolve inside localDir after following
// symlinks.
func checkMatrix(matrix, localDir string) error {
	if matrix == "" {
		return notAVerdict("APPROVE with no matrix")
	}
	path := matrix
	if rest, ok := strings.CutPrefix(matrix, "local://"); ok {
		if localDir == "" {
			return notAVerdict("matrix %q is local:// but --local was not given", matrix)
		}
		if !filepath.IsLocal(rest) || filepath.Clean(rest) == "." {
			return notAVerdict("matrix %q does not resolve to a file inside %s", matrix, localDir)
		}
		path = filepath.Join(localDir, rest)
	} else if localDir != "" && filepath.IsAbs(matrix) {
		return notAVerdict("matrix %s is an absolute path; with --local it must resolve inside %s", matrix, localDir)
	}
	info, err := os.Stat(path)
	if err != nil {
		return notAVerdict("matrix %s does not exist", path)
	}
	if !info.Mode().IsRegular() {
		return notAVerdict("matrix %s is not a regular file", path)
	}
	if localDir == "" {
		return nil
	}
	real, err := resolvePath(path)
	if err != nil {
		return notAVerdict("matrix %s cannot be resolved: %v", path, err)
	}
	root, err := resolvePath(localDir)
	if err != nil {
		return notAVerdict("--local %s cannot be resolved: %v", localDir, err)
	}
	if rel, err := filepath.Rel(root, real); err != nil || !filepath.IsLocal(rel) {
		return notAVerdict("matrix %s resolves to %s, which is not inside %s", path, real, localDir)
	}
	return nil
}

// resolvePath is p made absolute with every symlink followed.
func resolvePath(p string) (string, error) {
	p, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}
