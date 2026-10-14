package agent

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tamen25/Argus/engine/internal/bench"
	"github.com/tamen25/Argus/engine/internal/bounded"
)

// OpenAIConfig configures an OpenAI-compatible chat-completions agent. Any
// endpoint speaking that shape works — a remote API or a user-supplied
// compatible endpoint — which is the portability story for bench subjects.
type OpenAIConfig struct {
	Name     string // agent name for the run record (e.g. the model id)
	Endpoint string // full chat completions URL
	Model    string
	APIKey   string
	HTTP     *http.Client
	// Timeout caps a single chat completion when HTTP is nil. A reasoning model
	// can spend minutes on one turn, and too short a timeout kills the run
	// mid-investigation and records it as an agent failure rather than a limit
	// of the endpoint.
	Timeout time.Duration
	// MaxTurnTokens caps the output of a single turn (max_tokens). Zero sends no
	// cap. The run's token budget is only checked between turns, so without it
	// a model that loops in its reasoning holds one turn open until Timeout:
	// the first flagship session lost 30 minutes to one such turn.
	MaxTurnTokens int
}

// OpenAIAgent runs an agentic tool-use loop against an OpenAI-compatible
// endpoint: it presents the read-only MCP tools plus a terminal
// submit_diagnosis tool, executes tool calls the model requests, and returns
// the submit_diagnosis arguments as the raw diagnosis.
type OpenAIAgent struct {
	cfg  OpenAIConfig
	http *http.Client
}

// NewOpenAI builds an agent. A missing HTTP client gets a 60s-timeout default.
func NewOpenAI(cfg OpenAIConfig) *OpenAIAgent {
	h := cfg.HTTP
	if h == nil {
		h = &http.Client{Timeout: cmp.Or(cfg.Timeout, DefaultAgentTimeout)}
	}
	if cfg.Name == "" {
		cfg.Name = cfg.Model
	}
	return &OpenAIAgent{cfg: cfg, http: h}
}

// Name identifies the agent in the run record.
func (a *OpenAIAgent) Name() string { return a.cfg.Name }

// Diagnose runs the shared tool-use loop (runTurns) over the OpenAI
// chat-completions format.
func (a *OpenAIAgent) Diagnose(ctx context.Context, task Task) (Result, error) {
	conv := &oaConversation{
		agent: a,
		tools: a.toolDefs(task.Tools, task.Categories),
		msgs: []oaMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: openingBrief(task)},
		},
	}
	return runTurns(ctx, a.cfg.Name, task, conv, a.cfg.MaxTurnTokens)
}

// oaConversation is a run's message list in the chat-completions format.
type oaConversation struct {
	agent *OpenAIAgent
	tools []oaTool
	msgs  []oaMessage
	last  oaMessage // the model's last reply
}

func (c *oaConversation) next(ctx context.Context) (reply, error) {
	resp, err := c.agent.chat(ctx, oaRequest{
		Model:      c.agent.cfg.Model,
		MaxTokens:  c.agent.cfg.MaxTurnTokens,
		Messages:   c.msgs,
		Tools:      c.tools,
		ToolChoice: "auto",
	})
	if err != nil {
		return reply{}, err
	}
	if len(resp.Choices) == 0 {
		return reply{}, fmt.Errorf("agent %s: endpoint returned no choices", c.agent.cfg.Name)
	}
	choice := resp.Choices[0]
	c.last = choice.Message
	r := reply{text: choice.Message.Content, truncated: choice.FinishReason == "length", tokens: resp.Usage.TotalTokens}
	for _, tc := range choice.Message.ToolCalls {
		r.calls = append(r.calls, toolUse{id: tc.ID, name: tc.Function.Name, args: json.RawMessage(tc.Function.Arguments)})
	}
	return r, nil
}

// keepReply appends the last reply. An assistant message needs content or
// tool calls: Ollama answers one with neither (a reasoning model whose visible
// reply was empty) with HTTP 400.
func (c *oaConversation) keepReply() {
	if c.last.Content == "" && len(c.last.ToolCalls) == 0 {
		return
	}
	c.msgs = append(c.msgs, c.last)
}

// answerTools appends one "tool" message per call, then the notice as a user
// message.
func (c *oaConversation) answerTools(results []toolResult, notice string) {
	for _, r := range results {
		c.msgs = append(c.msgs, oaMessage{Role: "tool", ToolCallID: r.id, Name: r.name, Content: r.content})
	}
	if notice != "" {
		c.say(notice)
	}
}

func (c *oaConversation) say(text string) {
	c.msgs = append(c.msgs, oaMessage{Role: "user", Content: text})
}

// toolDefs converts the read-only MCP tools into OpenAI function definitions and
// appends the terminal submit_diagnosis tool.
func (a *OpenAIAgent) toolDefs(tools Tools, categories []bench.Category) []oaTool {
	var defs []oaTool
	if tools != nil {
		for _, t := range tools.List() {
			defs = append(defs, oaTool{
				Type: "function",
				Function: oaFunctionDef{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				},
			})
		}
	}
	defs = append(defs, oaTool{
		Type: "function",
		Function: oaFunctionDef{
			Name:        submitToolName,
			Description: "Submit the final diagnosis: the root-cause entities and fault category.",
			Parameters:  submitSchema(categories),
		},
	})
	return defs
}

func (a *OpenAIAgent) chat(ctx context.Context, body oaRequest) (oaResponse, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return oaResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.Endpoint, bytes.NewReader(buf))
	if err != nil {
		return oaResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return oaResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return oaResponse{}, &httpStatusError{Agent: a.cfg.Name, Status: resp.StatusCode, Body: string(b)}
	}
	var out oaResponse
	if err := bounded.DecodeJSON(resp.Body, bounded.ModelResponse, &out); err != nil {
		return oaResponse{}, err
	}
	return out, nil
}

// OpenAI-compatible chat-completions wire types (function-calling subset).

type oaRequest struct {
	Model      string      `json:"model"`
	MaxTokens  int         `json:"max_tokens,omitempty"`
	Messages   []oaMessage `json:"messages"`
	Tools      []oaTool    `json:"tools,omitempty"`
	ToolChoice string      `json:"tool_choice,omitempty"`
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content,omitempty"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	Name       string       `json:"name,omitempty"`
}

type oaToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function oaFunctionCall `json:"function"`
}

type oaFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON encoded as a string
}

type oaTool struct {
	Type     string        `json:"type"`
	Function oaFunctionDef `json:"function"`
}

type oaFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type oaResponse struct {
	Choices []struct {
		Message      oaMessage `json:"message"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

var _ Agent = (*OpenAIAgent)(nil)
