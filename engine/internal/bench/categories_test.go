package bench_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
)

const validCategories = `apiVersion: argus/v1alpha1
kind: BenchCategories
categories:
  - {name: oomkill, description: A container is killed for exceeding its memory limit.}
  - {name: deploy-regression, description: A rollout misbehaves.}
  - {name: network-partition, description: Traffic is blocked.}
  - {name: cardinality-explosion, description: Series grow without limit.}
  - {name: dependency-latency, description: A dependency is slow.}
`

func writeCategories(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "categories.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadCategories_Valid(t *testing.T) {
	c, err := bench.LoadCategories(writeCategories(t, validCategories))
	if err != nil {
		t.Fatal(err)
	}
	// Agents always see the list sorted, whatever order the file is in, so
	// position never hints at a scenario.
	want := "cardinality-explosion,dependency-latency,deploy-regression,network-partition,oomkill"
	if got := strings.Join(c.Names(), ","); got != want {
		t.Errorf("names = %s, want %s", got, want)
	}
	if !c.Has("OOMKill") || !c.Has(" oomkill ") {
		t.Error("Has must match the way a diagnosis is scored: case-insensitive, trimmed")
	}
	if c.Has("performance-degradation") {
		t.Error("Has reports a category that is not listed")
	}
}

func TestLoadCategories_Rejects(t *testing.T) {
	cases := []struct {
		name, body, wantErr string
	}{
		{"too few to be a vocabulary", `apiVersion: argus/v1alpha1
kind: BenchCategories
categories:
  - {name: oomkill, description: x}
  - {name: deploy-regression, description: x}
`, "gives the answer away"},
		{"listed twice", strings.Replace(validCategories, "name: dependency-latency", "name: oomkill", 1), "listed twice"},
		{"not a slug", strings.Replace(validCategories, "name: oomkill", "name: OOM Kill", 1), "not a lowercase slug"},
		{"no description", strings.Replace(validCategories, "description: A rollout misbehaves.", `description: ""`, 1), "has no description"},
		{"wrong kind", strings.Replace(validCategories, "BenchCategories", "BenchScenario", 1), `kind "BenchScenario"`},
		{"wrong apiVersion", strings.Replace(validCategories, "argus/v1alpha1", "argus/v2", 1), `apiVersion "argus/v2"`},
		{"unknown key", validCategories + "extra: true\n", "field extra not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := bench.LoadCategories(writeCategories(t, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
	if _, err := bench.LoadCategories(filepath.Join(t.TempDir(), "absent.yaml")); !os.IsNotExist(err) {
		t.Errorf("missing file: err = %v, want a not-exist error the caller can recognize", err)
	}
}

func TestCategories_CheckRefusesAScenarioThatCannotBeAnswered(t *testing.T) {
	c, err := bench.LoadCategories(writeCategories(t, validCategories))
	if err != nil {
		t.Fatal(err)
	}
	sc := bench.Scenario{}
	sc.Metadata.Name = "s"
	sc.Spec.GroundTruth.Category = "oomkill"
	if err := c.Check(sc); err != nil {
		t.Errorf("a listed category was refused: %v", err)
	}
	sc.Spec.GroundTruth.Category = "disk-pressure"
	if err := c.Check(sc); err == nil || !strings.Contains(err.Error(), `"disk-pressure" is not in the category list`) {
		t.Errorf("err = %v, want the unlisted category refused", err)
	}
}

func TestAddCategories_MergesAndSorts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "categories.yaml")
	n, err := bench.AddCategories(p, []bench.Category{{Name: "zeta", Description: "z"}, {Name: "alpha", Description: "a"}})
	if err != nil || n != 2 {
		t.Fatalf("first add: n=%d err=%v", n, err)
	}
	// A second import adds new names and keeps what is there, without duplicates.
	n, err = bench.AddCategories(p, []bench.Category{{Name: "alpha", Description: "changed"}, {Name: "mid", Description: "m"}})
	if err != nil || n != 3 {
		t.Fatalf("second add: n=%d err=%v", n, err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if a, m, z := strings.Index(body, "alpha"), strings.Index(body, "mid"), strings.Index(body, "zeta"); a < 0 || a > m || m > z {
		t.Errorf("file is not sorted by name:\n%s", body)
	}
	if strings.Contains(body, "changed") {
		t.Errorf("an existing category's description was overwritten:\n%s", body)
	}
	// Three is below the minimum: the file exists, and loading it for a run says why it is not enough.
	if _, err := bench.LoadCategories(p); err == nil || !strings.Contains(err.Error(), "at least 5") {
		t.Errorf("err = %v, want the short list refused for a run", err)
	}
}

// The shipped list must contain every shipped scenario's category, or that
// scenario can never be answered correctly.
func TestShippedCategoriesCoverEveryScenario(t *testing.T) {
	c, err := bench.LoadCategories(filepath.Join(scenarioDir, "categories.yaml"))
	if err != nil {
		t.Fatalf("shipped category list does not load: %v", err)
	}
	used := map[string]bool{}
	for _, p := range shippedScenarios(t) {
		sc, err := bench.LoadScenario(p)
		if err != nil {
			continue // covered by TestShippedScenariosLoad
		}
		if err := c.Check(sc); err != nil {
			t.Error(err)
		}
		used[sc.Spec.GroundTruth.Category] = true
	}
	// Distractors are the point of a closed list: if every listed category had a
	// scenario, an agent could rule categories out by counting.
	if len(c.Categories) <= len(used) {
		t.Errorf("%d categories listed for %d in use: the list needs categories no scenario uses",
			len(c.Categories), len(used))
	}
}

// A description explains a kind of fault. One that names a workload from a
// scenario would hand every agent a clue about that scenario.
func TestShippedCategoryDescriptionsNameNoScenarioEntity(t *testing.T) {
	c, err := bench.LoadCategories(filepath.Join(scenarioDir, "categories.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	entities := map[string]bool{}
	for _, p := range shippedScenarios(t) {
		sc, err := bench.LoadScenario(p)
		if err != nil {
			continue
		}
		for _, e := range append(append([]bench.Entity{}, sc.Spec.GroundTruth.RootCauseEntities...), sc.Spec.GroundTruth.Decoys...) {
			entities[e.Name] = true
		}
	}
	for _, cat := range c.Categories {
		for name := range entities {
			if regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(name) + `\b`).MatchString(cat.Description) {
				t.Errorf("category %q: description mentions the scenario entity %q", cat.Name, name)
			}
		}
	}
}
