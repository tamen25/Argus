package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The OpenAI-compatible client posts a chat completion and returns the
// assistant text. It redacts by default, so raw telemetry never reaches the
// endpoint even if the caller forgets.
func TestOpenAIExplainRedactsAndParses(t *testing.T) {
	var gotBody string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"This label explodes cardinality; the patch drops it."}}]}`))
	}))
	defer srv.Close()

	c := NewOpenAI(OpenAIConfig{Endpoint: srv.URL, Model: "gpt-x", APIKey: "sk-test", MaxTokens: 300})
	out, err := c.Explain(context.Background(), ExplainInput{
		RuleID:   "MET-001",
		Service:  "checkout",
		Patch:    "// patch",
		Evidence: []Evidence{{Summary: "user_id has 50000 values", Attrs: map[string]any{"attribute": "user_id", "cardinality": 50000}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "explodes cardinality") {
		t.Errorf("explanation = %q", out)
	}
	// default redaction: raw values must not appear in the request to the model
	if strings.Contains(gotBody, "user_id") || strings.Contains(gotBody, "50000") {
		t.Errorf("un-redacted value reached the endpoint:\n%s", gotBody)
	}
	// the request carries model + bearer auth
	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil {
		t.Fatal(err)
	}
	if req.Model != "gpt-x" {
		t.Errorf("model = %q", req.Model)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth header = %q", gotAuth)
	}
}

// A non-2xx from the endpoint is an error, not a silent empty explanation.
func TestOpenAIExplainErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewOpenAI(OpenAIConfig{Endpoint: srv.URL, Model: "m"})
	if _, err := c.Explain(context.Background(), ExplainInput{}); err == nil {
		t.Error("want error on 401")
	}
}

// NoRedact disables redaction (opt-in, off by default) — raw values then flow.
func TestOpenAINoRedactSendsRawValues(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	c := NewOpenAI(OpenAIConfig{Endpoint: srv.URL, Model: "m", NoRedact: true})
	_, err := c.Explain(context.Background(), ExplainInput{Evidence: []Evidence{{Attrs: map[string]any{"attribute": "user_id"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotBody, "user_id") {
		t.Error("NoRedact should have sent the raw value")
	}
}

// The client satisfies the Client interface.
var _ Client = (*OpenAIClient)(nil)
