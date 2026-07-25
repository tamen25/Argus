// Package local enforces the local-inference policy for bench runs and records
// what actually served a result.
//
// Two failure modes this package exists to make impossible rather than merely
// unlikely:
//
//   - A bench run silently billing a paid API. Endpoints must resolve to
//     loopback and API keys are refused outright, so the only way to spend money
//     is to change this code, not to mistype a flag.
//   - A bench run producing numbers under silent context truncation. Ollama
//     serves a model at its own small default context unless num_ctx is set
//     explicitly; the MCP surface plus telemetry context overruns that easily,
//     and the agent then fails for reasons unrelated to its diagnostic ability.
//     A result produced that way is worse than no result, because it looks fine.
package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MinContextTokens is the floor for a defensible bench run. The read-only MCP
// surface is five tools plus their schemas, and a diagnosis turn carries
// telemetry excerpts on top of the running transcript; below this the model
// starts dropping the earliest tool output without saying so.
const MinContextTokens = 32768

// Sentinel errors, so callers and tests can assert the reason rather than
// matching on message text.
var (
	// ErrRemoteEndpoint means an endpoint was not loopback.
	ErrRemoteEndpoint = errors.New("local: endpoint is not loopback")
	// ErrAPIKeyForbidden means an API key was supplied under a local-only policy.
	ErrAPIKeyForbidden = errors.New("local: API keys are forbidden")
	// ErrContextTooSmall means the served context is below MinContextTokens.
	ErrContextTooSmall = errors.New("local: effective context below the safe floor")
	// ErrJudgeSameModel means the judge and the agent are the same model.
	ErrJudgeSameModel = errors.New("local: judge model must differ from the agent model")
)

