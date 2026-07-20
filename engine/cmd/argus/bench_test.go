package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const benchScenarioYAML = `apiVersion: argus/v1alpha1
kind: BenchScenario
metadata:
  name: cardinality-explosion-checkout
spec:
  environment:
    app: otel-demo
  inject:
    - type: script
      script: noop.sh
      duration: 1m
  groundTruth:
    rootCauseEntities:
      - {kind: Deployment, namespace: otel-demo, name: checkout}
    category: cardinality-explosion
  scoring:
    entityMatch: jaccard
    partialCredit: true
`

func writeScenario(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "scenario.yaml")
	if err := os.WriteFile(p, []byte(benchScenarioYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// chatWithSubmit answers any chat-completions call with a submit_diagnosis
// tool call naming the correct root cause.
func chatWithSubmit(t *testing.T) *httptest.Server {
	t.Helper()
	args := `{"root_cause_entities":[{"kind":"Deployment","namespace":"otel-demo","name":"checkout"}],"category":"cardinality-explosion"}`
	body, err := json.Marshal(map[string]any{
		"choices": []map[string]any{{
			"message": map[string]any{
				"role": "assistant",
				"tool_calls": []map[string]any{{
					"id":       "c1",
					"type":     "function",
					"function": map[string]string{"name": "submit_diagnosis", "arguments": args},
				}},
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]int{"total_tokens": 120},
	})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
}

func mimirStub(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
	}))
}

func TestBenchRun_ScoresAndRendersMarkdown(t *testing.T) {
	chat := chatWithSubmit(t)
	defer chat.Close()
	mimir := mimirStub(t)
	defer mimir.Close()

	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetArgs([]string{
		"bench", "run",
		"--scenario", writeScenario(t),
		"--agent", "openai",
		"--endpoint", chat.URL,
		"--model", "test-model",
		"--mimir-url", mimir.URL,
		"--inject", "none",
		"--repeats", "2",
		"--env-digest", "unit-test",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("bench run: %v", err)
	}

	md := out.String()
	for _, want := range []string{
		"# Argus Bench",
		"cardinality-explosion-checkout",
		"test-model",
		"unit-test",
		"Method and caveats",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report missing %q:\n%s", want, md)
		}
	}
	// Correct answer twice -> perfect mean, deterministic normalization.
	if !strings.Contains(md, "1.00 ± 0.00") {
		t.Errorf("expected perfect mean score:\n%s", md)
	}
	if strings.Contains(md, "llm-judge") {
		t.Errorf("no judge configured, yet report mentions llm-judge:\n%s", md)
	}
}

func TestBenchRun_JSONToFile(t *testing.T) {
	chat := chatWithSubmit(t)
	defer chat.Close()
	mimir := mimirStub(t)
	defer mimir.Close()

	outPath := filepath.Join(t.TempDir(), "report.json")
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{
		"bench", "run",
		"--scenario", writeScenario(t),
		"--endpoint", chat.URL, "--model", "m",
		"--mimir-url", mimir.URL,
		"--inject", "none",
		"--format", "json", "--out", outPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("bench run: %v", err)
	}

	b, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Scenario     string `json:"scenario"`
		ScenarioHash string `json:"scenario_hash"`
		Summary      struct {
			Diagnoses int `json:"diagnoses"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if rep.Scenario != "cardinality-explosion-checkout" || rep.ScenarioHash == "" || rep.Summary.Diagnoses != 1 {
		t.Errorf("report = %+v", rep)
	}
}

func TestBenchRun_RejectsBadFlags(t *testing.T) {
	cases := [][]string{
		{"bench", "run", "--scenario", "missing.yaml"},                      // unreadable scenario
		{"bench", "run", "--scenario", writeScenario(t), "--agent", "nope"}, // unknown agent
		{"bench", "run", "--scenario", writeScenario(t), "--model", "m"},    // openai without endpoint
	}
	for _, args := range cases {
		root := newRootCmd()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Errorf("args %v: expected error", args)
		}
	}
}
