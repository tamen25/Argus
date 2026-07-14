// Package report renders score snapshots as JSON and Markdown. Honest
// reporting (architecture rule 7): every report carries the spec version,
// confidence markers, and the incomplete-rule-set disclosure the spec
// mandates while Argus implements a subset of official rules.
package report

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tamen25/Argus/engine/internal/rules"
)

// Report is the rendering envelope around a snapshot.
type Report struct {
	GeneratedAt  time.Time `json:"generated_at"`
	ArgusVersion string    `json:"argus_version"`
	SpecVersion  string    `json:"spec_version"`
	Window       string    `json:"window"`
	// RuleSetComplete is false while Argus implements a subset of the spec's
	// official rules; the spec requires disclosing that scores may differ
	// from a complete implementation.
	RuleSetComplete bool            `json:"rule_set_complete"`
	Notes           []string        `json:"notes,omitempty"`
	Snapshot        *rules.Snapshot `json:"snapshot"`
}

// JSON renders the report as indented JSON, with per-rule finding counts so
// one noisy rule is countable at a glance.
func JSON(r *Report) ([]byte, error) {
	return json.MarshalIndent(struct {
		*Report
		FindingCounts map[string]int `json:"finding_counts,omitempty"`
	}{r, findingCounts(r)}, "", "  ")
}

// findingCounts maps rule ID to the number of services it fired on.
func findingCounts(r *Report) map[string]int {
	counts := map[string]int{}
	for _, s := range r.Snapshot.Services {
		for _, f := range s.Findings {
			counts[f.RuleID]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

// Markdown renders the report for humans (and the showback-style artifacts
// later phases build on).
func Markdown(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Argus Instrumentation Score Report\n\n")
	fmt.Fprintf(&b, "- Generated: %s · argus %s · window %s\n", r.GeneratedAt.Format(time.RFC3339), r.ArgusVersion, r.Window)
	fmt.Fprintf(&b, "- Instrumentation Score spec %s\n", r.SpecVersion)
	fmt.Fprintf(&b, "- **Fleet score: %.1f** (%s)\n\n", r.Snapshot.FleetScore, rules.Category(r.Snapshot.FleetScore))
	if !r.RuleSetComplete {
		fmt.Fprintf(&b, "> ⚠️ This implementation does not yet implement the full rule set of the "+
			"Instrumentation Score specification (rules evaluated: %s). Scores may differ from a "+
			"complete implementation.\n\n", strings.Join(r.Snapshot.RulesEvaluated, ", "))
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "> Note: %s\n\n", n)
	}

	fmt.Fprintf(&b, "## Services\n\n")
	fmt.Fprintf(&b, "| Service | Score | Category | Extension | Findings |\n|---|---:|---|---:|---:|\n")
	for _, s := range r.Snapshot.Services {
		ext := "—"
		if s.ExtensionScore != nil {
			ext = fmt.Sprintf("%.1f", *s.ExtensionScore)
		}
		fmt.Fprintf(&b, "| %s | %.1f | %s | %s | %d |\n", s.ServiceName, s.SpecScore, s.Category, ext, len(s.Findings))
	}

	// Findings grouped by rule: one noisy rule reads as one section with a
	// count, not one section per service.
	var order []string
	grouped := map[string][]rules.Finding{}
	for _, s := range r.Snapshot.Services {
		for _, f := range s.Findings {
			if _, seen := grouped[f.RuleID]; !seen {
				order = append(order, f.RuleID)
			}
			grouped[f.RuleID] = append(grouped[f.RuleID], f)
		}
	}
	sort.Strings(order)
	if len(order) > 0 {
		fmt.Fprintf(&b, "\n## Findings\n")
		for _, id := range order {
			fs := grouped[id]
			f0 := fs[0]
			noun := "services"
			if len(fs) == 1 {
				noun = "service"
			}
			fmt.Fprintf(&b, "\n### %s (`%s`) — %d %s\n\n", f0.RuleName, f0.RuleID, len(fs), noun)
			fmt.Fprintf(&b, "- impact: **%s** · source: %s\n", f0.Impact, f0.Source)
			fmt.Fprintf(&b, "- %s\n", f0.Description)
			for _, f := range fs {
				fmt.Fprintf(&b, "- **%s** — confidence: **%s** · observed: %d · violations: %d (%.0f%%)\n",
					f.Service, f.Confidence, f.Stats.Observed, f.Stats.Violations, f.Stats.Ratio*100)
				for _, e := range f.Evidence {
					fmt.Fprintf(&b, "  - evidence (%s): %s\n", e.Kind, e.Summary)
				}
			}
		}
	}
	return b.String()
}
