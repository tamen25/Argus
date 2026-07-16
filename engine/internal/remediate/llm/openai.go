package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAIConfig configures the OpenAI-compatible chat-completions client. Any
// endpoint speaking that shape works (remote APIs or a user-supplied
// compatible endpoint) — that is the whole portability story.
type OpenAIConfig struct {
	Endpoint  string // full chat completions URL
	Model     string
	APIKey    string
	MaxTokens int
	// NoRedact opts OUT of value redaction. The zero value is redaction ON —
	// a forgotten config can never leak telemetry (architecture rule 8).
	NoRedact bool
	HTTP     *http.Client
}

// OpenAIClient talks to an OpenAI-compatible chat completions endpoint.
type OpenAIClient struct {
	cfg  OpenAIConfig
	http *http.Client
}

// NewOpenAI builds a client. A missing HTTP client gets a 60s-timeout default.
func NewOpenAI(cfg OpenAIConfig) *OpenAIClient {
	h := cfg.HTTP
	if h == nil {
		h = &http.Client{Timeout: 60 * time.Second}
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 512
	}
	return &OpenAIClient{cfg: cfg, http: h}
}

// Explain redacts (unless opted out), renders the prompt, and returns the
// model's explanation. Never auto-applied, never fed back into scoring.
func (c *OpenAIClient) Explain(ctx context.Context, in ExplainInput) (string, error) {
	if !c.cfg.NoRedact {
		in = Redact(in)
	}
	prompt, err := RenderPrompt(in)
	if err != nil {
		return "", err
	}

	reqBody, err := json.Marshal(map[string]any{
		"model":      c.cfg.Model,
		"max_tokens": c.cfg.MaxTokens,
		"messages": []map[string]string{
			{"role": "system", "content": "You explain observability instrumentation findings concisely and accurately."},
			{"role": "user", "content": prompt},
		},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("llm endpoint: HTTP %d: %s", resp.StatusCode, b)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm endpoint returned no choices")
	}
	return out.Choices[0].Message.Content, nil
}
