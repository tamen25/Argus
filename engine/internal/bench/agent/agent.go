// Package agent holds the bench subject adapters — the AI SRE agents whose
// diagnosis accuracy the benchmark measures. These adapters ARE the LLM under
// test (the edge), configured separately from the product LLM client
// (bench.agents[], not remediate/llm) so the two are never conflated. The
// deterministic scorer (bench/scoring) never imports this package.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tamen25/Argus/engine/internal/bench"
	"github.com/tamen25/Argus/engine/internal/mcp"
)

// DefaultAgentTimeout caps a single model call when an adapter is built without
// its own HTTP client. It is generous because a reasoning model can spend
// minutes on one turn, and a timeout that fires mid-investigation is recorded as
// an agent failure when it is really a limit of the endpoint. Tune with
// --agent-timeout.
const DefaultAgentTimeout = 10 * time.Minute

// ErrBudgetExhausted is returned when a run hits its tool-call or token cap
// before producing a diagnosis. The orchestrator records it as a no-diagnosis
// run, not a crash.
var ErrBudgetExhausted = errors.New("agent: budget exhausted before diagnosis")

// Budget caps, as named in a BudgetError.
const (
	CapToolCalls = "tool calls"
	CapTokens    = "tokens"
)

// BudgetError says which cap ended a run and by how much. It wraps
// ErrBudgetExhausted, so errors.Is still recognizes it; a report uses Cap to
// tell a run that ran out of tool calls from one that ran out of tokens.
type BudgetError struct {
	Cap   string
	Used  int
	Limit int
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("%s: %s cap (%d of %d)", ErrBudgetExhausted, e.Cap, e.Used, e.Limit)
}

// Unwrap lets errors.Is(err, ErrBudgetExhausted) match.
func (e *BudgetError) Unwrap() error { return ErrBudgetExhausted }

// Tools is the read-only tool surface an agent may call. *mcp.Registry
// satisfies it, so the same surface the MCP server exposes is what agents use —
// a fair, identical comparison across agents (master plan §3.2).
type Tools interface {
	List() []mcp.Tool
	Call(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error)
}

// Budget caps one diagnosis run. Zero means unlimited for that dimension; the
// orchestrator always sets caps (run-matrix economics, architecture rule 6).
type Budget struct {
	MaxToolCalls int
	MaxTokens    int
}

// Task is one diagnosis assignment handed to an agent.
type Task struct {
	// Scenario is the authoritative scenario name, injected into the resulting
	// diagnosis so the agent cannot mislabel its own answer.
	Scenario string
	// Brief is the incident brief / alert text the agent starts from.
	Brief string
	// Tools is the read-only tool access for this run.
	Tools Tools
	// Budget caps tool calls and tokens for this run.
	Budget Budget
	// Categories is the closed list of fault categories the agent chooses from,
	// in the order it is shown. Empty means no list was offered and the category
	// is free text.
	Categories []bench.Category
}

// Usage records what a run consumed, for the honest run record (rules 6/7).
type Usage struct {
	ToolCalls int `json:"tool_calls"`
	// ToolErrors is how many of those calls returned an error to the agent — a
	// rejected query, or a backend that did not answer. A run full of them says
	// more about the queries or the tool surface than about the diagnosis.
	ToolErrors int `json:"tool_errors"`
	Tokens     int `json:"tokens"`
	Steps      int `json:"steps"`
}

// ToolCall records one tool call an agent made: what it asked, and whether it
// got an answer. The answer itself is not kept — it is raw telemetry, and only
// its size is recorded — so a run report shows how an agent investigated
// without becoming a copy of the environment's data.
type ToolCall struct {
	Tool      string `json:"tool"`
	Arguments string `json:"arguments,omitempty"`
	// Error is the error returned to the agent, if the call failed.
	Error       string `json:"error,omitempty"`
	ResultBytes int    `json:"result_bytes"`
}

// Result is an agent's raw answer plus usage. Raw is fed to a bench.Normalizer
// (JSON for API agents; native output for shell agents), never to the scorer
// directly.
type Result struct {
	Raw   json.RawMessage
	Usage Usage
	// Calls is the log of executed tool calls, in order. Empty for agents whose
	// tool use Argus cannot see (the shell adapter).
	Calls []ToolCall
}

// Agent runs one diagnosis attempt against a scenario.
type Agent interface {
	// Name identifies the agent in the run record (e.g. model id).
	Name() string
	// Diagnose runs the agent loop and returns its raw output plus usage. A
	// budget overrun returns ErrBudgetExhausted with the partial Usage.
	Diagnose(ctx context.Context, task Task) (Result, error)
}

