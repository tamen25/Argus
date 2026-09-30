package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recording returns canned responses like scripted, and keeps every request
// body so a test can assert what the model was actually told.
func recording(t *testing.T, bodies *[]string, responses ...string) *httptest.Server {
	t.Helper()
	step := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*bodies = append(*bodies, string(b))
		if step >= len(responses) {
			t.Errorf("unexpected extra model call #%d", step)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(responses[step]))
		step++
	}))
}

// multiToolCallResp is one assistant turn asking for n tool calls at once.
func multiToolCallResp(n int) string {
	calls := make([]string, n)
	for i := range calls {
		calls[i] = fmt.Sprintf(
			`{"id":"c%d","type":"function","function":{"name":"query_prometheus","arguments":"{\"query\":\"up\"}"}}`, i)
	}
	return `{"choices":[{"message":{"role":"assistant","tool_calls":[` + strings.Join(calls, ",") +
		`]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":10}}`
}

// failingTools errors on every call whose query is "bad".
type failingTools struct{ fakeTools }

func (f *failingTools) Call(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	if strings.Contains(string(args), "bad") {
		f.calls++
		return nil, errors.New("bad_data: parse error")
	}
	return f.fakeTools.Call(ctx, name, args)
}

var _ Tools = (*failingTools)(nil)

func TestBudgetBrief(t *testing.T) {
	cases := []struct {
		name    string
		budget  Budget
		want    []string
		wantNot string
	}{
		{"both caps", Budget{MaxToolCalls: 20, MaxTokens: 100000}, []string{"at most 20 tool calls", "100000 tokens"}, ""},
		{"tool calls only", Budget{MaxToolCalls: 5}, []string{"at most 5 tool calls"}, "tokens in total"},
		{"tokens only", Budget{MaxTokens: 5000}, []string{"at most 5000 tokens"}, "tool calls for"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := budgetBrief(tc.budget)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("brief %q does not state %q", got, w)
				}
			}
			if tc.wantNot != "" && strings.Contains(got, tc.wantNot) {
				t.Errorf("brief %q mentions a cap that is not set (%q)", got, tc.wantNot)
			}
			if !strings.Contains(got, "recorded as no answer") {
				t.Errorf("brief %q does not say what running out means", got)
			}
		})
	}
	// An uncapped run must not invent a limit.
	if got := budgetBrief(Budget{}); got != "" {
		t.Errorf("uncapped brief = %q, want empty", got)
	}
}

// The first live run spent its whole budget investigating and was cut off,
// because nothing ever told the model a budget existed.
func TestOpenAI_ModelIsToldItsBudget(t *testing.T) {
	var bodies []string
	srv := recording(t, &bodies, toolCallResp("c1", submitToolName, diagArgs, 50))
	defer srv.Close()

	_, err := newAgent(t, srv).Diagnose(context.Background(), Task{
		Scenario: "s", Brief: "an incident", Tools: &fakeTools{},
		Budget: Budget{MaxToolCalls: 20, MaxTokens: 100000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bodies[0], "at most 20 tool calls and 100000 tokens") {
		t.Errorf("first request does not state the budget:\n%s", bodies[0])
	}
}

func TestOpenAI_SubmitsOnTheFinalTurn(t *testing.T) {
	var bodies []string
	srv := recording(t, &bodies,
		toolCallResp("c1", "query_prometheus", `{"query":"up"}`, 10), // uses the only permitted call
		toolCallResp("c2", submitToolName, diagArgs, 10),             // the final turn
	)
	defer srv.Close()

	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{
		Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 1},
	})
	if err != nil {
		t.Fatalf("a diagnosis submitted on the final turn must be accepted, got %v", err)
	}
	if string(res.Raw) != diagArgs {
		t.Errorf("raw = %s", res.Raw)
	}
	if res.Usage.ToolCalls != 1 {
		t.Errorf("tool calls = %d, want 1", res.Usage.ToolCalls)
	}
	if strings.Contains(bodies[0], "budget is now spent") {
		t.Error("the spent notice was sent before the budget was spent")
	}
	if !strings.Contains(bodies[1], "budget is now spent") {
		t.Errorf("the model was not told its budget was spent before its final turn:\n%s", bodies[1])
	}
}

