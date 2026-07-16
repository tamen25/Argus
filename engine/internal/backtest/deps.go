package backtest

import (
	"sort"
	"strings"

	"github.com/prometheus/prometheus/promql/parser"
)

// DepKind classifies a series an expression reads, for fidelity failure mode
// (a): recording rules that did not run historically cannot be replayed
// directly.
type DepKind int

const (
	// DepPlainSeries is a scraped/ingested series — replayable as-is.
	DepPlainSeries DepKind = iota
	// DepDefinedRecording is a recording rule defined in the loaded set —
	// synthesizable by evaluating its expression inline (with caveats).
	DepDefinedRecording
	// DepExternalRecording looks like a recording rule (colon-form name) but
	// no loaded file defines it — replay must flag it, not guess.
	DepExternalRecording
)

// Dependency is one series an expression reads and how replay can treat it.
type Dependency struct {
	Series string
	Kind   DepKind
}

// Dependencies maps every rule name to the series its expression reads,
// classified. Rules whose expressions fail to parse are absent (the strict
// loader should have rejected them already).
func Dependencies(rs RuleSet) map[string][]Dependency {
	defined := map[string]bool{}
	for _, g := range rs.Groups {
		for _, r := range g.Rules {
			if !r.Alert {
				defined[r.Name] = true
			}
		}
	}

	out := map[string][]Dependency{}
	promql := parser.NewParser(parser.Options{})
	for _, g := range rs.Groups {
		for _, r := range g.Rules {
			expr, err := promql.ParseExpr(r.Expr)
			if err != nil {
				continue
			}
			seen := map[string]bool{}
			var deps []Dependency
			parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
				vs, ok := node.(*parser.VectorSelector)
				if !ok || vs.Name == "" || seen[vs.Name] {
					return nil
				}
				seen[vs.Name] = true
				deps = append(deps, Dependency{Series: vs.Name, Kind: classify(vs.Name, defined)})
				return nil
			})
			sort.Slice(deps, func(i, j int) bool { return deps[i].Series < deps[j].Series })
			out[r.Name] = deps
		}
	}
	return out
}

// classify: defined in the loaded set beats the naming heuristic; a colon in
// the name is the Prometheus recording-rule convention (level:metric:ops).
func classify(name string, defined map[string]bool) DepKind {
	if defined[name] {
		return DepDefinedRecording
	}
	if strings.Contains(name, ":") {
		return DepExternalRecording
	}
	return DepPlainSeries
}