// submitToolName is the synthetic terminal tool every API agent is given: the
// agent calls it to emit its structured diagnosis, so structured output falls
// out of the same function-calling mechanism as the read-only tools.
const submitToolName = "submit_diagnosis"

// submitToolSchema is the JSON-Schema for submit_diagnosis arguments. It mirrors
// the scored fields of bench.Diagnosis (scenario is injected by us, not the
// agent, so it is intentionally absent here).
const submitToolSchema = `{"type":"object","required":["root_cause_entities","category","evidence"],` +
	`"properties":{` +
	`"root_cause_entities":{"type":"array","items":{"type":"object","required":["kind","name"],` +
	`"properties":{"kind":{"type":"string"},"namespace":{"type":"string"},"name":{"type":"string"}}}},` +
	`"category":{"type":"string"},` +
	`"evidence":{"type":"array","description":"Telemetry supporting the conclusion. Cite what you actually queried.",` +
	`"items":{"type":"object","required":["signal","observation"],` +
	`"properties":{"signal":{"type":"string","enum":["metrics","logs","traces","alerts","topology"]},` +
	`"query":{"type":"string"},"observation":{"type":"string"}}}},` +
	`"summary":{"type":"string"},` +
	`"confidence":{"type":"number"}}}`

// submitSchema returns the submit_diagnosis schema for a run. With a category
// list, `category` becomes an enum of exactly those names and its description
// spells each one out, so the vocabulary the scorer matches against is part of
// the tool the agent answers with. Without one it stays free text.
func submitSchema(categories []bench.Category) json.RawMessage {
	if len(categories) == 0 {
		return json.RawMessage(submitToolSchema)
	}
	names := make([]string, len(categories))
	var desc strings.Builder
	desc.WriteString("The kind of fault. Choose exactly one:")
	for i, c := range categories {
		names[i] = c.Name
		fmt.Fprintf(&desc, " %s — %s", c.Name, c.Description)
	}

	// Decode into generic maps, change one property, encode again. The constant
	// is valid JSON by construction and encoding/json writes map keys in sorted
	// order, so the result is deterministic.
	var schema map[string]any
	if err := json.Unmarshal([]byte(submitToolSchema), &schema); err != nil {
		panic("agent: submitToolSchema is not valid JSON: " + err.Error())
	}
	schema["properties"].(map[string]any)["category"] = map[string]any{
		"type":        "string",
		"enum":        names,
		"description": desc.String(),
	}
	out, err := json.Marshal(schema)
	if err != nil {
		panic("agent: encoding submit schema: " + err.Error())
	}
	return out
}

// categoryBrief lists the fault categories in the brief itself, so an agent
// that never sees the tool schema (the shell adapter) is offered the same
// vocabulary as one that does. Empty when no list was given.
func categoryBrief(categories []bench.Category) string {
	if len(categories) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nFault categories — give exactly one of these names as the category:")
	for _, c := range categories {
		fmt.Fprintf(&b, "\n- %s: %s", c.Name, c.Description)
	}
	return b.String()
}

const systemPrompt = "You are an SRE incident-diagnosis agent. Investigate the incident using the " +
	"read-only observability tools (metrics, logs, traces, alerts, topology). Do not guess — use the " +
	"tools to gather evidence. Metric, label and workload names differ between environments: discover " +
	"them with the tools rather than assuming them. Identify the root-cause Kubernetes entities and the " +
	"fault category. " +
	"Naming the busiest or most obvious service without evidence is scored as a wrong answer, and so " +
	"is listing many services hoping one is right. " +
	"When you are confident, call " + submitToolName + " with the root-cause entities, the fault " +
	"category, and the specific telemetry you observed as evidence."

// budgetBrief tells the agent what it may spend. A cap the subject cannot see
// measures whether it happens to stop early, not whether it can diagnose within
// a budget: the first live run spent all 20 tool calls investigating and was
// cut off without ever being told there was a limit. Empty when uncapped.
func budgetBrief(b Budget) string {
	var limit string
	switch {
	case b.MaxToolCalls > 0 && b.MaxTokens > 0:
		limit = fmt.Sprintf("at most %d tool calls and %d tokens in total", b.MaxToolCalls, b.MaxTokens)
	case b.MaxToolCalls > 0:
		limit = fmt.Sprintf("at most %d tool calls", b.MaxToolCalls)
	case b.MaxTokens > 0:
		limit = fmt.Sprintf("at most %d tokens in total", b.MaxTokens)
	default:
		return ""
	}
	return "\n\nBudget: " + limit + " for this investigation. " + submitToolName +
		" does not count as a tool call. A run that ends without " + submitToolName +
		" is recorded as no answer, so submit the diagnosis your evidence best supports" +
		" before the budget runs out."
}

