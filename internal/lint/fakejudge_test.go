package lint

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dpoage/llmkit/decide"
)

// fakeAnswer is one answer in the fake System One response, shaped like the
// vendor's wire format (decide/client.go's answerBody).
type fakeAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        *string            `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
}

// fakeRequest is the request envelope Ask posts to /v1/systemone.
type fakeRequest struct {
	State     json.RawMessage            `json:"state"`
	Model     string                     `json:"model"`
	Questions map[string]json.RawMessage `json:"questions"`
}

// newFakeJudge starts an httptest server implementing just enough of the
// TypeSafe System One wire protocol for these tests and returns a
// decide.Client pointed at it through decide.Config.BaseURL. answer runs
// once per request with the decoded state string and the question ids, and
// returns one fakeAnswer per id. Every response reports the model "jev-test".
func newFakeJudge(t *testing.T, answer func(state string, questions map[string]json.RawMessage) map[string]fakeAnswer) *decide.Client {
	t.Helper()
	return newFakeJudgeModel(t, func(state string, questions map[string]json.RawMessage) (string, map[string]fakeAnswer) {
		return "jev-test", answer(state, questions)
	})
}

// newFakeJudgeModel is newFakeJudge whose answer also names the model the
// response reports.
func newFakeJudgeModel(t *testing.T, answer func(state string, questions map[string]json.RawMessage) (string, map[string]fakeAnswer)) *decide.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req fakeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var state string
		if err := json.Unmarshal(req.State, &state); err != nil {
			http.Error(w, "state must be a string in these tests", http.StatusBadRequest)
			return
		}
		model, answers := answer(state, req.Questions)
		resp := map[string]any{
			"model":   model,
			"answers": answers,
			"usage":   map[string]any{"input_tokens": 1, "output_tokens": 0},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	client, err := decide.New(decide.Config{APIKey: "test-key", Model: "jev-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("decide.New: %v", err)
	}
	return client
}

// newErrorJudge returns a decide.Client whose every Ask fails with a terminal
// (non-retried) server error, for tests that need Ask itself to fail without
// touching the network.
func newErrorJudge(t *testing.T) *decide.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid request", http.StatusUnprocessableEntity)
	}))
	t.Cleanup(srv.Close)
	client, err := decide.New(decide.Config{APIKey: "test-key", Model: "jev-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("decide.New: %v", err)
	}
	return client
}

func nounQuestion(p float64) fakeAnswer {
	v := p
	return fakeAnswer{Type: "noul", Noul: &v}
}

func choiceQuestion(choice string, confidence float64) fakeAnswer {
	c := choice
	return fakeAnswer{Type: "choice", Choice: &c, Probabilities: map[string]float64{choice: confidence}, Confidence: confidence}
}
