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

// OpenAIConfig configures an OpenAI-compatible chat-completions agent. Any
// endpoint speaking that shape works — a remote API or a user-supplied
// compatible endpoint — which is the portability story for bench subjects.
type OpenAIConfig struct {
	Name     string // agent name for the run record (e.g. the model id)
	Endpoint string // full chat completions URL
	Model    string
	APIKey   string
	HTTP     *http.Client
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
		h = &http.Client{Timeout: 60 * time.Second}
	}
	if cfg.Name == "" {
		cfg.Name = cfg.Model
	}
	return &OpenAIAgent{cfg: cfg, http: h}
}

// Name identifies the agent in the run record.
func (a *OpenAIAgent) Name() string { return a.cfg.Name }

// Diagnose runs the tool-use loop. Budget caps are enforced between steps:
// tool calls are counted (submit_diagnosis is terminal and not counted), and
// cumulative reported tokens are summed as a conservative upper bound.
func (a *OpenAIAgent) Diagnose(ctx context.Context, task Task) (Result, error) {
	tools := a.toolDefs(task.Tools)
	msgs := []oaMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: task.Brief + "\n\nWhen you have identified the root cause, call " + submitToolName + "."},
	}

	var usage Usage
	hardStep := 20
	if task.Budget.MaxToolCalls > 0 {
		hardStep = task.Budget.MaxToolCalls + 2 // room for the terminal submit + a final turn
	}

	for step := 0; step < hardStep; step++ {
		usage.Steps++
		resp, err := a.chat(ctx, oaRequest{
			Model:      a.cfg.Model,
			Messages:   msgs,
			Tools:      tools,
			ToolChoice: "auto",
		})
		if err != nil {
			return Result{Usage: usage}, err
		}
		usage.Tokens += resp.Usage.TotalTokens
		if task.Budget.MaxTokens > 0 && usage.Tokens > task.Budget.MaxTokens {
			return Result{Usage: usage}, ErrBudgetExhausted
		}
		if len(resp.Choices) == 0 {
			return Result{Usage: usage}, fmt.Errorf("agent %s: endpoint returned no choices", a.cfg.Name)
		}
		msg := resp.Choices[0].Message

		if len(msg.ToolCalls) == 0 {
			// The model answered without calling submit_diagnosis: no structured
			// diagnosis. Treated as a failed run, not a silent empty answer.
			return Result{Usage: usage}, fmt.Errorf("agent %s: finished without calling %s", a.cfg.Name, submitToolName)
		}

		msgs = append(msgs, msg) // assistant turn carrying the tool_calls

		for _, tc := range msg.ToolCalls {
			if tc.Function.Name == submitToolName {
				return Result{Raw: json.RawMessage(tc.Function.Arguments), Usage: usage}, nil
			}
			usage.ToolCalls++
			if task.Budget.MaxToolCalls > 0 && usage.ToolCalls > task.Budget.MaxToolCalls {
				return Result{Usage: usage}, ErrBudgetExhausted
			}
			content := a.runTool(ctx, task.Tools, tc)
			msgs = append(msgs, oaMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    content,
			})
		}
	}
	return Result{Usage: usage}, fmt.Errorf("agent %s: exceeded %d steps without submitting", a.cfg.Name, hardStep)
}

// runTool executes one requested tool call and returns the content string to
// feed back. A tool error is returned to the model as JSON so it can adapt,
// rather than aborting the run.
func (a *OpenAIAgent) runTool(ctx context.Context, tools Tools, tc oaToolCall) string {
	out, err := tools.Call(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
	if err != nil {
		return `{"error":` + strconv.Quote(err.Error()) + `}`
	}
	return string(out)
}

// toolDefs converts the read-only MCP tools into OpenAI function definitions and
// appends the terminal submit_diagnosis tool.
func (a *OpenAIAgent) toolDefs(tools Tools) []oaTool {
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
			Parameters:  json.RawMessage(submitToolSchema),
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
		return oaResponse{}, fmt.Errorf("agent %s: HTTP %d: %s", a.cfg.Name, resp.StatusCode, b)
	}
	var out oaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
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
