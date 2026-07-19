package bench

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const validScenario = `apiVersion: argus/v1alpha1
kind: BenchScenario
metadata:
  name: cardinality-explosion-checkout
spec:
  environment:
    app: otel-demo
  inject:
    - type: chaosmesh
      manifest: faults/cardinality-explosion.yaml
      duration: 10m
  groundTruth:
    rootCauseEntities:
      - {kind: Deployment, namespace: otel-demo, name: checkout}
    category: cardinality-explosion
  scoring:
    entityMatch: jaccard
    partialCredit: true
`

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadScenario_Valid(t *testing.T) {
	s, err := LoadScenario(writeTemp(t, validScenario))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Metadata.Name != "cardinality-explosion-checkout" {
		t.Errorf("name = %q", s.Metadata.Name)
	}
	if got := len(s.Spec.Inject); got != 1 {
		t.Fatalf("inject steps = %d, want 1", got)
	}
	d, err := s.Spec.Inject[0].Dur()
	if err != nil {
		t.Fatalf("duration parse: %v", err)
	}
	if d != 10*time.Minute {
		t.Errorf("duration = %v, want 10m", d)
	}
	if got := len(s.Spec.GroundTruth.RootCauseEntities); got != 1 {
		t.Errorf("ground-truth entities = %d, want 1", got)
	}
}

func TestLoadScenario_Rejects(t *testing.T) {
	cases := map[string]string{
		"unknown field": validScenario + "extraTopLevel: nope\n",
		"wrong apiVersion": `apiVersion: argus/v1
kind: BenchScenario
metadata: {name: x}
spec:
  environment: {app: otel-demo}
  inject: [{type: script, script: s.sh, duration: 1m}]
  groundTruth: {rootCauseEntities: [{kind: Pod, name: p}], category: c}
`,
		"empty inject": `apiVersion: argus/v1alpha1
kind: BenchScenario
metadata: {name: x}
spec:
  environment: {app: otel-demo}
  inject: []
  groundTruth: {rootCauseEntities: [{kind: Pod, name: p}], category: c}
`,
		"bad duration": `apiVersion: argus/v1alpha1
kind: BenchScenario
metadata: {name: x}
spec:
  environment: {app: otel-demo}
  inject: [{type: script, script: s.sh, duration: 10minutes}]
  groundTruth: {rootCauseEntities: [{kind: Pod, name: p}], category: c}
`,
		"script step with manifest": `apiVersion: argus/v1alpha1
kind: BenchScenario
metadata: {name: x}
spec:
  environment: {app: otel-demo}
  inject: [{type: script, script: s.sh, manifest: m.yaml, duration: 1m}]
  groundTruth: {rootCauseEntities: [{kind: Pod, name: p}], category: c}
`,
		"empty ground truth": `apiVersion: argus/v1alpha1
kind: BenchScenario
metadata: {name: x}
spec:
  environment: {app: otel-demo}
  inject: [{type: script, script: s.sh, duration: 1m}]
  groundTruth: {rootCauseEntities: [], category: c}
`,
		"bad entityMatch": `apiVersion: argus/v1alpha1
kind: BenchScenario
metadata: {name: x}
spec:
  environment: {app: otel-demo}
  inject: [{type: script, script: s.sh, duration: 1m}]
  groundTruth: {rootCauseEntities: [{kind: Pod, name: p}], category: c}
  scoring: {entityMatch: fuzzy}
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadScenario(writeTemp(t, body)); err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}
