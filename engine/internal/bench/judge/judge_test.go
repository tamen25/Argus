package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func chatServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		b, err := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": content}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}))
}

func newJudge(srv *httptest.Server) *LLMJudge {
	return New(Config{Endpoint: srv.URL, Model: "m", HTTP: srv.Client()})
}

const cleanJSON = `{"root_cause_entities":[{"kind":"Deployment","namespace":"otel-demo","name":"checkout"}],"category":"cardinality-explosion"}`

func TestJudge_Method(t *testing.T) {
	if m := New(Config{}).Method(); m != "llm-judge" {
		t.Errorf("Method() = %q, want llm-judge", m)
	}
}

func TestJudge_NormalizeCleanJSON(t *testing.T) {
	srv := chatServer(t, cleanJSON)
	defer srv.Close()

	d, err := newJudge(srv).Normalize(context.Background(), []byte("checkout blew up cardinality"), "the-scenario")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if d.Scenario != "the-scenario" {
		t.Errorf("scenario = %q, want forced authoritative", d.Scenario)
	}
	if len(d.RootCauseEntities) != 1 || d.RootCauseEntities[0].Name != "checkout" {
		t.Errorf("entities = %+v", d.RootCauseEntities)
	}
}

func TestJudge_StripsFencesAndProse(t *testing.T) {
	cases := map[string]string{
		"json fence":   "```json\n" + cleanJSON + "\n```",
		"bare fence":   "```\n" + cleanJSON + "\n```",
		"prose around": "Here is the JSON you asked for:\n" + cleanJSON + "\nHope that helps!",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			srv := chatServer(t, content)
			defer srv.Close()
			d, err := newJudge(srv).Normalize(context.Background(), []byte("raw"), "s")
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			if d.Category != "cardinality-explosion" {
				t.Errorf("category = %q", d.Category)
			}
		})
	}
}

func TestJudge_RejectsUnparseableReply(t *testing.T) {
	srv := chatServer(t, "I could not determine the root cause.")
	defer srv.Close()
	if _, err := newJudge(srv).Normalize(context.Background(), []byte("raw"), "s"); err == nil {
		t.Error("expected error on non-JSON reply")
	}
}

func TestJudge_RejectsInvalidDiagnosis(t *testing.T) {
	// Well-formed JSON, but no entities — must fail validation, not score as zero.
	srv := chatServer(t, `{"root_cause_entities":[],"category":"x"}`)
	defer srv.Close()
	_, err := newJudge(srv).Normalize(context.Background(), []byte("raw"), "s")
	if err == nil || !strings.Contains(err.Error(), "root_cause_entities") {
		t.Errorf("expected validation error, got %v", err)
	}
}

func TestJudge_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, "nope")
	}))
	defer srv.Close()
	if _, err := newJudge(srv).Normalize(context.Background(), []byte("raw"), "s"); err == nil {
		t.Error("expected HTTP error")
	}
}

func TestExtractJSON(t *testing.T) {
	tests := map[string]string{
		`{"a":1}`:                 `{"a":1}`,
		"```json\n{\"a\":1}\n```": `{"a":1}`,
		"text {\"a\":1} more":     `{"a":1}`,
		"no json here":            "no json here",
	}
	for in, want := range tests {
		if got := extractJSON(in); got != want {
			t.Errorf("extractJSON(%q) = %q, want %q", in, got, want)
		}
	}
}
