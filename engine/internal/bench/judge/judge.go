// Package judge normalizes free-form agent output into a bench.Diagnosis using
// an LLM.
//
// It is NON-DETERMINISTIC by construction, and says so: Method() returns
// "llm-judge", which the orchestrator records so every result normalized this
// way discloses it (architecture rule 7). It exists only as the fallback for
// shell agents whose native output we cannot deterministically map — callers
// should try bench.JSONNormalizer first and record the method actually used.
//
// The judge shapes an answer; it never grades one. bench/scoring does not
// import this package, so no LLM can influence a score (architecture rule 2).
package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tamen25/Argus/engine/internal/bench"
)

// Config configures the OpenAI-compatible endpoint used for judging.
type Config struct {
	Endpoint  string
	Model     string
	APIKey    string
	MaxTokens int
	HTTP      *http.Client
}

// LLMJudge implements bench.Normalizer by asking a model to extract the scored
// fields from an agent's prose.
type LLMJudge struct {
	cfg  Config
	http *http.Client
}

// New builds a judge, defaulting the HTTP client and token cap.
func New(cfg Config) *LLMJudge {
	h := cfg.HTTP
	if h == nil {
		h = &http.Client{Timeout: 60 * time.Second}
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 512
	}
	return &LLMJudge{cfg: cfg, http: h}
}

// Method identifies this normalizer in the run record. Always "llm-judge" —
// this normalizer always calls a model, so the label is never misleading.
func (j *LLMJudge) Method() string { return "llm-judge" }

const judgeSystem = "You convert an SRE agent's free-form incident diagnosis into strict JSON. " +
	"Extract only what the agent actually claimed — never invent a root cause, and never infer one " +
	"the agent did not state. Reply with JSON only, no prose and no code fences."

const judgeShape = `{"root_cause_entities":[{"kind":"Deployment","namespace":"ns","name":"svc"}],` +
	`"category":"short-fault-category","summary":"one line","confidence":0.0}`

// Normalize asks the model to extract a Diagnosis from raw agent output, then
// strictly parses and validates the reply. The scenario name is forced
// authoritative, so neither the agent nor the judge can mislabel the answer.
func (j *LLMJudge) Normalize(ctx context.Context, raw []byte, scenario string) (bench.Diagnosis, error) {
	prompt := "Agent output:\n\n" + string(raw) +
		"\n\nReturn JSON with exactly this shape:\n" + judgeShape +
		"\n\nOmit summary and confidence if the agent did not state them."

	content, err := j.chat(ctx, prompt)
	if err != nil {
		return bench.Diagnosis{}, err
	}
	d, err := bench.ParseDiagnosis([]byte(extractJSON(content)))
	if err != nil {
		return bench.Diagnosis{}, fmt.Errorf("llm-judge: %w (reply: %s)", err, truncate(content, 256))
	}
	d.Scenario = scenario
	if err := d.Validate(); err != nil {
		return bench.Diagnosis{}, fmt.Errorf("llm-judge: %w", err)
	}
	return d, nil
}

// extractJSON strips code fences and surrounding prose, returning the outermost
// JSON object. Models wrap replies despite instructions; failing the whole run
// over a fence would be a normalization bug, not an agent failure.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 && !strings.HasPrefix(rest, "{") {
			rest = rest[nl+1:] // drop a ```json language tag
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			rest = rest[:end]
		}
		s = strings.TrimSpace(rest)
	}
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (j *LLMJudge) chat(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model":      j.cfg.Model,
		"max_tokens": j.cfg.MaxTokens,
		"messages": []map[string]string{
			{"role": "system", "content": judgeSystem},
			{"role": "user", "content": prompt},
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if j.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+j.cfg.APIKey)
	}
	resp, err := j.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("llm-judge: HTTP %d: %s", resp.StatusCode, b)
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
		return "", fmt.Errorf("llm-judge: endpoint returned no choices")
	}
	return out.Choices[0].Message.Content, nil
}

var _ bench.Normalizer = (*LLMJudge)(nil)
