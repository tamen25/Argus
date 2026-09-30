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

	"github.com/tamen25/Argus/engine/internal/bench"
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
	if !errors.Is(err, ErrBudgetExhausted) {
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

var testCategories = []bench.Category{
	{Name: "deploy-regression", Description: "A rollout misbehaves."},
	{Name: "oomkill", Description: "A container is killed for exceeding its memory limit."},
}

// The category is scored by exact match. The first real run answered
// "PerformanceDegradation" because nothing had shown it the vocabulary.
func TestSubmitSchema_OffersTheCategoryList(t *testing.T) {
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type        string   `json:"type"`
			Enum        []string `json:"enum"`
			Description string   `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(submitSchema(testCategories), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	cat := schema.Properties["category"]
	if strings.Join(cat.Enum, ",") != "deploy-regression,oomkill" {
		t.Errorf("category enum = %v, want exactly the listed names in order", cat.Enum)
	}
	for _, c := range testCategories {
		if !strings.Contains(cat.Description, c.Name+" — "+c.Description) {
			t.Errorf("category description does not explain %q: %s", c.Name, cat.Description)
		}
	}
	// Everything else about the schema is untouched.
	if len(schema.Properties) != 5 || strings.Join(schema.Required, ",") != "root_cause_entities,category,evidence" {
		t.Errorf("schema changed beyond the category: %d properties, required %v", len(schema.Properties), schema.Required)
	}
	if len(schema.Properties["evidence"].Type) == 0 {
		t.Error("evidence property lost its type")
	}

	// Without a list the category stays free text: no enum is invented.
	if err := json.Unmarshal(submitSchema(nil), &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Properties["category"].Enum) != 0 {
		t.Errorf("no list given, yet the schema has an enum: %v", schema.Properties["category"].Enum)
	}
}

func TestAgents_SendTheCategoryListToTheModel(t *testing.T) {
	task := Task{Scenario: "s", Brief: "an incident", Tools: &fakeTools{}, Categories: testCategories}

	var oa []string
	srv := recording(t, &oa, toolCallResp("c1", submitToolName, diagArgs, 50))
	defer srv.Close()
	if _, err := newAgent(t, srv).Diagnose(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	var ant []string
	asrv := recording(t, &ant, antToolUseResp("t1", submitToolName, diagArgs, 5, 5))
	defer asrv.Close()
	if _, err := newAnt(t, asrv).Diagnose(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{"openai": oa[0], "anthropic": ant[0]} {
		// In the tool schema (as an enum) and in the brief (as prose).
		if !strings.Contains(body, `"enum":["deploy-regression","oomkill"]`) {
			t.Errorf("%s: submit_diagnosis has no category enum:\n%s", name, body)
		}
		if !strings.Contains(body, "give exactly one of these names as the category") {
			t.Errorf("%s: the brief does not list the categories:\n%s", name, body)
		}
	}
}

func TestCategoryBrief(t *testing.T) {
	got := categoryBrief(testCategories)
	if !strings.Contains(got, "\n- deploy-regression: A rollout misbehaves.") ||
		!strings.Contains(got, "\n- oomkill: A container is killed") {
		t.Errorf("brief = %q", got)
	}
	if categoryBrief(nil) != "" {
		t.Error("no list must add nothing to the brief")
	}
}

// The fourth real run was cut off on its token cap with no warning: each turn
// re-sends the whole conversation, so the count grows faster than an agent can
// track. When the tokens left would not cover another turn, the agent is told
// and gets a final turn.
func TestOpenAI_WarnsWhenTokensRunLowAndAcceptsTheFinalSubmit(t *testing.T) {
	var bodies []string
	srv := recording(t, &bodies,
		toolCallResp("c1", "query_prometheus", `{"query":"up"}`, 3000), // 3000 used, 7000 left: plenty
		toolCallResp("c2", "query_prometheus", `{"query":"up"}`, 4000), // 7000 used, 3000 left < 4000+2000
		toolCallResp("c3", submitToolName, diagArgs, 5000),             // the final turn crosses the cap
	)
	defer srv.Close()

	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{
		Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 20, MaxTokens: 10000},
	})
	if err != nil {
		t.Fatalf("a diagnosis submitted on the final turn must be accepted, got %v", err)
	}
	if res.Usage.Tokens != 12000 {
		t.Errorf("tokens = %d, want the true total of 12000 (reported, not hidden)", res.Usage.Tokens)
	}
	if strings.Contains(bodies[1], "token budget is nearly spent") {
		t.Error("warned while most of the budget was left")
	}
	if !strings.Contains(bodies[2], "token budget is nearly spent (7000 of 10000 used)") {
		t.Errorf("the model was not warned before its final turn:\n%s", bodies[2])
	}
}

func TestOpenAI_AToolCallAfterTheTokenNoticeEndsTheRun(t *testing.T) {
	srv := scripted(t,
		toolCallResp("c1", "query_prometheus", `{"query":"up"}`, 7000),
		toolCallResp("c2", "query_prometheus", `{"query":"up"}`, 1000),
	)
	defer srv.Close()
	tools := &fakeTools{}
	res, err := newAgent(t, srv).Diagnose(context.Background(), Task{
		Scenario: "s", Tools: tools, Budget: Budget{MaxToolCalls: 20, MaxTokens: 10000},
	})
	var be *BudgetError
	if !errors.As(err, &be) || be.Cap != CapTokens {
		t.Fatalf("err = %v, want a BudgetError on the token cap", err)
	}
	if tools.calls != 1 || res.Usage.ToolCalls != 1 {
		t.Errorf("executed %d / reported %d calls, want 1: the call after the notice must not run",
			tools.calls, res.Usage.ToolCalls)
	}
}

func TestBudgetError_NamesTheCap(t *testing.T) {
	srv := scripted(t,
		toolCallResp("c1", "query_prometheus", `{"query":"up"}`, 10),
		toolCallResp("c2", "query_prometheus", `{"query":"up"}`, 10),
	)
	defer srv.Close()
	_, err := newAgent(t, srv).Diagnose(context.Background(), Task{
		Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxToolCalls: 1},
	})
	var be *BudgetError
	if !errors.As(err, &be) || be.Cap != CapToolCalls || be.Used != 1 || be.Limit != 1 {
		t.Fatalf("err = %#v, want the tool-call cap (1 of 1)", err)
	}
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Error("a BudgetError must still match ErrBudgetExhausted")
	}
	if !strings.Contains(err.Error(), "tool calls cap (1 of 1)") {
		t.Errorf("message = %q", err.Error())
	}
}

func TestAnthropic_WarnsWhenTokensRunLow(t *testing.T) {
	var bodies []string
	srv := recording(t, &bodies,
		antToolUseResp("t1", "query_prometheus", `{"query":"up"}`, 6000, 1000),
		antToolUseResp("t2", submitToolName, diagArgs, 4000, 500),
	)
	defer srv.Close()
	if _, err := newAnt(t, srv).Diagnose(context.Background(), Task{
		Scenario: "s", Tools: &fakeTools{}, Budget: Budget{MaxTokens: 10000},
	}); err != nil {
		t.Fatalf("final submit refused: %v", err)
	}
	if !strings.Contains(bodies[1], "token budget is nearly spent (7000 of 10000 used)") {
		t.Errorf("no token notice before the final turn:\n%s", bodies[1])
	}
}
