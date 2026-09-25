package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dpoage/llmkit/decide"
)

// commentCount reads bd's own comment_count for bead, so a test can assert
// nothing was recorded without depending on the ledger's comment format.
func commentCount(t *testing.T, bead string) int {
	t.Helper()
	out, err := exec.Command("bd", "show", bead, "--json").Output()
	if err != nil {
		t.Fatalf("bd show: %v", err)
	}
	var shown []struct {
		CommentCount int `json:"comment_count"`
	}
	if err := json.Unmarshal(out, &shown); err != nil || len(shown) != 1 {
		t.Fatalf("bd show --json: unexpected output %s (err %v)", out, err)
	}
	return shown[0].CommentCount
}

// fakeJudgeServer starts an httptest server answering every Ask with a
// fixed noul/choice value for every question id it receives, and returns a
// judgeFactory-shaped constructor pointed at it via decide.Config.BaseURL.
func fakeJudgeServer(t *testing.T, status int, body func(w http.ResponseWriter, r *http.Request)) func() (*decide.Client, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body != nil {
			body(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return func() (*decide.Client, error) {
		return decide.New(decide.Config{APIKey: "test-key", Model: "jev-test", BaseURL: srv.URL})
	}
}

// withJudgeFactory substitutes omen's judge constructor for the duration of
// the test.
func withJudgeFactory(t *testing.T, f func() (*decide.Client, error)) {
	t.Helper()
	prev := judgeFactory
	judgeFactory = f
	t.Cleanup(func() { judgeFactory = prev })
}

// appointBead appoints bead to a round so ledger.Append will accept a Lint
// record on it; tests then look for exactly one comment (the appointment)
// staying alone as proof that a failed omen recorded nothing further.
func appointBead(t *testing.T, bead string) {
	t.Helper()
	var out bytes.Buffer
	if code := runAppoint([]string{"--slice", bead, "--round", "jmg-1-test", "--tier", "heavy"}, &out, &out); code != exitSuccess {
		t.Fatalf("appoint: exit = %d: %s", code, out.String())
	}
}

// TestOmenFailsClosedWithoutKey and TestOmenFailsClosedOnJudgeError are
// criterion O5: a missing LLMKIT_TYPESAFE_API_KEY, or any decide error,
// exits nonzero with nothing recorded beyond the bead's appointment.
// Hiding mutant: record with p=0 (fabricate a Lint instead of erroring closed).
func TestOmenFailsClosedWithoutKey(t *testing.T) {
	_, dir := gitBDEnv(t)
	bead := createBead(t, "S4 omen O5 (no key)")
	appointBead(t, bead)
	t.Setenv("LLMKIT_TYPESAFE_API_KEY", "")

	inputPath := filepath.Join(dir, "reply.txt")
	if err := os.WriteFile(inputPath, []byte("the fix landed and tests pass."), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runOmen([]string{"reply", "--slice", bead, inputPath}, &stdout, &stderr)
	if code != exitObnuntiatio {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, exitObnuntiatio, stdout.String(), stderr.String())
	}
	if n := commentCount(t, bead); n != 1 {
		t.Fatalf("comment_count = %d, want 1 (only the appointment): a failed judge construction must record nothing", n)
	}
}

func TestOmenFailsClosedOnJudgeError(t *testing.T) {
	_, dir := gitBDEnv(t)
	bead := createBead(t, "S4 omen O5 (judge error)")
	appointBead(t, bead)
	t.Setenv("LLMKIT_TYPESAFE_API_KEY", "test-key")
	withJudgeFactory(t, fakeJudgeServer(t, http.StatusUnprocessableEntity, nil))

	inputPath := filepath.Join(dir, "reply.txt")
	if err := os.WriteFile(inputPath, []byte("the fix landed and tests pass."), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runOmen([]string{"reply", "--slice", bead, inputPath}, &stdout, &stderr)
	if code != exitObnuntiatio {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, exitObnuntiatio, stdout.String(), stderr.String())
	}
	if n := commentCount(t, bead); n != 1 {
		t.Fatalf("comment_count = %d, want 1 (only the appointment): a decide error must record nothing", n)
	}
}

// TestOmenRecordsAndPrintsFlags is a smoke test of the whole command: given
// a working judge, omen records exactly one lint record and prints one
// omen flag line per flagged unit, then exits 0 — a flag is advice, never a
// gate failure.
func TestOmenRecordsAndPrintsFlags(t *testing.T) {
	_, dir := gitBDEnv(t)
	bead := createBead(t, "S4 omen smoke test")
	t.Setenv("LLMKIT_TYPESAFE_API_KEY", "test-key")
	var appointOut bytes.Buffer
	if code := runAppoint([]string{"--slice", bead, "--round", "jmg-1-test", "--tier", "heavy"}, &appointOut, &appointOut); code != exitSuccess {
		t.Fatalf("appoint: exit = %d: %s", code, appointOut.String())
	}

	withJudgeFactory(t, fakeJudgeServer(t, 0, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			State     string                     `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		p := 0.0
		if strings.Contains(req.State, "no evidence at all") {
			p = 0.99
		}
		answers := map[string]any{}
		for id := range req.Questions {
			answers[id] = map[string]any{"type": "noul", "noul": p}
		}
		resp := map[string]any{
			"model":   "jev-test",
			"answers": answers,
			"usage":   map[string]any{"input_tokens": 1, "output_tokens": 0},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))

	inputPath := filepath.Join(dir, "reply.txt")
	text := "the fix landed with no evidence at all."
	if err := os.WriteFile(inputPath, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runOmen([]string{"reply", "--slice", bead, inputPath}, &stdout, &stderr)
	if code != exitSuccess {
		t.Fatalf("exit = %d, want 0 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "omen: unevidenced claim flagged at 0.99 (threshold 0.7)") {
		t.Fatalf("stdout = %q, want an omen flag line", stdout.String())
	}
	if n := commentCount(t, bead); n != 2 {
		t.Fatalf("comment_count = %d, want exactly 2 (the appointment plus the lint record)", n)
	}
}

// answeringJudge is a judgeFactory whose server answers every question of
// every request with answer, and counts the requests it served.
func answeringJudge(t *testing.T, answer map[string]any) (func() (*decide.Client, error), *int) {
	t.Helper()
	requests := 0
	return fakeJudgeServer(t, 0, func(w http.ResponseWriter, r *http.Request) {
		requests++
		var req struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		answers := map[string]any{}
		for id := range req.Questions {
			answers[id] = answer
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "jev-test",
			"answers": answers,
			"usage":   map[string]any{"input_tokens": 1, "output_tokens": 0},
		})
	}), &requests
}

// writeInput writes text to a file under dir and returns its path.
func writeInput(t *testing.T, dir, text string) string {
	t.Helper()
	path := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestOmenPrintsBlockerClass is criterion O3's print: a blocker under 0.8
// confidence prints as unclassified and needing the orchestrator's class;
// a classified blocker prints its class, never as flagged. Hiding mutants:
// suppress the unclassified line; print a classified blocker as flagged.
func TestOmenPrintsBlockerClass(t *testing.T) {
	for _, c := range []struct {
		name       string
		choice     string
		confidence float64
		want       string
	}{
		{"below 0.8", "PROSE", 0.71, "omen: blocker unclassified at 0.71 (threshold 0.8): needs the orchestrator's class\n"},
		{"at 0.8", "PRODUCT", 0.8, "omen: blocker PRODUCT at 0.8\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, dir := gitBDEnv(t)
			bead := createBead(t, "S4 omen blocker print")
			appointBead(t, bead)
			t.Setenv("LLMKIT_TYPESAFE_API_KEY", "test-key")
			judge, _ := answeringJudge(t, map[string]any{
				"type": "choice", "choice": c.choice,
				"probabilities": map[string]float64{c.choice: c.confidence}, "confidence": c.confidence,
			})
			withJudgeFactory(t, judge)

			var stdout, stderr bytes.Buffer
			code := runOmen([]string{"blocker", "--slice", bead, writeInput(t, dir, "the guard clause was never reached")}, &stdout, &stderr)
			if code != exitSuccess {
				t.Fatalf("exit = %d, want 0 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
			}
			_, lines, _ := strings.Cut(stdout.String(), "\n") // the first line is the record id
			if lines != c.want {
				t.Fatalf("omen printed %q after the record id, want exactly %q", lines, c.want)
			}
		})
	}
}

// TestOmenRefusesEmptyInput: empty or whitespace-only input, for any kind,
// is a usage error — exit 2, no judge call, nothing recorded. Hiding
// mutant: drop the empty-input check.
func TestOmenRefusesEmptyInput(t *testing.T) {
	_, dir := gitBDEnv(t)
	bead := createBead(t, "S4 omen empty input")
	appointBead(t, bead)
	t.Setenv("LLMKIT_TYPESAFE_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())
	judge, requests := answeringJudge(t, map[string]any{"type": "noul", "noul": 0.1})
	withJudgeFactory(t, judge)

	for _, kind := range []string{"brief", "fixlist", "reply", "blocker"} {
		for _, text := range []string{"", " \n\t\r\n \n"} {
			var stdout, stderr bytes.Buffer
			code := runOmen([]string{kind, "--slice", bead, writeInput(t, dir, text)}, &stdout, &stderr)
			if code != exitUsage {
				t.Fatalf("omen %s on %q: exit = %d, want %d (stdout=%q stderr=%q)", kind, text, code, exitUsage, stdout.String(), stderr.String())
			}
		}
	}
	if *requests != 0 {
		t.Fatalf("judge served %d requests, want 0 for empty input", *requests)
	}
	if n := commentCount(t, bead); n != 1 {
		t.Fatalf("comment_count = %d, want 1 (only the appointment): empty input must record nothing", n)
	}
}

// TestOmenBriefFailsWhenSkillsUnreadable: a skills directory that exists
// but cannot be read makes omen brief exit 1 with nothing recorded, rather
// than route over a partial skill set. Hiding mutant: ignore the ReadDir
// error.
func TestOmenBriefFailsWhenSkillsUnreadable(t *testing.T) {
	_, dir := gitBDEnv(t)
	bead := createBead(t, "S4 omen unreadable skills")
	appointBead(t, bead)
	t.Setenv("LLMKIT_TYPESAFE_API_KEY", "test-key")
	judge, _ := answeringJudge(t, map[string]any{"type": "noul", "noul": 0.1})
	withJudgeFactory(t, judge)

	home := t.TempDir()
	t.Setenv("HOME", home)
	skills := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(skills, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(skills, 0o755) })
	if _, err := os.ReadDir(skills); err == nil {
		t.Fatal("mode 000 does not deny this user (root?); the probe cannot run")
	}

	var stdout, stderr bytes.Buffer
	code := runOmen([]string{"brief", "--slice", bead, writeInput(t, dir, "Build the thing.")}, &stdout, &stderr)
	if code != exitObnuntiatio {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, exitObnuntiatio, stdout.String(), stderr.String())
	}
	if n := commentCount(t, bead); n != 1 {
		t.Fatalf("comment_count = %d, want 1 (only the appointment): an unreadable skills directory must record nothing", n)
	}
}

// TestOmenRefusesOutOfRangeAnswers: a judge answer outside its question's
// range exits 1 with nothing recorded. Hiding mutants: drop the probability
// range check; drop the class membership check; drop the confidence check.
func TestOmenRefusesOutOfRangeAnswers(t *testing.T) {
	for _, c := range []struct {
		name   string
		kind   string
		answer map[string]any
	}{
		{"noul outside [0,1]", "reply", map[string]any{"type": "noul", "noul": 1.5}},
		{"choice outside the classes", "blocker", map[string]any{"type": "choice", "choice": "OTHER", "confidence": 0.9}},
		{"choice without a confidence", "blocker", map[string]any{"type": "choice", "choice": "PRODUCT"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, dir := gitBDEnv(t)
			bead := createBead(t, "S4 omen out-of-range answer")
			appointBead(t, bead)
			t.Setenv("LLMKIT_TYPESAFE_API_KEY", "test-key")
			judge, _ := answeringJudge(t, c.answer)
			withJudgeFactory(t, judge)

			var stdout, stderr bytes.Buffer
			code := runOmen([]string{c.kind, "--slice", bead, writeInput(t, dir, "the fix landed.")}, &stdout, &stderr)
			if code != exitObnuntiatio {
				t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, exitObnuntiatio, stdout.String(), stderr.String())
			}
			if n := commentCount(t, bead); n != 1 {
				t.Fatalf("comment_count = %d, want 1 (only the appointment): a refused answer must record nothing", n)
			}
		})
	}
}
