package agent

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/tamen25/Argus/engine/internal/bench"
)

// AnthropicConfig configures an Anthropic Messages API agent. It is a second
// implementation of the Agent interface (master plan §7): same tool surface and
// budget semantics as the OpenAI agent, different wire protocol.
type AnthropicConfig struct {
	Name      string
	Endpoint  string // default https://api.anthropic.com/v1/messages
	Model     string
	APIKey    string
	Version   string // anthropic-version header; default 2023-06-01
	MaxTokens int    // required by the API; default 1024
	HTTP      *http.Client
	// Timeout caps a single model call when HTTP is nil; see DefaultAgentTimeout.
	Timeout time.Duration
}

// AnthropicAgent runs an agentic tool-use loop against the Anthropic Messages
// API, presenting the read-only MCP tools plus the terminal submit_diagnosis
// tool.
type AnthropicAgent struct {
	cfg  AnthropicConfig
	http *http.Client
}

const (
	defaultAnthropicEndpoint = "https://api.anthropic.com/v1/messages"
	defaultAnthropicVersion  = "2023-06-01"
)

// NewAnthropic builds an Anthropic agent, filling endpoint/version/max-tokens
// defaults.
func NewAnthropic(cfg AnthropicConfig) *AnthropicAgent {
	h := cfg.HTTP
	if h == nil {
		h = &http.Client{Timeout: cmp.Or(cfg.Timeout, DefaultAgentTimeout)}
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultAnthropicEndpoint
	}
	if cfg.Version == "" {
		cfg.Version = defaultAnthropicVersion
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 1024
	}
	if cfg.Name == "" {
		cfg.Name = cfg.Model
	}
	return &AnthropicAgent{cfg: cfg, http: h}
}

// Name identifies the agent in the run record.
func (a *AnthropicAgent) Name() string { return a.cfg.Name }

// Diagnose runs the shared tool-use loop (runTurns) over the Anthropic
// Messages format.
func (a *AnthropicAgent) Diagnose(ctx context.Context, task Task) (Result, error) {
	conv := &antConversation{
		agent: a,
		tools: a.toolDefs(task.Tools, task.Categories),
		msgs:  []antMessage{{Role: "user", Content: []antBlock{{Type: "text", Text: openingBrief(task)}}}},
	}
	return runTurns(ctx, a.cfg.Name, task, conv, a.cfg.MaxTokens)
}

// antConversation is a run's message list in the Messages format.
type antConversation struct {
	agent *AnthropicAgent
	tools []antTool
	msgs  []antMessage
	last  []antBlock // the model's last reply
}

func (c *antConversation) next(ctx context.Context) (reply, error) {
	resp, err := c.agent.messages(ctx, antRequest{
		Model:     c.agent.cfg.Model,
		MaxTokens: c.agent.cfg.MaxTokens,
		System:    systemPrompt,
		Messages:  c.msgs,
		Tools:     c.tools,
	})
	if err != nil {
		return reply{}, err
	}
	c.last = resp.Content
	r := reply{truncated: resp.StopReason == "max_tokens", tokens: resp.Usage.InputTokens + resp.Usage.OutputTokens}
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			r.text += b.Text
		case "tool_use":
			r.calls = append(r.calls, toolUse{id: b.ID, name: b.Name, args: b.Input})
		}
	}
	return r, nil
}

// keepReply appends the last reply's content blocks verbatim, as the API
// requires; an empty turn is invalid on the wire and is skipped.
func (c *antConversation) keepReply() {
	if len(c.last) == 0 {
		return
	}
	c.msgs = append(c.msgs, antMessage{Role: "assistant", Content: c.last})
}

// answerTools appends one user turn: a tool_result block per call, then the
// notice as a text block.
func (c *antConversation) answerTools(results []toolResult, notice string) {
	blocks := make([]antBlock, 0, len(results)+1)
	for _, r := range results {
		blocks = append(blocks, antBlock{Type: "tool_result", ToolUseID: r.id, Content: r.content})
	}
	if notice != "" {
		blocks = append(blocks, antBlock{Type: "text", Text: notice})
	}
	c.msgs = append(c.msgs, antMessage{Role: "user", Content: blocks})
}

func (c *antConversation) say(text string) {
	c.msgs = append(c.msgs, antMessage{Role: "user", Content: []antBlock{{Type: "text", Text: text}}})
}

func (a *AnthropicAgent) toolDefs(tools Tools, categories []bench.Category) []antTool {
	var defs []antTool
	if tools != nil {
		for _, t := range tools.List() {
			defs = append(defs, antTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
		}
	}
	defs = append(defs, antTool{
		Name:        submitToolName,
		Description: "Submit the final diagnosis: the root-cause entities and fault category.",
		InputSchema: submitSchema(categories),
	})
	return defs
}

func (a *AnthropicAgent) messages(ctx context.Context, body antRequest) (antResponse, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return antResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.Endpoint, bytes.NewReader(buf))
	if err != nil {
		return antResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", a.cfg.Version)
	if a.cfg.APIKey != "" {
		req.Header.Set("x-api-key", a.cfg.APIKey)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return antResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return antResponse{}, &httpStatusError{Agent: a.cfg.Name, Status: resp.StatusCode, Body: string(b)}
	}
	var out antResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return antResponse{}, err
	}
	return out, nil
}

// Anthropic Messages API wire types (tool-use subset).

type antRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    string       `json:"system,omitempty"`
	Messages  []antMessage `json:"messages"`
	Tools     []antTool    `json:"tools,omitempty"`
}

type antTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type antMessage struct {
	Role    string     `json:"role"`
	Content []antBlock `json:"content"`
}

// antBlock is the content-block union for both directions: text, tool_use
// (assistant → us), and tool_result (us → assistant).
type antBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type antResponse struct {
	Content    []antBlock `json:"content"`
	StopReason string     `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

var _ Agent = (*AnthropicAgent)(nil)
