package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeOllamaShow stands in for Ollama's management API on loopback, serving the
// given JSON string as the "parameters" block of /api/show.
func fakeOllamaShow(t *testing.T, paramsJSON string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"parameters": ` + paramsJSON +
			`, "details": {"quantization_level":"Q4_K_M"}, "model_info": {"x.context_length": 262144}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// runBenchExpectingError executes `bench run` with the given extra args and
// returns the error, which these tests require to be non-nil.
func runBenchExpectingError(t *testing.T, extra ...string) error {
	t.Helper()
	args := append([]string{"bench", "run", "--scenario", writeScenario(t)}, extra...)
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		t.Fatalf("bench run %v: expected an error, got nil", extra)
	}
	return err
}

// TestBenchRefusesRemoteEndpoints is the money guard: no bench run may reach a
// paid API by default.
func TestBenchRefusesRemoteEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"https://api.openai.com/v1/chat/completions",
		"http://10.0.0.5:11434/v1/chat/completions",
	} {
		err := runBenchExpectingError(t,
			"--agent", "openai", "--endpoint", endpoint, "--model", "m",
			"--mimir-url", "http://127.0.0.1:1", "--inject", "none")
		if !strings.Contains(err.Error(), "not loopback") {
			t.Errorf("endpoint %q: error = %v, want a loopback refusal", endpoint, err)
		}
	}
}

// TestBenchRefusesAPIKeyEnv: under local-only there is nothing to authenticate
// to, so a key being configured at all means the run is pointed somewhere wrong.
func TestBenchRefusesAPIKeyEnv(t *testing.T) {
	err := runBenchExpectingError(t,
		"--agent", "openai", "--endpoint", "http://127.0.0.1:11434/v1/chat/completions",
		"--model", "m", "--api-key-env", "OPENAI_API_KEY",
		"--mimir-url", "http://127.0.0.1:1", "--inject", "none")
	if !strings.Contains(err.Error(), "API keys are forbidden") {
		t.Fatalf("error = %v, want an API-key refusal", err)
	}
}

// TestBenchRefusesJudgeSharingAgentModel: one model must not both produce and
// normalize an answer, or scoring inherits the agent's own errors.
func TestBenchRefusesJudgeSharingAgentModel(t *testing.T) {
	err := runBenchExpectingError(t,
		"--agent", "openai", "--endpoint", "http://127.0.0.1:11434/v1/chat/completions",
		"--model", "qwen3.6:35b", "--mimir-url", "http://127.0.0.1:1", "--inject", "none",
		"--judge-endpoint", "http://127.0.0.1:11434/v1/chat/completions",
		"--judge-model", "qwen3.6:35b")
	if !strings.Contains(err.Error(), "judge model must differ") {
		t.Fatalf("error = %v, want a judge/agent model refusal", err)
	}
}

// TestBenchJudgeGuardAppliesEvenWithoutLocalOnly: the judge rule is about
// scoring integrity, not cost, so switching off local-only must not switch it
// off too.
func TestBenchJudgeGuardAppliesEvenWithoutLocalOnly(t *testing.T) {
	err := runBenchExpectingError(t,
		"--local-only=false",
		"--agent", "openai", "--endpoint", "https://api.openai.com/v1/chat/completions",
		"--model", "gpt-x", "--mimir-url", "http://127.0.0.1:1", "--inject", "none",
		"--judge-endpoint", "https://api.openai.com/v1/chat/completions",
		"--judge-model", "GPT-X")
	if !strings.Contains(err.Error(), "judge model must differ") {
		t.Fatalf("error = %v, want a judge/agent model refusal", err)
	}
}

// TestBenchAbortsOnUnsetNumCtx: a model with no explicit num_ctx is served at
// the runtime default and truncates silently, so the run must not start.
func TestBenchAbortsOnUnsetNumCtx(t *testing.T) {
	ollama := fakeOllamaShow(t, `"temperature                    1"`)
	err := runBenchExpectingError(t,
		"--agent", "openai", "--endpoint", ollama+"/v1/chat/completions",
		"--model", "m", "--mimir-url", "http://127.0.0.1:1", "--inject", "none")
	if !strings.Contains(err.Error(), "no num_ctx set") {
		t.Fatalf("error = %v, want an unset-num_ctx abort", err)
	}
}

// TestBenchAbortsOnTooSmallNumCtx covers an explicit but inadequate context.
func TestBenchAbortsOnTooSmallNumCtx(t *testing.T) {
	ollama := fakeOllamaShow(t, `"num_ctx                        4096"`)
	err := runBenchExpectingError(t,
		"--agent", "openai", "--endpoint", ollama+"/v1/chat/completions",
		"--model", "m", "--mimir-url", "http://127.0.0.1:1", "--inject", "none")
	if !strings.Contains(err.Error(), "below the 32768 floor") {
		t.Fatalf("error = %v, want a context-floor abort", err)
	}
}

// TestBenchAllowsStubUnderLocalOnly: the calibration stub dials nothing, so the
// policy must not stand in the way of calibrating a rubric.
func TestBenchAllowsStubUnderLocalOnly(t *testing.T) {
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{
		"bench", "run", "--scenario", writeScenario(t),
		"--agent", "stub", "--stub-profile", "cited",
		"--inject", "none", "--inject-namespace", "otel-demo",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("stub run under --local-only: %v", err)
	}
	if !strings.Contains(out.String(), "Argus Bench") {
		t.Fatalf("expected a rendered report, got:\n%s", out.String())
	}
}
