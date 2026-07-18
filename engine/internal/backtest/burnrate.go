package backtest

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SLOPolicySchema is the accepted policy file schema tag.
const SLOPolicySchema = "argus.slo/v1"

// SLOPolicy is one service-level objective with multi-window burn-rate
// alerting policies (SRE-workbook style).
type SLOPolicy struct {
	Name      string           `yaml:"name"`
	Objective float64          `yaml:"objective"` // e.g. 0.999
	SLI       SLI              `yaml:"sli"`
	Windows   []BurnRateWindow `yaml:"windows"` // empty → DefaultBurnRateWindows
}

// SLI names the error and total series the ratio is computed from. Both are
// rate()-able selectors; GroupBy keeps the two sides vector-matchable.
type SLI struct {
	Errors  string   `yaml:"errors"`
	Total   string   `yaml:"total"`
	GroupBy []string `yaml:"group_by"`
}

// BurnRateWindow is one fast/slow pair: alert when BOTH the short and long
// window burn rates exceed Factor × the error budget.
type BurnRateWindow struct {
	Short    time.Duration `yaml:"short"`
	Long     time.Duration `yaml:"long"`
	Factor   float64       `yaml:"factor"`
	For      time.Duration `yaml:"for"`
	Severity string        `yaml:"severity"`
}

// DefaultBurnRateWindows is the standard 5m/1h fast-page + 30m/6h slow-ticket
// pair from the SRE workbook.
func DefaultBurnRateWindows() []BurnRateWindow {
	return []BurnRateWindow{
		{Short: 5 * time.Minute, Long: time.Hour, Factor: 14.4, For: 2 * time.Minute, Severity: "page"},
		{Short: 30 * time.Minute, Long: 6 * time.Hour, Factor: 6.0, For: 15 * time.Minute, Severity: "ticket"},
	}
}

// LoadSLOPolicies strictly parses an SLO policy file.
func LoadSLOPolicies(path string) ([]SLOPolicy, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Schema   string      `yaml:"schema"`
		Policies []SLOPolicy `yaml:"policies"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parsing SLO policies %s: %w", path, err)
	}
	if doc.Schema != SLOPolicySchema {
		return nil, fmt.Errorf("SLO policies %s: schema %q, want %q", path, doc.Schema, SLOPolicySchema)
	}
	for i := range doc.Policies {
		p := &doc.Policies[i]
		if p.Objective <= 0 || p.Objective >= 1 {
			return nil, fmt.Errorf("SLO policy %q: objective %v must be in (0, 1)", p.Name, p.Objective)
		}
		if p.SLI.Errors == "" || p.SLI.Total == "" {
			return nil, fmt.Errorf("SLO policy %q: sli.errors and sli.total are required", p.Name)
		}
		if len(p.Windows) == 0 {
			p.Windows = DefaultBurnRateWindows()
		}
	}
	return doc.Policies, nil
}

// BurnRateRules turns a policy into replayable alert rules — one per window
// pair. The burn-rate simulation is thereby the same replay pipeline as
// ordinary alerts: same for:-state semantics, same fidelity caveats, no
// second evaluator to drift from the first.
func BurnRateRules(p SLOPolicy) []Rule {
	budget := 1 - p.Objective
	var rules []Rule
	for _, w := range p.Windows {
		cond := func(window time.Duration) string {
			return fmt.Sprintf("%s(rate(%s[%s])) / %s(rate(%s[%s])) > (%.3f * %f)",
				groupBy(p.SLI.GroupBy), p.SLI.Errors, promDur(window),
				groupBy(p.SLI.GroupBy), p.SLI.Total, promDur(window),
				w.Factor, budget)
		}
		rules = append(rules, Rule{
			Name:  fmt.Sprintf("%s-burnrate-%s-%s", p.Name, w.Severity, w.Short),
			Alert: true,
			Expr:  fmt.Sprintf("(%s) and (%s)", cond(w.Short), cond(w.Long)),
			For:   w.For,
			Labels: map[string]string{
				"severity":   w.Severity,
				"slo_policy": p.Name,
			},
		})
	}
	return rules
}

func groupBy(labels []string) string {
	if len(labels) == 0 {
		return "sum"
	}
	return fmt.Sprintf("sum by (%s) ", strings.Join(labels, ", "))
}

// promDur renders a duration in compact PromQL form (5m, 1h, 6h — not 5m0s).
func promDur(d time.Duration) string {
	s := d.String()
	s = strings.TrimSuffix(s, "0s")
	s = strings.TrimSuffix(s, "0m")
	if s == "" {
		return "0s"
	}
	return s
}
