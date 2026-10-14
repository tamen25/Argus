// Package backtest replays historical Mimir data against Prometheus/Mimir
// alert rules and SLO burn-rate policies (module B), and scores the firings
// against the incident registry. The pipeline, in Run:
//
//   - Segments maps where telemetry exists in the window;
//   - Synthesize inlines recording rules that never ran over that history;
//   - Replay steps each alerting rule through every segment, rebuilding its
//     for: state;
//   - Score judges the firings against the labeled incidents.
//
// DiffReports compares two rule sets for the CI gate. What replay cannot
// reproduce is listed in docs/backtest-fidelity.md, and every report carries
// those caveats.
//
// Deterministic core: never imports the LLM client (depguard, architecture
// rule 2). Backends sit behind the EvalQuerier and InstantQuerier interfaces
// defined here (architecture rule 1); concrete Mimir clients live in adapter
// packages.
package backtest

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/rulefmt"
	"github.com/prometheus/prometheus/promql/parser"
)

// RuleSet is the loaded rule files in backtest's own model — thin on purpose
// so the prometheus library types don't leak through the package boundary.
type RuleSet struct {
	Groups []Group
}

// Group mirrors one ruler group: a name, an evaluation interval (the default
// replay step), and rules in file order.
type Group struct {
	Name     string
	Interval time.Duration
	Rules    []Rule
}

// Rule is one alerting or recording rule.
type Rule struct {
	Name  string // alert name, or the recording rule's series name
	Alert bool
	Expr  string
	For   time.Duration // alerts only
	// KeepFiringFor keeps a firing alert firing for this long after its
	// condition stops holding (alerts only).
	KeepFiringFor time.Duration
	Labels        map[string]string // static labels from the rule file
}

// LoadRuleFiles strictly parses Prometheus/Mimir ruler rule files (Sloth and
// Pyrra emit the same format; Mimir's ruler config API stores BARE group
// documents without the groups: wrapper — accepted too). Any parse or
// validation error fails the load — a backtest against a silently half-loaded
// rule set would report misses that are really loader bugs.
func LoadRuleFiles(paths ...string) (RuleSet, error) {
	var rs RuleSet
	for _, path := range paths {
		groups, err := parseRuleFile(path)
		if err != nil {
			return RuleSet{}, err
		}
		for _, g := range groups.Groups {
			grp := Group{Name: g.Name, Interval: time.Duration(g.Interval)}
			for _, r := range g.Rules {
				grp.Rules = append(grp.Rules, fromRulefmt(r))
			}
			rs.Groups = append(rs.Groups, grp)
		}
	}
	return rs, nil
}

// parseRuleFile parses one rule file with Prometheus's own loader: unknown
// fields are rejected, expressions are checked by the real PromQL parser, and
// UTF-8 metric names are allowed (the Prometheus 3.x default). A file that
// fails is tried again as a bare ruler-API group; if that fails too, the
// error reported is the first one, which describes the file as written.
func parseRuleFile(path string) (*rulefmt.RuleGroups, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	promql := parser.NewParser(parser.Options{})
	groups, errs := rulefmt.Parse(b, false, model.UTF8Validation, promql, discard)
	if len(errs) == 0 {
		return groups, nil
	}
	wrapped := append([]byte("groups:\n"), indent(b)...)
	if groups, bareErrs := rulefmt.Parse(wrapped, false, model.UTF8Validation, promql, discard); len(bareErrs) == 0 {
		return groups, nil
	}
	return nil, fmt.Errorf("parsing rules %s: %w", path, errs[0])
}

// fromRulefmt converts one parsed rule into backtest's model.
func fromRulefmt(r rulefmt.Rule) Rule {
	if r.Alert != "" {
		return Rule{
			Name: r.Alert, Alert: true, Expr: r.Expr, Labels: r.Labels,
			For: time.Duration(r.For), KeepFiringFor: time.Duration(r.KeepFiringFor),
		}
	}
	return Rule{Name: r.Record, Expr: r.Expr, Labels: r.Labels}
}

// recordingRules maps each recording rule in the set to its expression.
func (rs RuleSet) recordingRules() map[string]string {
	defined := map[string]string{}
	for _, g := range rs.Groups {
		for _, r := range g.Rules {
			if !r.Alert {
				defined[r.Name] = r.Expr
			}
		}
	}
	return defined
}

// indent re-indents a bare ruler-API group document as one list item under a
// groups: key: first content line gets the "- " marker, the rest four spaces.
func indent(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	marked := false
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		if !marked && !strings.HasPrefix(trimmed, "#") {
			lines[i] = "  - " + l
			marked = true
			continue
		}
		lines[i] = "    " + l
	}
	return []byte(strings.Join(lines, "\n"))
}
