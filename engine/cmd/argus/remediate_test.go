package main

import (
	"context"
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
