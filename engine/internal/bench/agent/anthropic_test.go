package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// antToolUseResp builds a Messages response whose assistant turn requests one
// tool_use.
func antToolUseResp(id, name, input string, inTok, outTok int) string {
	return fmt.Sprintf(
		`{"content":[{"type":"text","text":"thinking"},{"type":"tool_use","id":%q,"name":%q,"input":%s}],"stop_reason":"tool_use","usage":{"input_tokens":%d,"output_tokens":%d}}`,
		id, name, input, inTok, outTok)
}

func antTextResp(text string) string {
	return fmt.Sprintf(`{"content":[{"type":"text","text":%q}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":10}}`, text)
}

func newAnt(t *testing.T, srv *httptest.Server) *AnthropicAgent {
	t.Helper()
	return NewAnthropic(AnthropicConfig{Name: "claude-test", Endpoint: srv.URL, Model: "m", HTTP: srv.Client()})
}

func TestAnthropic_ToolThenSubmit(t *testing.T) {
	srv := scripted(t,
		antToolUseResp("t1", "query_prometheus", `{"query":"up"}`, 40, 20),
		antToolUseResp("t2", submitToolName, diagArgs, 30, 15),
	)
	defer srv.Close()

	tools := &fakeTools{}
	res, err := newAnt(t, srv).Diagnose(context.Background(), Task{
		Scenario: "cardinality-explosion-checkout",
		Brief:    "active series spike",
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
		t.Errorf("usage tool calls = %d, want 1", res.Usage.ToolCalls)
	}
	if res.Usage.Tokens != 40+20+30+15 {
		t.Errorf("tokens = %d, want %d", res.Usage.Tokens, 40+20+30+15)
	}
	if string(res.Raw) != diagArgs {
		t.Errorf("raw = %s", res.Raw)
	}
}

func TestAnthropic_ToolCallBudget(t *testing.T) {
	srv := scripted(t,
		antToolUseResp("t1", "query_prometheus", `{"query":"up"}`, 5, 5),
		antToolUseResp("t2", "query_prometheus", `{"query":"up"}`, 5, 5),
	)
	defer srv.Close()
	_, err := newAnt(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 1}})
	if err != ErrBudgetExhausted {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
}

func TestAnthropic_TokenBudget(t *testing.T) {
	srv := scripted(t, antToolUseResp("t1", "query_prometheus", `{"query":"up"}`, 800, 800))
	defer srv.Close()
	_, err := newAnt(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxTokens: 1000}})
	if err != ErrBudgetExhausted {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
}

func TestAnthropic_FinishWithoutSubmit(t *testing.T) {
	srv := scripted(t, antTextResp("probably checkout"))
	defer srv.Close()
	_, err := newAnt(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}})
	if err == nil {
		t.Fatal("expected error when agent never submits")
	}
}

func TestAnthropic_HeadersAndDefaults(t *testing.T) {
	var gotVersion, gotKey, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotVersion = r.Header.Get("anthropic-version")
		gotKey = r.Header.Get("x-api-key")
		gotCT = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(antToolUseResp("t1", submitToolName, diagArgs, 5, 5)))
	}))
	defer srv.Close()

	a := NewAnthropic(AnthropicConfig{Endpoint: srv.URL, Model: "m", APIKey: "sk-x", HTTP: srv.Client()})
	if _, err := a.Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}}); err != nil {
		t.Fatal(err)
	}
	if gotVersion != defaultAnthropicVersion {
		t.Errorf("anthropic-version = %q, want %q", gotVersion, defaultAnthropicVersion)
	}
	if gotKey != "sk-x" {
		t.Errorf("x-api-key = %q", gotKey)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}
}

func TestAnthropic_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error"}`))
	}))
	defer srv.Close()
	_, err := newAnt(t, srv).Diagnose(context.Background(), Task{Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 3}})
	if err == nil {
		t.Fatal("expected HTTP error")
	}
}
