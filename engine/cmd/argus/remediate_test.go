package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
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

// Service names come from telemetry, so they must not choose where a patch is
// written. A report whose service is named "../escaped" still writes inside
// the output directory.
func TestRunRemediateKeepsPatchesInsideOutDir(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "calibrate-soak", "report-000.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.json")
	hostile := strings.ReplaceAll(string(src), `"checkout"`, `"../escaped"`)
	if err := os.WriteFile(reportPath, []byte(hostile), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if _, err := runRemediate(context.Background(), &remediateOptions{
		reportPath: reportPath, findingID: "ARG-LOG-001", outDir: out,
	}); err != nil {
		t.Fatal(err)
	}
	if escaped, _ := filepath.Glob(filepath.Join(dir, "escaped*")); len(escaped) > 0 {
		t.Fatalf("a patch escaped the output directory: %v", escaped)
	}
	if found, _ := filepath.Glob(filepath.Join(out, ".._escaped-*-logs-without-trace-context.alloy.river")); len(found) != 1 {
		t.Errorf("the patch for the hostile name is not where it should be: %v", found)
	}
}

// A safe name is kept as it is. Any other is made safe and given a hash of
// the original, so two names that sanitize alike still get two files.
func TestFileSafe(t *testing.T) {
	for _, keep := range []string{"checkout", "cart.v2_x-y"} {
		if got := fileSafe(keep); got != keep {
			t.Errorf("fileSafe(%q) = %q, want it unchanged", keep, got)
		}
	}
	safe := regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	seen := map[string]string{}
	for _, in := range []string{"../../etc/x", `a/b\c`, "..", "", "svc with spcs", "a/b", "a_b", "a b"} {
		got := fileSafe(in)
		if !safe.MatchString(got) || strings.Trim(got, ".") == "" || strings.Contains(got, "/") {
			t.Errorf("fileSafe(%q) = %q, not a safe file-name component", in, got)
		}
		if other, dup := seen[got]; dup {
			t.Errorf("fileSafe(%q) and fileSafe(%q) are both %q: one patch would overwrite the other", in, other, got)
		}
		seen[got] = in
	}
}

// R07: with redaction on (the default) nothing from the service's telemetry
// reaches the endpoint, in any field: not the service name, and not the patch,
// which names the service in its comments and block labels. With
// --llm-no-redact it does, which shows the check can fail.
func TestRunRemediateExplainSendsNoTelemetry(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "calibrate-soak", "report-000.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.json")
	if err := os.WriteFile(reportPath, []byte(strings.ReplaceAll(string(src), `"checkout"`, `"zz-svc-marker"`)), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, noRedact := range []bool{false, true} {
		var body string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"explained"}}]}`))
		}))
		_, err := runRemediate(context.Background(), &remediateOptions{
			reportPath: reportPath, findingID: "ARG-LOG-001", service: "zz-svc-marker",
			outDir: filepath.Join(dir, fmt.Sprint(noRedact)), explain: true,
			llmEndpoint: srv.URL, llmModel: "m", llmMaxTokens: 100, llmNoRedact: noRedact,
		})
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		leaked := strings.Contains(body, "zz-svc-marker") || strings.Contains(body, "zz_svc_marker")
		if leaked != noRedact {
			t.Errorf("no-redact=%v: service name in the request = %v\n%s", noRedact, leaked, body)
		}
	}
}