func TestOpenAI_CallsPastTheCapAreRefusedNotRun(t *testing.T) {
	var bodies []string
	srv := recording(t, &bodies,
		multiToolCallResp(3), // three calls in one turn against a cap of two
		toolCallResp("s1", submitToolName, diagArgs, 10),
	)
	defer srv.Close()

	tools := &fakeTools{}
	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{
		Scenario: "s", Tools: tools, Budget: Budget{MaxToolCalls: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tools.calls != 2 {
		t.Errorf("executed %d tool calls, want 2 (the cap)", tools.calls)
	}
	if res.Usage.ToolCalls != 2 || len(res.Calls) != 2 {
		t.Errorf("reported %d calls / %d logged, want 2 / 2: a refused call must not be counted",
			res.Usage.ToolCalls, len(res.Calls))
	}
	// The API requires an answer for every tool_call id; the third gets the refusal.
	if !strings.Contains(bodies[1], "this call was not executed") {
		t.Errorf("the call past the cap got no refusal:\n%s", bodies[1])
	}
}

func TestOpenAI_ToolLogRecordsQueriesAndErrors(t *testing.T) {
	long := strings.Repeat("x", 2*maxLoggedArguments)
	srv := scripted(t,
		toolCallResp("c1", "query_prometheus", `{"query":"up"}`, 10),
		toolCallResp("c2", "query_prometheus", `{"query":"bad `+long+`"}`, 10),
		toolCallResp("c3", submitToolName, diagArgs, 10),
	)
	defer srv.Close()

	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{
		Scenario: "s", Tools: &failingTools{}, Budget: Budget{MaxToolCalls: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.ToolCalls != 2 || res.Usage.ToolErrors != 1 {
		t.Fatalf("usage = %+v, want 2 calls, 1 error", res.Usage)
	}
	if len(res.Calls) != 2 {
		t.Fatalf("logged %d calls, want 2", len(res.Calls))
	}
	ok, failed := res.Calls[0], res.Calls[1]
	if ok.Tool != "query_prometheus" || ok.Arguments != `{"query":"up"}` || ok.Error != "" {
		t.Errorf("successful call logged as %+v", ok)
	}
	if ok.ResultBytes != len(`{"data":"series"}`) {
		t.Errorf("result bytes = %d, want the size of the answer", ok.ResultBytes)
	}
	if failed.Error != "bad_data: parse error" {
		t.Errorf("failed call error = %q", failed.Error)
	}
	// Arguments are the agent's own query, kept for the record but bounded: a
	// run report is not a transcript.
	if len(failed.Arguments) > maxLoggedArguments+len("…") {
		t.Errorf("logged arguments are %d bytes, want at most %d", len(failed.Arguments), maxLoggedArguments)
	}
}

// A backend error is "<request URL with the whole query>: <reason>". The log
// must keep the reason, which is the only part not already in Arguments.
func TestToolLogKeepsTheReasonOfALongError(t *testing.T) {
	long := "backend http://mimir/api/v1/query?query=" + strings.Repeat("x", 4*maxLoggedError) +
		": HTTP 400: parse error: unexpected end of input"
	got := truncateMiddle(long, maxLoggedError)
	if !strings.HasSuffix(got, "unexpected end of input") {
		t.Errorf("truncated error lost its reason: %q", got)
	}
	if !strings.HasPrefix(got, "backend http://mimir") {
		t.Errorf("truncated error lost which backend failed: %q", got)
	}
	if len(got) > maxLoggedError+len("…") {
		t.Errorf("truncated error is %d bytes, want about %d", len(got), maxLoggedError)
	}
	if short := "bad_data"; truncateMiddle(short, maxLoggedError) != short {
		t.Error("a short error must be kept whole")
	}
}

// A failed run must still say what the agent did with its budget.
func TestOpenAI_ExhaustedRunKeepsItsToolLog(t *testing.T) {
	srv := scripted(t,
		toolCallResp("c1", "query_prometheus", `{"query":"up"}`, 10),
		toolCallResp("c2", "query_prometheus", `{"query":"up"}`, 10),
	)
	defer srv.Close()
	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{
		Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 1},
	})
	if err != ErrBudgetExhausted {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
	if len(res.Calls) != 1 {
		t.Errorf("logged %d calls on an exhausted run, want 1", len(res.Calls))
	}
}

func TestAnthropic_ModelIsToldItsBudget(t *testing.T) {
	var bodies []string
	srv := recording(t, &bodies, antToolUseResp("t1", submitToolName, diagArgs, 5, 5))
	defer srv.Close()
	_, err := newAnt(t, srv).Diagnose(context.Background(), Task{
		Scenario: "s", Brief: "an incident", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 12},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bodies[0], "at most 12 tool calls") {
		t.Errorf("first request does not state the budget:\n%s", bodies[0])
	}
}

func TestAnthropic_SubmitsOnTheFinalTurn(t *testing.T) {
	var bodies []string
	srv := recording(t, &bodies,
		antToolUseResp("t1", "query_prometheus", `{"query":"up"}`, 5, 5),
		antToolUseResp("t2", submitToolName, diagArgs, 5, 5),
	)
	defer srv.Close()
	res, err := newAnt(t, srv).Diagnose(context.Background(), Task{
		Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 1},
	})
	if err != nil {
		t.Fatalf("a diagnosis submitted on the final turn must be accepted, got %v", err)
	}
	if res.Usage.ToolCalls != 1 || len(res.Calls) != 1 {
		t.Errorf("usage %+v, %d logged; want 1 call", res.Usage, len(res.Calls))
	}
	if !strings.Contains(bodies[1], "budget is now spent") {
		t.Errorf("the model was not told its budget was spent before its final turn:\n%s", bodies[1])
	}
}
