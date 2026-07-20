package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
)

const itbenchRaw = `{
  "id": 102, "category": "sre", "complexity": "medium",
  "description": "The ad service cannot run due to a namespace resource quota.",
  "alerts": ["KubePodNotReady"],
  "environment": {"applications": [{"id": "opentelemetry-demo"}]},
  "disruptions": [{"injections": [{
     "id": "insufficient-kubernetes-resource-quota",
     "args": {"kubernetesObject": {"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"ad","namespace":"otel-demo"}}}
  }]}]
}`

// A scenario with no derivable ground truth.
const itbenchNoEntity = `{
  "id": 7, "category": "sre", "complexity": "low",
  "environment": {"applications": [{"id": "opentelemetry-demo"}]},
  "disruptions": [{"injections": [{"id": "mystery-fault", "args": {}}]}]
}`

func writeITBench(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestBenchImportITBench_WritesLoadableScenario(t *testing.T) {
	in := writeITBench(t, map[string]string{"102.json": itbenchRaw})
	out := filepath.Join(t.TempDir(), "imported")

	var stdout bytes.Buffer
	root := newRootCmd()
	root.SetOut(&stdout)
	root.SetArgs([]string{"bench", "import-itbench", "--in", in, "--out", out})
	if err := root.Execute(); err != nil {
		t.Fatalf("import: %v", err)
	}

	path := filepath.Join(out, "itbench-sre-102.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected scenario file: %v", err)
	}
	// The header must state the score-only constraint.
	if !strings.Contains(string(raw), "SCORE-ONLY") || !strings.Contains(string(raw), "--inject=none") {
		t.Errorf("header does not state the score-only constraint:\n%s", raw)
	}

	// It must survive the strict Argus loader.
	sc, err := bench.LoadScenario(path)
	if err != nil {
		t.Fatalf("imported scenario fails the strict loader: %v", err)
	}
	if sc.Metadata.Source != "itbench:sre/102" {
		t.Errorf("provenance lost: %q", sc.Metadata.Source)
	}
	if len(sc.Spec.GroundTruth.RootCauseEntities) != 1 ||
		sc.Spec.GroundTruth.RootCauseEntities[0].Name != "ad" {
		t.Errorf("ground truth = %+v", sc.Spec.GroundTruth.RootCauseEntities)
	}
	if !strings.Contains(stdout.String(), "imported 1") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestBenchImportITBench_FailsOnUnderivableGroundTruth(t *testing.T) {
	in := writeITBench(t, map[string]string{"7.json": itbenchNoEntity})
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"bench", "import-itbench", "--in", in, "--out", filepath.Join(t.TempDir(), "o")})
	if err := root.Execute(); err == nil {
		t.Fatal("expected failure rather than an empty answer key")
	}
}

func TestBenchImportITBench_SkipInvalidContinues(t *testing.T) {
	in := writeITBench(t, map[string]string{"102.json": itbenchRaw, "7.json": itbenchNoEntity})
	out := filepath.Join(t.TempDir(), "imported")

	var stdout, stderr bytes.Buffer
	root := newRootCmd()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"bench", "import-itbench", "--in", in, "--out", out, "--skip-invalid"})
	if err := root.Execute(); err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(stdout.String(), "imported 1") || !strings.Contains(stdout.String(), "1 skipped") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "skipped") {
		t.Errorf("skips should be reported on stderr: %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(out, "itbench-sre-7.yaml")); err == nil {
		t.Error("a skipped scenario must not be written")
	}
}

// An imported scenario must refuse to run under script injection, so nobody
// accidentally scores an un-faulted environment.
func TestImportedScenario_FailsUnderScriptInjection(t *testing.T) {
	in := writeITBench(t, map[string]string{"102.json": itbenchRaw})
	out := filepath.Join(t.TempDir(), "imported")
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"bench", "import-itbench", "--in", in, "--out", out})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	scenario := filepath.Join(out, "itbench-sre-102.yaml")
	var runOut bytes.Buffer
	run := newRootCmd()
	run.SetOut(&runOut)
	run.SetArgs([]string{
		"bench", "run", "--scenario", scenario,
		"--endpoint", "http://127.0.0.1:1", "--model", "m",
		"--mimir-url", "http://127.0.0.1:1",
		"--inject", "script",
	})
	if err := run.Execute(); err != nil {
		t.Fatalf("bench run returned a hard error: %v", err)
	}
	// The run is recorded as a failed injection, not a scored diagnosis.
	if !strings.Contains(runOut.String(), "inject") {
		t.Errorf("expected an injection failure in the report:\n%s", runOut.String())
	}
	if strings.Contains(runOut.String(), "| 0 | 1.00") {
		t.Error("an un-injected run must not produce a score")
	}
}
