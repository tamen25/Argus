package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 1}})
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
	// The reported count must be what was actually executed, never the attempt
	// that tripped the cap. A live run capped at 12 reported 13 before this was
	// fixed, which would overstate every exhausted row on a leaderboard.
	if res.Usage.ToolCalls != 1 {
		t.Fatalf("tool calls = %d, want 1 (the cap) — a call that never ran must not be counted",
			res.Usage.ToolCalls)
	}
}

func TestOpenAI_TokenBudget(t *testing.T) {
	srv := scripted(t, toolCallResp("c1", "query_prometheus", `{"query":"up"}`, 5000))
	defer srv.Close()
	_, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxTokens: 1000}})
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
}

// Prose twice: reminded once, then failed. scripted() fails the test on a
// third call, so the reminder is given exactly once.
func TestOpenAI_FinishWithoutSubmit(t *testing.T) {
	srv := scripted(t, contentResp("I think it's checkout but I'm not sure."), contentResp("Checkout."))
	defer srv.Close()
	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}})
	if err == nil {
		t.Fatal("expected error when agent never submits")
	}
	if res.Usage.Nudges != 1 {
		t.Errorf("nudges = %d, want 1", res.Usage.Nudges)
	}
}

// Prose, then a diagnosis after the reminder: the run is answered, and the
// reminder reached the model as the last message it saw.
func TestOpenAI_NudgedThenSubmits(t *testing.T) {
	var last string
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req oaRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		last = req.Messages[len(req.Messages)-1].Content
		if step == 0 {
			_, _ = w.Write([]byte(contentResp("It is checkout.")))
		} else {
			_, _ = w.Write([]byte(toolCallResp("c1", submitToolName, diagArgs, 50)))
		}
		step++
	}))
	defer srv.Close()
	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if string(res.Raw) != diagArgs || res.Usage.Nudges != 1 {
		t.Errorf("raw = %s, nudges = %d; want the diagnosis after one reminder", res.Raw, res.Usage.Nudges)
	}
	if last != noToolNotice {
		t.Errorf("last message before the answer = %q, want the reminder", last)
	}
}

// A reasoning model's visible reply can be empty. Echoing it back as an
// assistant message with neither content nor tool calls made Ollama answer
// HTTP 400 and ended the first gpt-oss run; the fake rejects it the same way.
func TestOpenAI_NudgeAfterEmptyReply(t *testing.T) {
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req oaRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		for _, m := range req.Messages {
			if m.Role == "assistant" && m.Content == "" && len(m.ToolCalls) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"invalid message content type: <nil>"}}`))
				return
			}
		}
		if step == 0 {
			_, _ = w.Write([]byte(contentResp("")))
		} else {
			_, _ = w.Write([]byte(toolCallResp("c1", submitToolName, diagArgs, 50)))
		}
		step++
	}))
	defer srv.Close()
	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if string(res.Raw) != diagArgs || res.Usage.Nudges != 1 {
		t.Errorf("raw = %s, nudges = %d; want the diagnosis after one reminder", res.Raw, res.Usage.Nudges)
	}
}

// Over the token cap, prose is not answered with a reminder.
func TestOpenAI_NoNudgePastTokenCap(t *testing.T) {
	srv := scripted(t, `{"choices":[{"message":{"role":"assistant","content":"checkout"},"finish_reason":"stop"}],"usage":{"total_tokens":2000}}`)
	defer srv.Close()
	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxTokens: 1000}})
	if err == nil || res.Usage.Nudges != 0 {
		t.Fatalf("err = %v, nudges = %d; want a failed run and no reminder", err, res.Usage.Nudges)
	}
}

// A turn cut off at the output cap ends the run at once, saying why, and every
// request carries the cap. Without one, the first flagship session lost 30
// minutes to a single turn of a model looping in its reasoning.
func TestOpenAI_TurnCap(t *testing.T) {
	var sent []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req oaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		sent = append(sent, req.MaxTokens)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"length"}],"usage":{"total_tokens":8300}}`))
	}))
	defer srv.Close()
	ag := NewOpenAI(OpenAIConfig{Name: "test-model", Endpoint: srv.URL, Model: "m", HTTP: srv.Client(), MaxTurnTokens: 8192})

	res, err := ag.Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}})
	if err == nil || !strings.Contains(err.Error(), "per-turn output cap (8192 tokens)") {
		t.Fatalf("err = %v, want the per-turn cap named", err)
	}
	if len(sent) != 1 || sent[0] != 8192 {
		t.Errorf("max_tokens sent = %v, want one request carrying 8192", sent)
	}
	if res.Usage.Tokens != 8300 {
		t.Errorf("tokens = %d, want the cut-off turn counted (8300)", res.Usage.Tokens)
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
