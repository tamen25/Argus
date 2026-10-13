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

// A malformed citation is not counted; it must not fail the diagnosis. This is
// gpt-oss's first clean flagship submission (right category, citations in its
// own shape), which strict decoding rejected as "no diagnosis". Top-level
// strictness is unchanged (TestParseDiagnosis_Strict).
func TestParseDiagnosis_MalformedCitationsAreNotCountedNotFatal(t *testing.T) {
	raw := `{"category":"broken-trace-propagation","confidence":0.45,"evidence":[` +
		`{"id":"HighSpanErrorRatio","service":"checkout","type":"alert"},` +
		`{"query":"service:span_errors:ratio_rate5m","type":"metric_query","values":[["t",0.08]]},` +
		`"a bare string",` +
		`{"signal":"traces","query":"{status=error}","observation":"no error spans","extra":1}],` +
		`"root_cause_entities":[{"kind":"Deployment","name":"checkout","namespace":"otel-demo"}],"summary":"s"}`
	d, err := JSONNormalizer{}.Normalize(context.Background(), []byte(raw), "broken-trace-propagation-payment")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if d.Category != "broken-trace-propagation" || len(d.Evidence) != 4 {
		t.Errorf("category = %q, evidence items = %d; want the diagnosis kept with all 4 items", d.Category, len(d.Evidence))
	}
	// Only the item in the schema's shape counts, extra field and all.
	if wf := d.WellFormedEvidence(); len(wf) != 1 || wf[0].Signal != "traces" {
		t.Errorf("well-formed evidence = %+v, want only the traces citation", wf)
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
