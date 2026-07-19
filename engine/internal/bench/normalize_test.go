package bench

import (
	"context"
	"testing"
)

const goodDiagnosis = `{
  "scenario": "ignored-overridden",
  "root_cause_entities": [{"kind": "Deployment", "namespace": "otel-demo", "name": "checkout"}],
  "category": "cardinality-explosion",
  "summary": "active series spiked on checkout",
  "confidence": 0.8
}`

func TestJSONNormalizer_ForcesScenarioAndValidates(t *testing.T) {
	d, err := JSONNormalizer{}.Normalize(context.Background(), []byte(goodDiagnosis), "the-real-name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Scenario != "the-real-name" {
		t.Errorf("scenario = %q, want authoritative override", d.Scenario)
	}
	if len(d.RootCauseEntities) != 1 || d.RootCauseEntities[0].Name != "checkout" {
		t.Errorf("entities = %+v", d.RootCauseEntities)
	}
}

func TestParseDiagnosis_Strict(t *testing.T) {
	cases := map[string]string{
		"unknown field":  `{"scenario":"s","root_cause_entities":[{"kind":"Pod","name":"p"}],"category":"c","bogus":1}`,
		"trailing data":  `{"scenario":"s","root_cause_entities":[{"kind":"Pod","name":"p"}],"category":"c"} extra`,
		"malformed json": `{"scenario":`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDiagnosis([]byte(raw)); err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

func TestDiagnosis_Validate(t *testing.T) {
	base := func() Diagnosis {
		return Diagnosis{
			Scenario:          "s",
			Category:          "c",
			RootCauseEntities: []Entity{{Kind: "Pod", Name: "p"}},
			Confidence:        0.5,
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("valid diagnosis rejected: %v", err)
	}

	bad := map[string]func(*Diagnosis){
		"empty scenario":   func(d *Diagnosis) { d.Scenario = "" },
		"empty category":   func(d *Diagnosis) { d.Category = "" },
		"no entities":      func(d *Diagnosis) { d.RootCauseEntities = nil },
		"entity no name":   func(d *Diagnosis) { d.RootCauseEntities = []Entity{{Kind: "Pod"}} },
		"confidence high":  func(d *Diagnosis) { d.Confidence = 1.5 },
		"confidence below": func(d *Diagnosis) { d.Confidence = -0.1 },
	}
	for name, mut := range bad {
		t.Run(name, func(t *testing.T) {
			d := base()
			mut(&d)
			if err := d.Validate(); err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

func TestJSONNormalizer_Method(t *testing.T) {
	if m := (JSONNormalizer{}).Method(); m != "json" {
		t.Errorf("Method() = %q, want json", m)
	}
}
