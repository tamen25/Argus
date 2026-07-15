// Package calibrate proposes evidence-based values for rule params from
// observed distributions (soak aggregates, report/Postgres finding ratios).
//
// Boundaries, by design:
//   - criteria are never modified — only the params key named by a rule's
//     calibration block (spec rules: params the spec leaves open;
//     argus-extension rules: any declared param);
//   - robust statistics only (median, MAD, nearest-rank percentiles) —
//     telemetry distributions are heavy-tailed;
//   - deterministic: the same input set produces byte-identical output;
//   - proposals are files a human reviews and commits (read-only product).
package calibrate

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tamen25/Argus/engine/internal/rules"
	"github.com/tamen25/Argus/engine/internal/stats"
)

// Input is everything Propose looks at.
type Input struct {
	Rules      []*rules.Rule
	Aggregates []rules.AggregateRow // union of aggregate snapshots
	Ratios     map[string][]float64 // rule ID -> per-service violation ratios
}

// Proposal is one evidence-backed param suggestion.
type Proposal struct {
	RuleID, RuleName, Param string
	Kind                    string
	Current, Proposed       float64
	N                       int
	Median, MAD, P90, P99   float64
	Max                     float64
	Note                    string
}

// Propose computes proposals for every calibratable rule that has data.
// Rules without observations are skipped — calibration never invents
// evidence. Output is sorted by rule ID.
func Propose(in Input) []Proposal {
	var out []Proposal
	for _, r := range in.Rules {
		c := r.Calibration
		if c.Param == "" {
			continue
		}
		var vals []float64
		note := ""
		switch c.Source {
		case "aggregate":
			for _, row := range in.Aggregates {
				if row.Aggregate != c.Aggregate {
					continue
				}
				if v, ok := toFloat(row.Fields[c.Field]); ok {
					vals = append(vals, v)
				}
			}
		case "finding_ratio":
			vals = in.Ratios[r.ID]
			note = "ratios from failing services only — passing services carry no stats in reports"
		}
		if len(vals) == 0 {
			continue
		}
		p99 := stats.Percentile(vals, 99)
		prop := Proposal{
			RuleID: r.ID, RuleName: r.Name, Param: c.Param, Kind: c.Kind,
			Current: currentParam(r, c.Param),
			N:       len(vals),
			Median:  stats.Median(vals), MAD: stats.MAD(vals),
			P90: stats.Percentile(vals, 90), P99: p99, Max: stats.Max(vals),
			Note: note,
		}
		switch c.Kind {
		case "count":
			prop.Proposed = ceil2sig(p99 * 2)
		case "small_count":
			prop.Proposed = math.Ceil(p99) + 1
		case "ratio":
			spread := math.Max(0.05, 2*prop.MAD)
			prop.Proposed = math.Min(1, math.Round((p99+spread)*100)/100)
		}
		out = append(out, prop)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RuleID < out[j].RuleID })
	return out
}

// ceil2sig rounds up to two significant digits (deterministic headroom
// ceiling for large cardinalities).
func ceil2sig(v float64) float64 {
	if v <= 0 {
		return 0
	}
	exp := math.Pow(10, math.Floor(math.Log10(v))-1)
	return math.Ceil(v/exp) * exp
}

