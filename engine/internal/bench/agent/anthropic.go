package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
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
		h = &http.Client{Timeout: 60 * time.Second}
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

// Diagnose runs the tool-use loop. Budget semantics match the OpenAI agent:
// tool calls counted (submit_diagnosis terminal, not counted), input+output
// tokens summed, overrun returns ErrBudgetExhausted with partial usage.
func (a *AnthropicAgent) Diagnose(ctx context.Context, task Task) (Result, error) {
	tools := a.toolDefs(task.Tools)
	msgs := []antMessage{{
		Role:    "user",
		Content: []antBlock{{Type: "text", Text: task.Brief + "\n\nWhen you have identified the root cause, call " + submitToolName + "."}},
	}}

	var usage Usage
	hardStep := 20
	if task.Budget.MaxToolCalls > 0 {
		hardStep = task.Budget.MaxToolCalls + 2
	}

	for step := 0; step < hardStep; step++ {
		usage.Steps++
		resp, err := a.messages(ctx, antRequest{
			Model:     a.cfg.Model,
			MaxTokens: a.cfg.MaxTokens,
			System:    systemPrompt,
			Messages:  msgs,
			Tools:     tools,
		})
		if err != nil {
			return Result{Usage: usage}, err
		}
		usage.Tokens += resp.Usage.InputTokens + resp.Usage.OutputTokens
		if task.Budget.MaxTokens > 0 && usage.Tokens > task.Budget.MaxTokens {
			return Result{Usage: usage}, ErrBudgetExhausted
		}

		// The assistant turn (its content blocks) must be echoed back verbatim.
		msgs = append(msgs, antMessage{Role: "assistant", Content: resp.Content})

		var toolResults []antBlock
		sawToolUse := false
		for _, b := range resp.Content {
			if b.Type != "tool_use" {
				continue
			}
			sawToolUse = true
			if b.Name == submitToolName {
				return Result{Raw: b.Input, Usage: usage}, nil
			}
			usage.ToolCalls++
			if task.Budget.MaxToolCalls > 0 && usage.ToolCalls > task.Budget.MaxToolCalls {
				return Result{Usage: usage}, ErrBudgetExhausted
			}
			toolResults = append(toolResults, antBlock{
				Type:      "tool_result",
				ToolUseID: b.ID,
				Content:   a.runToolBlock(ctx, task.Tools, b),
			})
		}

		if !sawToolUse {
			return Result{Usage: usage}, fmt.Errorf("agent %s: finished without calling %s", a.cfg.Name, submitToolName)
		}
		msgs = append(msgs, antMessage{Role: "user", Content: toolResults})
	}
	return Result{Usage: usage}, fmt.Errorf("agent %s: exceeded %d steps without submitting", a.cfg.Name, hardStep)
}

func (a *AnthropicAgent) runToolBlock(ctx context.Context, tools Tools, b antBlock) string {
	out, err := tools.Call(ctx, b.Name, b.Input)
	if err != nil {
		return `{"error":` + strconv.Quote(err.Error()) + `}`
	}
	return string(out)
}

func (a *AnthropicAgent) toolDefs(tools Tools) []antTool {
	var defs []antTool
	if tools != nil {
		for _, t := range tools.List() {
			defs = append(defs, antTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
		}
	}
	defs = append(defs, antTool{
		Name:        submitToolName,
		Description: "Submit the final diagnosis: the root-cause entities and fault category.",
		InputSchema: json.RawMessage(submitToolSchema),
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
		return antResponse{}, fmt.Errorf("agent %s: HTTP %d: %s", a.cfg.Name, resp.StatusCode, b)
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