// EnforceLoopback rejects any endpoint that is not on this machine. Hostnames
// other than the loopback literals are refused without resolving them: a name
// that resolves to 127.0.0.1 today can resolve elsewhere tomorrow, and a policy
// that depends on DNS is not a policy.
//
// what names the flag being checked, so the error says which one to fix.
func EnforceLoopback(what, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("%s is empty", what)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s %q: %w", what, raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: %s %q has scheme %q", ErrRemoteEndpoint, what, raw, u.Scheme)
	}
	host := u.Hostname()
	if !isLoopbackHost(host) {
		return fmt.Errorf("%w: %s %q points at %q. This benchmark runs against local "+
			"inference only; no paid API, no keys. Use http://127.0.0.1:11434/v1/... instead",
			ErrRemoteEndpoint, what, raw, host)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// RefuseAPIKey rejects a configured API key. Under the local-only policy there
// is nothing to authenticate to, so a key being present at all means the run was
// pointed somewhere it should not be.
func RefuseAPIKey(what, envName string) error {
	if strings.TrimSpace(envName) == "" {
		return nil
	}
	return fmt.Errorf("%w: %s=%q was set. Local inference needs no credentials; "+
		"remove the flag", ErrAPIKeyForbidden, what, envName)
}

// EnforceDistinctJudge refuses to let one model both produce and normalize an
// answer. A model that misreads its own output the same way twice launders its
// error into the score, and the scoring path would silently inherit the agent's
// mistakes.
func EnforceDistinctJudge(agentModel, judgeModel string) error {
	if strings.TrimSpace(judgeModel) == "" {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(agentModel), strings.TrimSpace(judgeModel)) {
		return fmt.Errorf("%w: both are %q. Pull a second, different model for the judge",
			ErrJudgeSameModel, agentModel)
	}
	return nil
}

// ModelInfo is the provenance recorded for every result. Without it a
// leaderboard row cannot be reproduced or defended: the same tag at a different
// quantization or a different served context is a different subject.
type ModelInfo struct {
	// Endpoint is the chat endpoint that served the run.
	Endpoint string `json:"endpoint"`
	// Model is the exact tag, including quantization suffix.
	Model string `json:"model"`
	// Quantization is the weight format actually loaded (e.g. Q4_K_M).
	Quantization string `json:"quantization,omitempty"`
	// Family and ParameterSize describe the architecture.
	Family        string `json:"family,omitempty"`
	ParameterSize string `json:"parameter_size,omitempty"`
	// EffectiveNumCtx is the context the model is actually served with — the
	// num_ctx parameter baked into the model, NOT its architectural maximum.
	// Zero means no num_ctx was set and the runtime default applies.
	EffectiveNumCtx int `json:"effective_num_ctx"`
	// ArchContextLength is the architectural maximum, recorded for contrast: a
	// large value here with a small EffectiveNumCtx is exactly the trap this
	// package exists to catch.
	ArchContextLength int `json:"arch_context_length,omitempty"`
}

// RequireContext asserts the served context clears the floor. An unset num_ctx
// is treated as a failure, not as "probably fine": the runtime default is small
// and the truncation it causes is silent.
func (m ModelInfo) RequireContext(minTokens int) error {
	if m.EffectiveNumCtx == 0 {
		return fmt.Errorf("%w: model %q has no num_ctx set, so it is served at the Ollama "+
			"default (far below %d). Its architecture supports %d, but that maximum is not "+
			"what gets served. Build a variant with an explicit num_ctx — see "+
			"deploy/ollama/Modelfile.qwen3.6-bench",
			ErrContextTooSmall, m.Model, minTokens, m.ArchContextLength)
	}
	if m.EffectiveNumCtx < minTokens {
		return fmt.Errorf("%w: model %q is served with num_ctx=%d, below the %d floor. "+
			"Tool schemas plus telemetry would be truncated without warning",
			ErrContextTooSmall, m.Model, m.EffectiveNumCtx, minTokens)
	}
	return nil
}

// Probe asks Ollama what it will actually serve for this model. chatEndpoint is
// the OpenAI-compatible URL used for inference; the management API is derived
// from its origin.
func Probe(ctx context.Context, chatEndpoint, model string, hc *http.Client) (ModelInfo, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	base, err := originOf(chatEndpoint)
	if err != nil {
		return ModelInfo{}, err
	}
	body, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return ModelInfo{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/show", strings.NewReader(string(body)))
	if err != nil {
		return ModelInfo{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return ModelInfo{}, fmt.Errorf("probing %s for model %q: %w (is Ollama running?)", base, model, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ModelInfo{}, fmt.Errorf("probing %s for model %q: HTTP %d", base, model, resp.StatusCode)
	}

	var show struct {
		Parameters string `json:"parameters"`
		Details    struct {
			Family            string `json:"family"`
			ParameterSize     string `json:"parameter_size"`
			QuantizationLevel string `json:"quantization_level"`
		} `json:"details"`
		ModelInfo map[string]any `json:"model_info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&show); err != nil {
		return ModelInfo{}, fmt.Errorf("decoding /api/show for %q: %w", model, err)
	}

	return ModelInfo{
		Endpoint:          chatEndpoint,
		Model:             model,
		Quantization:      show.Details.QuantizationLevel,
		Family:            show.Details.Family,
		ParameterSize:     show.Details.ParameterSize,
		EffectiveNumCtx:   parseNumCtx(show.Parameters),
		ArchContextLength: archContextLength(show.ModelInfo),
	}, nil
}

// parseNumCtx reads num_ctx out of Ollama's whitespace-aligned parameters block:
//
//	temperature                    1
//	num_ctx                        32768
//
// An absent num_ctx returns 0, which RequireContext treats as a failure.
func parseNumCtx(params string) int {
	for _, line := range strings.Split(params, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "num_ctx" {
			if n, err := strconv.Atoi(fields[1]); err == nil {
				return n
			}
		}
	}
	return 0
}

// archContextLength finds the "<family>.context_length" entry, whose key is
// architecture-dependent (e.g. qwen35moe.context_length).
func archContextLength(mi map[string]any) int {
	for k, v := range mi {
		if !strings.HasSuffix(k, ".context_length") {
			continue
		}
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	return 0
}

// originOf reduces a chat endpoint to scheme://host[:port].
func originOf(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parsing endpoint %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("endpoint %q is not an absolute URL", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}
