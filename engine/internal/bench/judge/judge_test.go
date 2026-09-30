package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func chatServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		b, err := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": content}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}))
}

func newJudge(srv *httptest.Server) *LLMJudge {
	return New(Config{Endpoint: srv.URL, Model: "m", HTTP: srv.Client()})
}

const cleanJSON = `{"root_cause_entities":[{"kind":"Deployment","namespace":"otel-demo","name":"checkout"}],"category":"cardinality-explosion"}`

func TestJudge_Method(t *testing.T) {
	if m := New(Config{}).Method(); m != "llm-judge" {
		t.Errorf("Method() = %q, want llm-judge", m)
	}
}

func TestJudge_NormalizeCleanJSON(t *testing.T) {
	srv := chatServer(t, cleanJSON)
	defer srv.Close()

	d, err := newJudge(srv).Normalize(context.Background(), []byte("checkout blew up cardinality"), "the-scenario")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if d.Scenario != "the-scenario" {
		t.Errorf("scenario = %q, want forced authoritative", d.Scenario)
	}
	if len(d.RootCauseEntities) != 1 || d.RootCauseEntities[0].Name != "checkout" {
		t.Errorf("entities = %+v", d.RootCauseEntities)
	}
}

func TestJudge_StripsFencesAndProse(t *testing.T) {
	cases := map[string]string{
		"json fence":   "```json\n" + cleanJSON + "\n```",
		"bare fence":   "```\n" + cleanJSON + "\n```",
		"prose around": "Here is the JSON you asked for:\n" + cleanJSON + "\nHope that helps!",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			srv := chatServer(t, content)
			defer srv.Close()
			d, err := newJudge(srv).Normalize(context.Background(), []byte("raw"), "s")
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			if d.Category != "cardinality-explosion" {
				t.Errorf("category = %q", d.Category)
			}
		})
	}
}

func TestJudge_RejectsUnparseableReply(t *testing.T) {
	srv := chatServer(t, "I could not determine the root cause.")
	defer srv.Close()
	if _, err := newJudge(srv).Normalize(context.Background(), []byte("raw"), "s"); err == nil {
		t.Error("expected error on non-JSON reply")
	}
}

func TestJudge_RejectsInvalidDiagnosis(t *testing.T) {
	// Well-formed JSON, but no entities — must fail validation, not score as zero.
	srv := chatServer(t, `{"root_cause_entities":[],"category":"x"}`)
	defer srv.Close()
	_, err := newJudge(srv).Normalize(context.Background(), []byte("raw"), "s")
	if err == nil || !strings.Contains(err.Error(), "root_cause_entities") {
		t.Errorf("expected validation error, got %v", err)
	}
}

func TestJudge_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, "nope")
	}))
	defer srv.Close()
	if _, err := newJudge(srv).Normalize(context.Background(), []byte("raw"), "s"); err == nil {
		t.Error("expected HTTP error")
	}
}

func TestExtractJSON(t *testing.T) {
	tests := map[string]string{
		`{"a":1}`:                 `{"a":1}`,
		"```json\n{\"a\":1}\n```": `{"a":1}`,
		"text {\"a\":1} more":     `{"a":1}`,
		"no json here":            "no json here",
	}
	for in, want := range tests {
		if got := extractJSON(in); got != want {
			t.Errorf("extractJSON(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestJudge_ExtractsEvidence is the regression guard for B-01. The judge's
// extraction shape used to omit evidence, so every judge-normalized answer had
// zero citations and every prose agent scored 0 on a requireEvidence scenario.
// A test cannot make a model obey a prompt, so this pins what the code
// controls: the prompt asks for evidence and forbids inventing it, and evidence
// the judge returns reaches the Diagnosis intact.
func TestJudge_ExtractsEvidence(t *testing.T) {
	var sentPrompt string
	reply := `{"root_cause_entities":[{"kind":"Deployment","namespace":"otel-demo","name":"frontend"}],` +
		`"category":"cardinality-explosion",` +
		`"evidence":[{"signal":"metrics","query":"count(app_frontend_requests_total)","observation":"series tripled"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for _, m := range body.Messages {
			sentPrompt += m.Content + "\n"
		}
		b, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": reply}}}})
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	d, err := newJudge(srv).Normalize(context.Background(), []byte("I queried Mimir and saw series triple on frontend."), "s")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"evidence"`, "never supply a citation", `"evidence": []`} {
		if !strings.Contains(sentPrompt, want) {
			t.Errorf("judge prompt is missing %q — without it prose agents cannot be credited for evidence, "+
				"or can be credited for evidence they never gave", want)
		}
	}
	if len(d.Evidence) != 1 || d.Evidence[0].Signal != "metrics" || d.Evidence[0].Observation != "series tripled" {
		t.Fatalf("Evidence = %+v, want the one citation the judge returned", d.Evidence)
	}
}
