package main

import (
	"strings"
	"testing"
)

// The analyzer turns a soak output dir into the summary the calibrate
// discussion needs: memory verdict, rotation check, receiver errors, and
// threshold-relevant distributions.
func TestAnalyzeSoakDir(t *testing.T) {
	out, err := analyze("testdata/soakdir")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		// memory: fixture RSS grows 52MB -> ~55MB (~6%), within flat bounds
		"memory: flat",
		// pairs_tracked drops 380 -> 190 mid-run: rotation visible
		"window rotation: observed",
		// no engine-errors.log in fixture
		"receiver errors: none recorded",
		// items/sec: (241000+482000+120500-1000-2000-500)/7200s ≈ 116.7
		"items/sec (avg): 116.7",
		// span-name distribution across 3 services, max is cart's 180
		"span_name_cardinality", "max 180",
		// metric attribute cardinality max 950
		"metric_attribute_cardinality", "max 950",
		// resource attr: service.version max 5
		"service.version", "max 5",
		// exemplar coverage: 1 of 2 services has exemplars
		"exemplar coverage: 1/2 services",
		// trace health ratios surfaced
		"orphan_ratio", "missing_root_ratio",
		// per-rule ratios from reports: ARG-LOG-001 across 2 services
		"ARG-LOG-001", "2 services",
		// last fleet score echoed
		"fleet score (last): 84.7",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestAnalyzeMissingDir(t *testing.T) {
	if _, err := analyze("testdata/nope"); err == nil {
		t.Error("want error for missing dir")
	}
}