// budgetSpentNotice is sent once, when the last permitted tool call has been
// used. The agent then gets exactly one more turn, in which only
// submit_diagnosis is accepted.
const budgetSpentNotice = "The tool-call budget is now spent: no further tool calls will be executed. " +
	"Call " + submitToolName + " now with the diagnosis your evidence best supports."

// tokenLowNotice is sent once, when the tokens left would not cover another
// turn like the last one. Every turn re-sends the whole conversation, so the
// count grows faster than an agent can track: the fourth real run was cut off at
// 109404 of 100000 tokens with three tool calls to spare and no warning.
const tokenLowNotice = "The token budget is nearly spent (%d of %d used): another round of tool calls would " +
	"exceed it. Call " + submitToolName + " now with the diagnosis your evidence best supports; a tool call " +
	"now ends the run without an answer."

// budgetRefusal is what a tool call made past the cap receives instead of a
// result. It is not executed and not counted.
const budgetRefusal = `{"error":"tool-call budget spent: this call was not executed"}`

// Limits on what a run report keeps of each tool call.
const (
	maxLoggedArguments = 512
	maxLoggedError     = 256
)

// truncateMiddle shortens s to about n bytes by cutting out its middle. A
// backend error starts with the request URL, which repeats the query, and ends
// with the reason; cutting the tail would keep the part already recorded and
// drop the part that explains the failure.
func truncateMiddle(s string, n int) string {
	if len(s) <= n {
		return s
	}
	head := n / 3
	return s[:head] + "…" + s[len(s)-(n-head):]
}

// session tracks what one diagnosis run has consumed and enforces the
// tool-call cap. Both API adapters drive it, so the budget means the same thing
// whichever model is on the other end.
type session struct {
	budget Budget
	usage  Usage
	calls  []ToolCall
	// warned is set once a budget notice has been sent. A tool call after that
	// ends the run; only submit_diagnosis is accepted.
	warned bool
	// lastTurn is the token count of the most recent model turn — the best
	// estimate of what the next one will cost.
	lastTurn int
}

// spent reports whether the tool-call cap has been reached.
func (s *session) spent() bool {
	return s.budget.MaxToolCalls > 0 && s.usage.ToolCalls >= s.budget.MaxToolCalls
}

// addTokens records one model turn.
func (s *session) addTokens(n int) {
	s.usage.Tokens += n
	s.lastTurn = n
}

// overTokens reports whether the token cap has been exceeded.
func (s *session) overTokens() bool {
	return s.budget.MaxTokens > 0 && s.usage.Tokens > s.budget.MaxTokens
}

// tokensLow reports whether the tokens left would not cover another turn like
// the last one, with half again for the tool results it adds.
func (s *session) tokensLow() bool {
	return s.budget.MaxTokens > 0 && s.budget.MaxTokens-s.usage.Tokens < s.lastTurn+s.lastTurn/2
}

// notice returns the budget notice due after a turn, or "" when none is. It
// is sent at most once; sending it starts the agent's final turn.
func (s *session) notice() string {
	if s.warned {
		return ""
	}
	switch {
	case s.spent():
		s.warned = true
		return budgetSpentNotice
	case s.tokensLow():
		s.warned = true
		return fmt.Sprintf(tokenLowNotice, s.usage.Tokens, s.budget.MaxTokens)
	}
	return ""
}

// exhausted is the error for a run the budget ended, naming the cap.
func (s *session) exhausted() error {
	if s.spent() {
		return &BudgetError{Cap: CapToolCalls, Used: s.usage.ToolCalls, Limit: s.budget.MaxToolCalls}
	}
	return &BudgetError{Cap: CapTokens, Used: s.usage.Tokens, Limit: s.budget.MaxTokens}
}

// result packages what the run consumed, with raw set on success.
func (s *session) result(raw json.RawMessage) Result {
	return Result{Raw: raw, Usage: s.usage, Calls: s.calls}
}

// call executes one tool call, records it, and returns the content to feed
// back to the model. A tool error goes back to the model as JSON so it can
// adapt, rather than aborting the run.
func (s *session) call(ctx context.Context, tools Tools, name string, args json.RawMessage) string {
	s.usage.ToolCalls++
	rec := ToolCall{Tool: name, Arguments: truncateStr(string(args), maxLoggedArguments)}
	out, err := tools.Call(ctx, name, args)
	var content string
	if err != nil {
		s.usage.ToolErrors++
		rec.Error = truncateMiddle(err.Error(), maxLoggedError)
		content = `{"error":` + strconv.Quote(err.Error()) + `}`
	} else {
		content = string(out)
	}
	rec.ResultBytes = len(content)
	s.calls = append(s.calls, rec)
	return content
}
