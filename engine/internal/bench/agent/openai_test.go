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

// diagArgs is a submission reviewSubmission accepts: well-formed evidence
// included, and no category list in the tests' tasks to check against.
const diagArgs = `{"root_cause_entities":[{"kind":"Deployment","namespace":"otel-demo","name":"checkout"}],"category":"cardinality-explosion",` +
	`"evidence":[{"signal":"metrics","query":"count({job=\"checkout\"})","observation":"series up 40x"}]}`

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
	// The last reply is kept in the error: it tells prose apart from a tool
	// call the endpoint could not parse and returned as text.
	if !strings.Contains(err.Error(), `last reply: "Checkout."`) {
		t.Errorf("err = %v, want the last reply quoted", err)
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

// statusThen answers the first n calls with status, then serves responses.
func statusThen(t *testing.T, status, n int, responses ...string) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls <= n {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"error parsing tool call"}}`))
			return
		}
		_, _ = w.Write([]byte(responses[calls-n-1]))
	})), &calls
}

// A 5xx is retried, at most twice per run, and the retry is not a step.
func TestOpenAI_Retries5xx(t *testing.T) {
	srv, calls := statusThen(t, http.StatusInternalServerError, 1, toolCallResp("c1", submitToolName, diagArgs, 50))
	defer srv.Close()
	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if *calls != 2 || res.Usage.Retries != 1 || res.Usage.Steps != 1 {
		t.Errorf("calls = %d, retries = %d, steps = %d; want 2, 1, 1", *calls, res.Usage.Retries, res.Usage.Steps)
	}

	srv2, calls2 := statusThen(t, http.StatusInternalServerError, 5)
	defer srv2.Close()
	res, err = newAgent(t, srv2).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") || *calls2 != 3 || res.Usage.Retries != 2 {
		t.Errorf("err = %v, calls = %d, retries = %d; want the 500 after 3 calls and 2 retries", err, *calls2, res.Usage.Retries)
	}
}

// The second flagship pass's citations: the model's own shape, not the schema's.
const badEvidenceArgs = `{"root_cause_entities":[{"kind":"Deployment","namespace":"otel-demo","name":"checkout"}],` +
	`"category":"cardinality-explosion","evidence":[{"type":"alert","id":"HighSpanErrorRatio"}]}`

// A submission with no well-formed citation is answered with what is wrong,
// and the corrected one ends the run. The rejection is not a tool call.
func TestOpenAI_SubmissionIsCheckedAndCanBeCorrected(t *testing.T) {
	var bodies []string
	srv := recording(t, &bodies,
		toolCallResp("c1", submitToolName, badEvidenceArgs, 50),
		toolCallResp("c2", submitToolName, diagArgs, 50))
	defer srv.Close()
	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if string(res.Raw) != diagArgs || res.Usage.SubmitRejections != 1 || res.Usage.ToolCalls != 0 {
		t.Errorf("raw = %s, rejections = %d, tool calls = %d; want the corrected diagnosis, 1, 0",
			res.Raw, res.Usage.SubmitRejections, res.Usage.ToolCalls)
	}
	if len(bodies) != 2 || !strings.Contains(bodies[1], "submit_diagnosis rejected: no evidence item is in the required shape") {
		t.Errorf("the second request should carry the rejection:\n%s", bodies[len(bodies)-1])
	}
}

// At most two rejections: the third submission is accepted as it is, so a run
// cannot loop and is scored rather than dropped.
func TestOpenAI_SubmissionRejectedAtMostTwice(t *testing.T) {
	srv := scripted(t,
		toolCallResp("c1", submitToolName, badEvidenceArgs, 50),
		toolCallResp("c2", submitToolName, badEvidenceArgs, 50),
		toolCallResp("c3", submitToolName, badEvidenceArgs, 50))
	defer srv.Close()
	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if string(res.Raw) != badEvidenceArgs || res.Usage.SubmitRejections != 2 {
		t.Errorf("raw = %s, rejections = %d; want the third submission accepted after 2", res.Raw, res.Usage.SubmitRejections)
	}
}

// Category outside the offered list, and unparseable arguments, are rejected
// too; a right-or-wrong diagnosis never is.
func TestSubmissionProblem(t *testing.T) {
	cats := []bench.Category{{Name: "oomkill"}}
	if p := submissionProblem(json.RawMessage(diagArgs), cats); !strings.Contains(p, `category "cardinality-explosion" is not one of the offered`) {
		t.Errorf("unoffered category: %q", p)
	}
	if p := submissionProblem(json.RawMessage(`{"category":`), nil); p == "" {
		t.Error("unparseable arguments accepted")
	}
	if p := submissionProblem(json.RawMessage(diagArgs), nil); p != "" {
		t.Errorf("a well-formed submission was rejected: %q", p)
	}
}

// A 4xx is the request's fault, not a transient one: never retried.
func TestOpenAI_NoRetryOn4xx(t *testing.T) {
	srv, calls := statusThen(t, http.StatusBadRequest, 5)
	defer srv.Close()
	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}})
	if err == nil || *calls != 1 || res.Usage.Retries != 0 {
		t.Errorf("err = %v, calls = %d, retries = %d; want one call and no retry", err, *calls, res.Usage.Retries)
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
