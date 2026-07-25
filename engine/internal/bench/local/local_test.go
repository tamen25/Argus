package local

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnforceLoopbackAcceptsLocalEndpoints(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:11434/v1/chat/completions",
		"http://localhost:11434/v1/chat/completions",
		"http://[::1]:11434/v1/chat/completions",
		"https://127.0.0.1:8443/v1/chat/completions",
	} {
		if err := EnforceLoopback("--endpoint", raw); err != nil {
			t.Errorf("EnforceLoopback(%q) = %v, want nil", raw, err)
		}
	}
}

func TestEnforceLoopbackRejectsRemoteEndpoints(t *testing.T) {
	// Every one of these is a way a paid call could slip in.
	for _, raw := range []string{
		"https://api.openai.com/v1/chat/completions",
		"https://api.anthropic.com/v1/messages",
		"http://192.168.1.50:11434/v1/chat/completions",
		"http://ollama.internal:11434/v1/chat/completions",
		"https://generativelanguage.googleapis.com/v1/chat",
	} {
		err := EnforceLoopback("--endpoint", raw)
		if !errors.Is(err, ErrRemoteEndpoint) {
			t.Errorf("EnforceLoopback(%q) = %v, want ErrRemoteEndpoint", raw, err)
		}
	}
}

// TestEnforceLoopbackRejectsResolvingHostnames pins the deliberate choice not to
// resolve names: a hostname that points at loopback today can point elsewhere
// tomorrow, and a policy that depends on DNS is not a policy.
func TestEnforceLoopbackRejectsResolvingHostnames(t *testing.T) {
	if err := EnforceLoopback("--endpoint", "http://my-local-box:11434/v1"); !errors.Is(err, ErrRemoteEndpoint) {
		t.Fatalf("got %v, want ErrRemoteEndpoint even though the name may resolve locally", err)
	}
}

func TestEnforceLoopbackRejectsNonHTTPSchemes(t *testing.T) {
	if err := EnforceLoopback("--endpoint", "file:///etc/passwd"); !errors.Is(err, ErrRemoteEndpoint) {
		t.Fatalf("got %v, want ErrRemoteEndpoint", err)
	}
}

func TestRefuseAPIKey(t *testing.T) {
	if err := RefuseAPIKey("--api-key-env", ""); err != nil {
		t.Fatalf("unset key env should be allowed, got %v", err)
	}
	if err := RefuseAPIKey("--api-key-env", "OPENAI_API_KEY"); !errors.Is(err, ErrAPIKeyForbidden) {
		t.Fatalf("got %v, want ErrAPIKeyForbidden", err)
	}
}

func TestEnforceDistinctJudge(t *testing.T) {
	if err := EnforceDistinctJudge("qwen3.6:35b", ""); err != nil {
		t.Fatalf("no judge configured should be allowed, got %v", err)
	}
	if err := EnforceDistinctJudge("qwen3.6:35b", "llama3.1:8b"); err != nil {
		t.Fatalf("distinct models should be allowed, got %v", err)
	}
	if err := EnforceDistinctJudge("qwen3.6:35b", "QWEN3.6:35B"); !errors.Is(err, ErrJudgeSameModel) {
		t.Fatalf("got %v, want ErrJudgeSameModel (comparison must be case-insensitive)", err)
	}
}

func TestRequireContext(t *testing.T) {
	// The trap: a huge architectural maximum with no num_ctx set. The model is
	// served at the runtime default and truncates silently.
	unset := ModelInfo{Model: "m", EffectiveNumCtx: 0, ArchContextLength: 262144}
	if err := unset.RequireContext(MinContextTokens); !errors.Is(err, ErrContextTooSmall) {
		t.Fatalf("got %v, want ErrContextTooSmall for unset num_ctx", err)
	}

	small := ModelInfo{Model: "m", EffectiveNumCtx: 4096}
	if err := small.RequireContext(MinContextTokens); !errors.Is(err, ErrContextTooSmall) {
		t.Fatalf("got %v, want ErrContextTooSmall for num_ctx=4096", err)
	}

	ok := ModelInfo{Model: "m", EffectiveNumCtx: MinContextTokens}
	if err := ok.RequireContext(MinContextTokens); err != nil {
		t.Fatalf("num_ctx exactly at the floor should pass, got %v", err)
	}
}

func TestParseNumCtx(t *testing.T) {
	// Real shape of Ollama's parameters block, whitespace-aligned.
	params := "min_p                          0\nnum_ctx                        32768\ntemperature                    1"
	if got := parseNumCtx(params); got != 32768 {
		t.Fatalf("parseNumCtx = %d, want 32768", got)
	}
	// The as-pulled qwen3.6 block has no num_ctx at all.
	noCtx := "min_p                          0\ntemperature                    1\ntop_k                          20"
	if got := parseNumCtx(noCtx); got != 0 {
		t.Fatalf("parseNumCtx = %d, want 0 when unset", got)
	}
}

func TestProbeReadsProvenanceAndContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			t.Errorf("probe hit %q, want /api/show", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"parameters": "num_ctx                        32768\ntemperature                    1",
			"details": {"family":"qwen35moe","parameter_size":"36.0B","quantization_level":"Q4_K_M"},
			"model_info": {"qwen35moe.context_length": 262144}
		}`))
	}))
	defer srv.Close()

	got, err := Probe(context.Background(), srv.URL+"/v1/chat/completions", "qwen3.6:35b-a3b-q4_K_M", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if got.EffectiveNumCtx != 32768 {
		t.Errorf("EffectiveNumCtx = %d, want 32768", got.EffectiveNumCtx)
	}
	if got.ArchContextLength != 262144 {
		t.Errorf("ArchContextLength = %d, want 262144", got.ArchContextLength)
	}
	if got.Quantization != "Q4_K_M" {
		t.Errorf("Quantization = %q, want Q4_K_M", got.Quantization)
	}
	if got.ParameterSize != "36.0B" {
		t.Errorf("ParameterSize = %q, want 36.0B", got.ParameterSize)
	}
	if err := got.RequireContext(MinContextTokens); err != nil {
		t.Errorf("RequireContext = %v, want nil", err)
	}
}

func TestProbeSurfacesUnsetNumCtx(t *testing.T) {
	// Mirrors the model exactly as pulled: no num_ctx in parameters.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"parameters": "temperature                    1\ntop_k                          20",
			"details": {"quantization_level":"Q4_K_M"},
			"model_info": {"qwen35moe.context_length": 262144}
		}`))
	}))
	defer srv.Close()

	got, err := Probe(context.Background(), srv.URL+"/v1/chat/completions", "m", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if got.EffectiveNumCtx != 0 {
		t.Fatalf("EffectiveNumCtx = %d, want 0", got.EffectiveNumCtx)
	}
	if err := got.RequireContext(MinContextTokens); !errors.Is(err, ErrContextTooSmall) {
		t.Fatalf("got %v, want ErrContextTooSmall", err)
	}
}