func currentParam(r *rules.Rule, key string) float64 {
	if key == rules.ServiceViolationParam {
		return r.ServiceViolation.ThresholdRatio
	}
	v, _ := toFloat(r.Params[key])
	return v
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// Render writes the human review table (Markdown). Disclosures — evidence
// quality caveats like a segmented soak run — render above the table where
// a reviewer cannot miss them.
func Render(props []Proposal, disclosures ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Proposed threshold overrides\n\n")
	for _, d := range disclosures {
		fmt.Fprintf(&b, "> ⚠️ %s\n\n", d)
	}
	if len(props) == 0 {
		fmt.Fprintf(&b, "No calibratable rule had observations — nothing proposed.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Review each row against the distribution before committing the override\nfiles. Calibration adjusts params only; criteria are untouched.\n\n")
	fmt.Fprintf(&b, "| Rule | Param | Current | Proposed | n | median | MAD | P90 | P99 | max |\n")
	fmt.Fprintf(&b, "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, p := range props {
		fmt.Fprintf(&b, "| %s | %s | %s | **%s** | %d | %s | %s | %s | %s | %s |\n",
			p.RuleID, p.Param, num(p.Current), num(p.Proposed), p.N,
			num(p.Median), num(p.MAD), num(p.P90), num(p.P99), num(p.Max))
	}
	for _, p := range props {
		if p.Note != "" {
			fmt.Fprintf(&b, "\n> %s: %s\n", p.RuleID, p.Note)
		}
	}
	return b.String()
}

// num prints integers bare and fractions with two decimals — matches how
// the values appear in rule YAML.
func num(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// OverrideYAML emits a complete override rule file (rules.Merge replaces
// whole rules by ID, so the copy must be complete) with only the calibrated
// param changed.
func OverrideYAML(r *rules.Rule, p Proposal) ([]byte, error) {
	out := yamlRule{
		Schema: r.Schema, ID: r.ID, Source: r.Source, Name: r.Name,
		Description: r.Description, Target: r.Target, Impact: string(r.Impact),
		Evaluation: yamlEvaluation{
			Mode:      r.Evaluation.Mode,
			Aggregate: r.Evaluation.Aggregate,
			Criteria:  r.Evaluation.Criteria,
		},
		Params: map[string]any{},
	}
	for k, v := range r.Params {
		out.Params[k] = v
	}
	sv := r.ServiceViolation.ThresholdRatio
	if p.Param == rules.ServiceViolationParam {
		sv = p.Proposed
	} else {
		out.Params[p.Param] = asParamValue(p.Proposed)
	}
	if sv != 0 {
		out.ServiceViolation = &yamlServiceViolation{ThresholdRatio: sv}
	}
	if r.Confidence.Poller != "" {
		out.Confidence = &yamlConfidence{Poller: r.Confidence.Poller}
	}
	if r.Remediation.Template != "" {
		out.Remediation = &yamlRemediation{Template: r.Remediation.Template}
	}
	if c := r.Calibration; c.Param != "" {
		out.Calibration = &yamlCalibration{
			Param: c.Param, Source: c.Source, Aggregate: c.Aggregate,
			Field: c.Field, Kind: c.Kind,
		}
	}

	body, err := yaml.Marshal(out)
	if err != nil {
		return nil, err
	}
	header := fmt.Sprintf("# Calibrated override for %s — generated by `argus rules calibrate`.\n"+
		"# Only params.%s changed (%s -> %s, kind=%s, n=%d). Criteria untouched.\n"+
		"# Review, then use with: argus score --rules <this dir>\n",
		p.RuleID, p.Param, num(p.Current), num(p.Proposed), p.Kind, p.N)
	return append([]byte(header), body...), nil
}

// asParamValue keeps whole numbers as ints so the YAML matches hand-written
// rule files (max_span_names: 260, not 260.0).
func asParamValue(v float64) any {
	if v == math.Trunc(v) {
		return int(v)
	}
	return v
}

// yamlRule mirrors rules.Rule with omitempty so overrides stay as clean as
// hand-written files (the rules schema itself stays untouched).
type yamlRule struct {
	Schema           string                `yaml:"schema"`
	ID               string                `yaml:"id"`
	Source           string                `yaml:"source"`
	Name             string                `yaml:"name"`
	Description      string                `yaml:"description"`
	Target           string                `yaml:"target"`
	Impact           string                `yaml:"impact"`
	Evaluation       yamlEvaluation        `yaml:"evaluation"`
	Params           map[string]any        `yaml:"params,omitempty"`
	ServiceViolation *yamlServiceViolation `yaml:"service_violation,omitempty"`
	Confidence       *yamlConfidence       `yaml:"confidence,omitempty"`
	Remediation      *yamlRemediation      `yaml:"remediation,omitempty"`
	Calibration      *yamlCalibration      `yaml:"calibration,omitempty"`
}

type yamlEvaluation struct {
	Mode      string `yaml:"mode"`
	Aggregate string `yaml:"aggregate,omitempty"`
	Criteria  string `yaml:"criteria"`
}

type yamlServiceViolation struct {
	ThresholdRatio float64 `yaml:"threshold_ratio"`
}

type yamlConfidence struct {
	Poller string `yaml:"poller"`
}

type yamlRemediation struct {
	Template string `yaml:"template"`
}

type yamlCalibration struct {
	Param     string `yaml:"param"`
	Source    string `yaml:"source"`
	Aggregate string `yaml:"aggregate,omitempty"`
	Field     string `yaml:"field,omitempty"`
	Kind      string `yaml:"kind"`
}
