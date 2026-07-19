package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
	"github.com/tamen25/Argus/engine/internal/bench/scoring"
	"github.com/tamen25/Argus/engine/internal/mcp"
)

// fakeTools satisfies agent.Tools without a live backend.
type fakeTools struct{ calls int }

func (f *fakeTools) List() []mcp.Tool {
	return []mcp.Tool{{
		Name:        "query_prometheus",
		Description: "run promql",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
		ReadOnly:    true,
	}}
}

func (f *fakeTools) Call(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	f.calls++
	return json.RawMessage(`{"data":"series"}`), nil
}

// scripted returns a sequence of canned chat-completions responses.
func scripted(t *testing.T, responses ...string) *httptest.Server {
	t.Helper()
	step := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if step >= len(responses) {
			t.Errorf("unexpected extra chat call #%d", step)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(responses[step]))
		step++
	}))
}

func toolCallResp(id, name, args string, tokens int) string {
	return fmt.Sprintf(
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":%q,"type":"function","function":{"name":%q,"arguments":%s}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":%d}}`,
		id, name, strconv.Quote(args), tokens)
}

func contentResp(text string) string {
	return fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"total_tokens":30}}`, text)
}

const diagArgs = `{"root_cause_entities":[{"kind":"Deployment","namespace":"otel-demo","name":"checkout"}],"category":"cardinality-explosion"}`

func newAgent(t *testing.T, srv *httptest.Server) *OpenAIAgent {
	t.Helper()
	return NewOpenAI(OpenAIConfig{Name: "test-model", Endpoint: srv.URL, Model: "m", HTTP: srv.Client()})
}

func TestOpenAI_ToolThenSubmit(t *testing.T) {
	srv := scripted(t,
		toolCallResp("c1", "query_prometheus", `{"query":"up"}`, 100),
		toolCallResp("c2", submitToolName, diagArgs, 120),
	)
	defer srv.Close()

	tools := &fakeTools{}
	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{
		Scenario: "cardinality-explosion-checkout",
		Brief:    "checkout latency + active series spike",
		Tools:    tools,
		Budget:   Budget{MaxToolCalls: 5, MaxTokens: 10000},
	})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if tools.calls != 1 {
		t.Errorf("tool calls executed = %d, want 1", tools.calls)
	}
	if res.Usage.ToolCalls != 1 {
		t.Errorf("usage tool calls = %d, want 1 (submit not counted)", res.Usage.ToolCalls)
	}
	if res.Usage.Tokens != 220 {
		t.Errorf("usage tokens = %d, want 220 (summed)", res.Usage.Tokens)
	}

	// End-to-end: raw submit args normalize + score to a perfect match.
	d, err := bench.JSONNormalizer{}.Normalize(context.Background(), res.Raw, "cardinality-explosion-checkout")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if d.Scenario != "cardinality-explosion-checkout" {
		t.Errorf("scenario not forced: %q", d.Scenario)
	}
	gt := bench.GroundTruth{
		RootCauseEntities: []bench.Entity{{Kind: "Deployment", Namespace: "otel-demo", Name: "checkout"}},
		Category:          "cardinality-explosion",
	}
	r := scoring.Score(gt, bench.ScoringSpec{PartialCredit: true}, d)
	if r.EntityScore != 1 || !r.CategoryMatch {
		t.Errorf("score = %+v, want perfect", r)
	}
}

func TestOpenAI_SubmitFirstTurn(t *testing.T) {
	srv := scripted(t, toolCallResp("c1", submitToolName, diagArgs, 50))
	defer srv.Close()
	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.ToolCalls != 0 {
		t.Errorf("tool calls = %d, want 0", res.Usage.ToolCalls)
	}
}

func TestOpenAI_ToolCallBudget(t *testing.T) {
	// Every turn asks for a read-only tool, never submitting: budget must stop it.
	srv := scripted(t,
		toolCallResp("c1", "query_prometheus", `{"query":"up"}`, 10),
		toolCallResp("c2", "query_prometheus", `{"query":"up"}`, 10),
		toolCallResp("c3", "query_prometheus", `{"query":"up"}`, 10),
	)
	defer srv.Close()
	_, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 1}})
	if err != ErrBudgetExhausted {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
}

func TestOpenAI_TokenBudget(t *testing.T) {
	srv := scripted(t, toolCallResp("c1", "query_prometheus", `{"query":"up"}`, 5000))
	defer srv.Close()
	_, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxTokens: 1000}})
	if err != ErrBudgetExhausted {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
}

func TestOpenAI_FinishWithoutSubmit(t *testing.T) {
	srv := scripted(t, contentResp("I think it's checkout but I'm not sure."))
	defer srv.Close()
	_, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}})
	if err == nil {
		t.Fatal("expected error when agent never submits")
	}
}

func TestOpenAI_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()
	_, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}})
	if err == nil {
		t.Fatal("expected HTTP error")
	}
}

func TestOpenAI_Name(t *testing.T) {
	if n := NewOpenAI(OpenAIConfig{Model: "gpt-x"}).Name(); n != "gpt-x" {
		t.Errorf("Name() = %q, want model fallback gpt-x", n)
	}
}
