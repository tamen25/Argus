// Package itbench converts ITBench scenario definitions into Argus bench
// scenarios, so results can be compared against published ITBench baselines.
//
// Scope, stated plainly: ITBench executes its faults with its own tooling,
// against a fixed catalogue of fault ids. Argus cannot reproduce those
// injections, and pretending otherwise would make the comparability claim
// false. So an imported scenario is SCORE-ONLY: you stage the environment with
// ITBench, then run `argus bench run --inject=none` to measure an agent against
// the derived ground truth. The emitted inject step names a script Argus
// deliberately cannot execute, so running it any other way fails loudly.
//
// Upstream schema: schemas/json/library/index/scenario.json in
// github.com/itbench-hub/ITBench (Apache-2.0).
package itbench

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/tamen25/Argus/engine/internal/bench"
)

// ExternalFaultScript is the placeholder script name emitted for an imported
// disruption. It is intentionally not executable: an imported scenario must be
// run with --inject=none, with ITBench staging the environment.
const ExternalFaultScript = "itbench-external-fault-not-executable-by-argus"

// DefaultDuration is used for imported inject steps: ITBench scenario
// definitions carry no duration.
const DefaultDuration = "10m"

// Scenario is the subset of an ITBench scenario index we consume. Decoding is
// deliberately permissive about unknown fields — the upstream catalogue evolves,
// and the checks that matter here are explicit.
type Scenario struct {
	ID          int          `json:"id"`
	Category    string       `json:"category"`
	Complexity  string       `json:"complexity"`
	Description string       `json:"description"`
	Alerts      []string     `json:"alerts"`
	Platforms   []string     `json:"platforms"`
	Tags        []string     `json:"tags"`
	Environment Environment  `json:"environment"`
	Disruptions []Disruption `json:"disruptions"`
}

// Environment names the applications the scenario runs against.
type Environment struct {
	Applications []Application `json:"applications"`
}

// Application is one target workload (e.g. opentelemetry-demo).
type Application struct {
	ID string `json:"id"`
}

// Disruption is one ordered fault group.
type Disruption struct {
	Injections []Injection `json:"injections"`
	// WaitFor holds operational waiters (restarts, scales). These are NOT the
	// fault and are never treated as ground truth.
	WaitFor *WaitFor `json:"waitFor,omitempty"`
}

// WaitFor holds pre/post-injection waiters.
type WaitFor struct {
	PreInjection  []Injection `json:"preInjection,omitempty"`
	PostInjection []Injection `json:"postInjection,omitempty"`
}

// Injection is one fault (or waiter) with its arguments.
type Injection struct {
	ID   string `json:"id"`
	Args Args   `json:"args"`
}

// Args carries the fault arguments; only the Kubernetes object is consumed.
type Args struct {
	KubernetesObject *K8sObject `json:"kubernetesObject,omitempty"`
}

// K8sObject identifies the object a fault was applied to — the root cause.
type K8sObject struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
}

// Convert turns one ITBench scenario definition into an Argus scenario.
//
// Ground truth is derived from each injection's args.kubernetesObject — the
// object the fault was actually applied to. Waiter objects are excluded: a
// restarted workload is collateral, not the root cause. If no injection carries
// a Kubernetes object, Convert FAILS rather than emitting a scenario with empty
// ground truth, which would score every agent answer wrong.
func Convert(raw []byte) (bench.Scenario, error) {
	var s Scenario
	if err := json.Unmarshal(raw, &s); err != nil {
		return bench.Scenario{}, fmt.Errorf("parsing ITBench scenario: %w", err)
	}
	if s.ID <= 0 {
		return bench.Scenario{}, fmt.Errorf("ITBench scenario has no id")
	}
	if s.Category != "sre" {
		return bench.Scenario{}, fmt.Errorf("ITBench scenario %d: category %q is not an SRE diagnosis scenario", s.ID, s.Category)
	}
	if len(s.Environment.Applications) == 0 || s.Environment.Applications[0].ID == "" {
		return bench.Scenario{}, fmt.Errorf("ITBench scenario %d: no environment application", s.ID)
	}
	if len(s.Disruptions) == 0 {
		return bench.Scenario{}, fmt.Errorf("ITBench scenario %d: no disruptions", s.ID)
	}

	entities, faultIDs := groundTruth(s)
	if len(entities) == 0 {
		return bench.Scenario{}, fmt.Errorf(
			"ITBench scenario %d: no injection carries args.kubernetesObject, so root-cause ground truth cannot be derived; refusing to emit a scenario that would score every answer wrong", s.ID)
	}

	steps := make([]bench.InjectStep, 0, len(s.Disruptions))
	for range s.Disruptions {
		steps = append(steps, bench.InjectStep{
			Type:     bench.InjectScript,
			Script:   ExternalFaultScript,
			Duration: DefaultDuration,
		})
	}

	return bench.Scenario{
		APIVersion: bench.APIVersion,
		Kind:       bench.Kind,
		Metadata: bench.Metadata{
			Name:        Name(s.ID),
			Description: s.Description,
			Source:      fmt.Sprintf("itbench:sre/%d", s.ID),
		},
		Spec: bench.ScenarioSpec{
			Environment: bench.Environment{App: s.Environment.Applications[0].ID},
			Inject:      steps,
			GroundTruth: bench.GroundTruth{
				RootCauseEntities: entities,
				Category:          strings.Join(faultIDs, "+"),
			},
			Scoring: bench.ScoringSpec{EntityMatch: bench.MatchJaccard, PartialCredit: true},
		},
	}, nil
}

// Name is the Argus scenario name for an ITBench SRE scenario id.
func Name(id int) string { return fmt.Sprintf("itbench-sre-%d", id) }

// groundTruth collects the faulted entities and the distinct fault ids, both
// deterministically ordered. Only injections are considered — never waiters.
func groundTruth(s Scenario) ([]bench.Entity, []string) {
	seenEntity := map[string]bool{}
	seenFault := map[string]bool{}
	var entities []bench.Entity
	var faults []string

	for _, d := range s.Disruptions {
		for _, inj := range d.Injections {
			if inj.ID != "" && !seenFault[inj.ID] {
				seenFault[inj.ID] = true
				faults = append(faults, inj.ID)
			}
			ko := inj.Args.KubernetesObject
			if ko == nil || ko.Kind == "" || ko.Metadata.Name == "" {
				continue
			}
			e := bench.Entity{Kind: ko.Kind, Namespace: ko.Metadata.Namespace, Name: ko.Metadata.Name}
			key := e.Kind + "/" + e.Namespace + "/" + e.Name
			if seenEntity[key] {
				continue
			}
			seenEntity[key] = true
			entities = append(entities, e)
		}
	}

	sort.Slice(entities, func(i, j int) bool {
		a, b := entities[i], entities[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	sort.Strings(faults)
	return entities, faults
}

// ConvertFile reads and converts a single ITBench scenario file.
func ConvertFile(path string) (bench.Scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return bench.Scenario{}, err
	}
	sc, err := Convert(b)
	if err != nil {
		return bench.Scenario{}, fmt.Errorf("%s: %w", path, err)
	}
	return sc, nil
}
