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

	"github.com/tamen25/Argus/engine/internal/mcp"
)

// ErrBudgetExhausted is returned when a run hits its tool-call or token cap
// before producing a diagnosis. The orchestrator records it as a no-diagnosis
// run, not a crash.
var ErrBudgetExhausted = errors.New("agent: budget exhausted before diagnosis")

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
}

// Usage records what a run consumed, for the honest run record (rules 6/7).
type Usage struct {
	ToolCalls int `json:"tool_calls"`
	Tokens    int `json:"tokens"`
	Steps     int `json:"steps"`
}

// Result is an agent's raw answer plus usage. Raw is fed to a bench.Normalizer
// (JSON for API agents; native output for shell agents), never to the scorer
// directly.
type Result struct {
	Raw   json.RawMessage
	Usage Usage
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
const submitToolSchema = `{"type":"object","required":["root_cause_entities","category"],` +
	`"properties":{` +
	`"root_cause_entities":{"type":"array","items":{"type":"object","required":["kind","name"],` +
	`"properties":{"kind":{"type":"string"},"namespace":{"type":"string"},"name":{"type":"string"}}}},` +
	`"category":{"type":"string"},` +
	`"summary":{"type":"string"},` +
	`"confidence":{"type":"number"}}}`

const systemPrompt = "You are an SRE incident-diagnosis agent. Investigate the incident using the " +
	"read-only observability tools (metrics, logs, traces, alerts, topology). Do not guess — use the " +
	"tools to gather evidence. Identify the root-cause Kubernetes entities and the fault category. " +
	"When you are confident, call " + submitToolName + " with the root-cause entities and category."
