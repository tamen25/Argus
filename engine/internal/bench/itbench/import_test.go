package itbench

import (
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
)

func TestConvert_RealScenario102(t *testing.T) {
	sc, err := ConvertFile("testdata/itbench-102.json")
	if err != nil {
		t.Fatalf("ConvertFile: %v", err)
	}
	if sc.Metadata.Name != "itbench-sre-102" {
		t.Errorf("name = %q", sc.Metadata.Name)
	}
	if sc.Metadata.Source != "itbench:sre/102" {
		t.Errorf("source = %q, want provenance", sc.Metadata.Source)
	}
	if !strings.Contains(sc.Metadata.Description, "resource quota") {
		t.Errorf("description not carried through: %q", sc.Metadata.Description)
	}
	if sc.Spec.Environment.App != "opentelemetry-demo" {
		t.Errorf("app = %q", sc.Spec.Environment.App)
	}
	if sc.Spec.GroundTruth.Category != "insufficient-kubernetes-resource-quota" {
		t.Errorf("category = %q", sc.Spec.GroundTruth.Category)
	}
	want := []bench.Entity{{Kind: "Deployment", Namespace: "otel-demo", Name: "ad"}}
	if len(sc.Spec.GroundTruth.RootCauseEntities) != 1 || sc.Spec.GroundTruth.RootCauseEntities[0] != want[0] {
		t.Errorf("entities = %+v, want %+v", sc.Spec.GroundTruth.RootCauseEntities, want)
	}
}

// Scenario 1 injects into a ConfigMap while its waitFor block restarts two
// Deployments. The restarted workloads are collateral, never ground truth.
func TestConvert_WaiterObjectsAreNotGroundTruth(t *testing.T) {
	sc, err := ConvertFile("testdata/itbench-1.json")
	if err != nil {
		t.Fatalf("ConvertFile: %v", err)
	}
	ents := sc.Spec.GroundTruth.RootCauseEntities
	if len(ents) != 1 {
		t.Fatalf("entities = %+v, want exactly the injected ConfigMap", ents)
	}
	got := ents[0]
	if got.Kind != "ConfigMap" || got.Name != "flagd-config" || got.Namespace != "otel-demo" {
		t.Errorf("entity = %+v, want ConfigMap/otel-demo/flagd-config", got)
	}
	for _, e := range ents {
		if e.Name == "flagd" || e.Name == "load-generator" {
			t.Errorf("waiter workload %q leaked into ground truth", e.Name)
		}
	}
}

// Imported scenarios must still satisfy the strict Argus loader.
func TestConvert_ProducesValidArgusScenario(t *testing.T) {
	for _, f := range []string{"testdata/itbench-1.json", "testdata/itbench-102.json"} {
		sc, err := ConvertFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if sc.APIVersion != bench.APIVersion || sc.Kind != bench.Kind {
			t.Errorf("%s: bad envelope %s/%s", f, sc.APIVersion, sc.Kind)
		}
		if len(sc.Spec.Inject) == 0 {
			t.Errorf("%s: no inject steps", f)
		}
		for _, step := range sc.Spec.Inject {
			if step.Script != ExternalFaultScript {
				t.Errorf("%s: inject script = %q, want the non-executable placeholder", f, step.Script)
			}
			if _, err := step.Dur(); err != nil {
				t.Errorf("%s: bad duration: %v", f, err)
			}
		}
	}
}

func TestConvert_RefusesWhenGroundTruthUnderivable(t *testing.T) {
	raw := []byte(`{
	  "id": 7, "category": "sre", "complexity": "low",
	  "environment": {"applications": [{"id": "opentelemetry-demo"}]},
	  "disruptions": [{"injections": [{"id": "some-fault", "args": {}}]}]
	}`)
	_, err := Convert(raw)
	if err == nil {
		t.Fatal("expected refusal when no kubernetesObject is present")
	}
	if !strings.Contains(err.Error(), "score every answer wrong") {
		t.Errorf("error should explain why emitting would be harmful: %v", err)
	}
}

func TestConvert_RejectsNonSRE(t *testing.T) {
	raw := []byte(`{
	  "id": 9, "category": "finops", "complexity": "low",
	  "environment": {"applications": [{"id": "opentelemetry-demo"}]},
	  "disruptions": [{"injections": [{"id": "x", "args": {"kubernetesObject": {"kind":"Deployment","metadata":{"name":"a","namespace":"b"}}}}]}]
	}`)
	if _, err := Convert(raw); err == nil {
		t.Fatal("expected non-SRE category to be rejected")
	}
}

func TestConvert_RejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"not json":   `{`,
		"no id":      `{"category":"sre","environment":{"applications":[{"id":"a"}]},"disruptions":[{}]}`,
		"no app":     `{"id":1,"category":"sre","environment":{"applications":[]},"disruptions":[{}]}`,
		"no disrupt": `{"id":1,"category":"sre","environment":{"applications":[{"id":"a"}]},"disruptions":[]}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Convert([]byte(raw)); err == nil {
				t.Error("expected error")
			}
		})
	}
}

// Multiple distinct faults produce a deterministic joined category and a
// deduplicated, sorted entity set.
func TestConvert_MultipleInjectionsDeterministic(t *testing.T) {
	raw := []byte(`{
	  "id": 5, "category": "sre", "complexity": "high",
	  "environment": {"applications": [{"id": "opentelemetry-demo"}]},
	  "disruptions": [
	    {"injections": [
	       {"id": "zeta-fault", "args": {"kubernetesObject": {"kind":"Deployment","metadata":{"name":"b","namespace":"ns"}}}},
	       {"id": "alpha-fault", "args": {"kubernetesObject": {"kind":"Deployment","metadata":{"name":"a","namespace":"ns"}}}}
	    ]},
	    {"injections": [
	       {"id": "alpha-fault", "args": {"kubernetesObject": {"kind":"Deployment","metadata":{"name":"a","namespace":"ns"}}}}
	    ]}
	  ]
	}`)
	sc, err := Convert(raw)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Spec.GroundTruth.Category != "alpha-fault+zeta-fault" {
		t.Errorf("category = %q, want sorted join", sc.Spec.GroundTruth.Category)
	}
	ents := sc.Spec.GroundTruth.RootCauseEntities
	if len(ents) != 2 || ents[0].Name != "a" || ents[1].Name != "b" {
		t.Errorf("entities = %+v, want deduped and sorted", ents)
	}
	if len(sc.Spec.Inject) != 2 {
		t.Errorf("inject steps = %d, want one per disruption", len(sc.Spec.Inject))
	}
}
