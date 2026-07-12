// Package report renders score snapshots as JSON and Markdown. Honest
// reporting (architecture rule 7): every report carries the spec version,
// confidence markers, and the incomplete-rule-set disclosure the spec
// mandates while Argus implements a subset of official rules.
package report

import (
	"encoding/json"
	"fmt"
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

// JSON renders the report as indented JSON.
func JSON(r *Report) ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
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

	var findings []rules.Finding
	for _, s := range r.Snapshot.Services {
		findings = append(findings, s.Findings...)
	}
	if len(findings) > 0 {
		fmt.Fprintf(&b, "\n## Findings\n")
		for _, f := range findings {
			fmt.Fprintf(&b, "\n### %s — %s (`%s`)\n\n", f.Service, f.RuleName, f.RuleID)
			fmt.Fprintf(&b, "- impact: **%s** · source: %s · confidence: **%s**\n", f.Impact, f.Source, f.Confidence)
			fmt.Fprintf(&b, "- observed: %d · violations: %d (%.0f%%)\n", f.Stats.Observed, f.Stats.Violations, f.Stats.Ratio*100)
			fmt.Fprintf(&b, "- %s\n", f.Description)
			for _, e := range f.Evidence {
				fmt.Fprintf(&b, "  - evidence (%s): %s\n", e.Kind, e.Summary)
			}
		}
	}
	return b.String()
}
