// Package backtest replays historical Mimir data against Prometheus/Mimir
// alert rules (module B). Phase 3 starts with the mandatory fidelity spike:
// this package currently holds the spike probes — rule loading, recording-rule
// dependency detection, and usable-window discovery — that seed the full
// engine. Deterministic core: never imports the LLM client (depguard,
// architecture rule 2). Backends sit behind interfaces defined here
// (architecture rule 1); concrete Mimir clients live in adapter packages.
package backtest

import (
	"fmt"
	"io"
	"log/slog"
	"os"
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
	Name   string // alert name, or the recording rule's series name
	Alert  bool
	Expr   string
	For    time.Duration     // alerts only
	Labels map[string]string // static labels from the rule file
}

// LoadRuleFiles strictly parses Prometheus/Mimir ruler rule files (Sloth and
// Pyrra emit the same format). Any parse or validation error fails the load —
// a backtest against a silently half-loaded rule set would report misses that
// are really loader bugs.
func LoadRuleFiles(paths ...string) (RuleSet, error) {
	var rs RuleSet
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return RuleSet{}, err
		}
		// strict: unknown fields rejected, expressions validated by the real
		// PromQL parser, UTF-8 metric names allowed (Prometheus 3.x default)
		discard := slog.New(slog.NewTextHandler(io.Discard, nil))
		groups, errs := rulefmt.Parse(b, false, model.UTF8Validation, parser.NewParser(parser.Options{}), discard)
		if len(errs) > 0 {
			return RuleSet{}, fmt.Errorf("parsing rules %s: %w", path, errs[0])
		}
		for _, g := range groups.Groups {
			grp := Group{Name: g.Name, Interval: time.Duration(g.Interval)}
			for _, r := range g.Rules {
				rule := Rule{
					Name:   r.Record,
					Expr:   r.Expr,
					Labels: r.Labels,
				}
				if r.Alert != "" {
					rule.Name = r.Alert
					rule.Alert = true
					rule.For = time.Duration(r.For)
				}
				grp.Rules = append(grp.Rules, rule)
			}
			rs.Groups = append(rs.Groups, grp)
		}
	}
	return rs, nil
}
