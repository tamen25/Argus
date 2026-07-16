package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The calibrate-soak fixture report has ARG-LOG-001 findings on checkout
// and cart — remediate turns them into patch files.
func TestRunRemediateWritesPatches(t *testing.T) {
	out := t.TempDir()
	summary, err := runRemediate(context.Background(), &remediateOptions{
		reportPath: filepath.Join("testdata", "calibrate-soak", "report-000.json"),
		findingID:  "ARG-LOG-001",
		outDir:     out,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"checkout-logs-without-trace-context.alloy.river",
		"checkout-logs-without-trace-context.collector.yaml",
		"cart-logs-without-trace-context.alloy.river",
		"cart-logs-without-trace-context.collector.yaml",
	} {
		data, err := os.ReadFile(filepath.Join(out, f))
		if err != nil {
			t.Errorf("missing patch file %s: %v", f, err)
			continue
		}
		if !strings.Contains(string(data), "review before applying") {
			t.Errorf("%s missing human-review notice", f)
		}
	}
	if !strings.Contains(summary, "ARG-LOG-001") || !strings.Contains(summary, "2 service") {
		t.Errorf("summary = %q", summary)
	}
}

func TestRunRemediateScopedToService(t *testing.T) {
	out := t.TempDir()
	if _, err := runRemediate(context.Background(), &remediateOptions{
		reportPath: filepath.Join("testdata", "calibrate-soak", "report-000.json"),
		findingID:  "ARG-LOG-001",
		service:    "checkout",
		outDir:     out,
	}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(out)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "cart-") {
			t.Errorf("cart patch written despite --service checkout")
		}
	}
}

// --explain writes an LLM explanation file per finding and redacts by
// default — raw telemetry values never reach the endpoint.
func TestRunRemediateExplainWritesRedactedExplanation(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Logs on this service lack trace context; the patch injects it."}}]}`))
	}))
	defer srv.Close()

	out := t.TempDir()
	summary, err := runRemediate(context.Background(), &remediateOptions{
		reportPath:   filepath.Join("testdata", "calibrate-soak", "report-000.json"),
		findingID:    "ARG-LOG-001",
		service:      "checkout",
		outDir:       out,
		explain:      true,
		llmEndpoint:  srv.URL,
		llmModel:     "test-model",
		llmMaxTokens: 200,
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(out, "checkout-logs-without-trace-context.explanation.md"))
	if err != nil {
		t.Fatalf("no explanation file: %v", err)
	}
	if !strings.Contains(string(data), "trace context") || !strings.Contains(string(data), "never changes the patch") {
		t.Errorf("explanation = %q", data)
	}
	if !strings.Contains(summary, "explanation file") {
		t.Errorf("summary missing explanation note: %q", summary)
	}
	// redaction on by default: no raw evidence value in the request
	if strings.Contains(gotBody, "correlation") && strings.Contains(gotBody, "0.5") {
		t.Errorf("un-redacted evidence reached the endpoint:\n%s", gotBody)
	}
}

// --explain without an endpoint is a clear configuration error, not a panic.
func TestRunRemediateExplainRequiresEndpoint(t *testing.T) {
	_, err := runRemediate(context.Background(), &remediateOptions{
		reportPath: filepath.Join("testdata", "calibrate-soak", "report-000.json"),
		findingID:  "ARG-LOG-001",
		outDir:     t.TempDir(),
		explain:    true,
	})
	if err == nil || !strings.Contains(err.Error(), "llm-endpoint") {
		t.Errorf("want endpoint-required error, got %v", err)
	}
}

func TestRunRemediateUnknownFinding(t *testing.T) {
	_, err := runRemediate(context.Background(), &remediateOptions{
		reportPath: filepath.Join("testdata", "calibrate-soak", "report-000.json"),
		findingID:  "RES-005", // no such finding in the fixture report
		outDir:     t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "no findings") {
		t.Errorf("want no-findings error, got %v", err)
	}
}
