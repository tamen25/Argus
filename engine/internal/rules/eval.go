package rules

import (
	"fmt"

	"github.com/google/cel-go/cel"

	"github.com/tamen25/Argus/engine/internal/model"
)

// celEnv declares the variables rule criteria may reference. One shared env;
// compiled programs are goroutine-safe.
var celEnv = func() *cel.Env {
	env, err := cel.NewEnv(
		cel.Variable("kind", cel.StringType),
		cel.Variable("service", cel.StringType),
		cel.Variable("resource", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("scope", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("span", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("metric", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("log", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("agg", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("params", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		panic(fmt.Sprintf("building CEL env: %v", err))
	}
	return env
}()

func compileCriteria(r *Rule) (cel.Program, error) {
	ast, iss := celEnv.Compile(r.Evaluation.Criteria)
	if iss != nil && iss.Err() != nil {
		return nil, iss.Err()
	}
	if ast.OutputType() != cel.BoolType {
		return nil, fmt.Errorf("criteria must evaluate to bool, got %s", ast.OutputType())
	}
	return celEnv.Program(ast)
}

// compiled pairs a rule with its ready-to-run CEL program.
type compiled struct {
	rule *Rule
	prog cel.Program
}

// Engine evaluates loaded rules against items and aggregate rows.
type Engine struct {
	item      []compiled // mode=item, indexed lookup by target at eval time
	aggregate []compiled // mode=aggregate
	all       []*Rule
}

// NewEngine compiles all rules. Load errors were already surfaced by the
// loader; this recompiles into executable programs.
func NewEngine(rs []*Rule) (*Engine, error) {
	e := &Engine{all: rs}
	for _, r := range rs {
		prog, err := compileCriteria(r)
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", r.ID, err)
		}
		c := compiled{rule: r, prog: prog}
		if r.Evaluation.Mode == ModeItem {
			e.item = append(e.item, c)
		} else {
			e.aggregate = append(e.aggregate, c)
		}
	}
	return e, nil
}

// Rules returns all loaded rules (sorted by ID).
func (e *Engine) Rules() []*Rule { return e.all }

// Violation is a single failed evaluation. Err is non-nil when the criteria
// itself failed to evaluate (reported, never silently dropped — architecture
// rule 7: honest reporting).
type Violation struct {
	RuleID  string
	Rule    *Rule
	Service string
	Err     error
	// Fields carries aggregate-row fields for aggregate violations (e.g.
	// metric name, attribute, cardinality estimate).
	Fields map[string]any
}

// EvalItem runs every item-mode rule matching the item's target. A false
// criteria result is a violation; an evaluation error is a violation with Err.
func (e *Engine) EvalItem(it model.Item) []Violation {
	act := itemActivation(it)
	var out []Violation
	for _, c := range e.item {
		if !targetMatches(c.rule.Target, it.Kind) {
			continue
		}
		act["params"] = paramsOf(c.rule)
		val, _, err := c.prog.Eval(act)
		if err != nil {
			out = append(out, Violation{RuleID: c.rule.ID, Rule: c.rule, Service: it.Service, Err: err})
			continue
		}
		if pass, ok := val.Value().(bool); !ok || !pass {
			out = append(out, Violation{RuleID: c.rule.ID, Rule: c.rule, Service: it.Service})
		}
	}
	return out
}

// AggregateRow is one named aggregate produced by the ingest layer or a
// poller (e.g. metric_attribute_cardinality with fields metric, attribute,
// cardinality).
type AggregateRow struct {
	Service   string
	Aggregate string
	Fields    map[string]any
}

// EvalAggregate runs every aggregate-mode rule bound to the row's aggregate
// name.
func (e *Engine) EvalAggregate(row AggregateRow) []Violation {
	var out []Violation
	for _, c := range e.aggregate {
		if c.rule.Evaluation.Aggregate != row.Aggregate {
			continue
		}
		act := map[string]any{
			"service": row.Service,
			"agg":     row.Fields,
			"params":  paramsOf(c.rule),
		}
		val, _, err := c.prog.Eval(act)
		if err != nil {
			out = append(out, Violation{RuleID: c.rule.ID, Rule: c.rule, Service: row.Service, Err: err, Fields: row.Fields})
			continue
		}
		if pass, ok := val.Value().(bool); !ok || !pass {
			out = append(out, Violation{RuleID: c.rule.ID, Rule: c.rule, Service: row.Service, Fields: row.Fields})
		}
	}
	return out
}

// targetMatches maps rule targets onto item kinds. Resource-target rules run
// against every item (the resource travels with each item; per-service dedup
// happens in the aggregator's rollup).
func targetMatches(target string, kind model.Kind) bool {
	switch target {
	case "resource":
		return true
	case "span":
		return kind == model.KindSpan
	case "metric":
		return kind == model.KindMetricPoint
	case "log":
		return kind == model.KindLogRecord
	default:
		return false
	}
}

var emptyMap = map[string]any{}

func itemActivation(it model.Item) map[string]any {
	act := map[string]any{
		"kind":     it.Kind.String(),
		"service":  it.Service,
		"resource": it.Resource,
		"scope":    map[string]any{"name": it.Scope.Name, "version": it.Scope.Version},
		"span":     emptyMap,
		"metric":   emptyMap,
		"log":      emptyMap,
		"agg":      emptyMap,
	}
	switch {
	case it.Span != nil:
		act["span"] = map[string]any{
			"name": it.Span.Name, "kind": it.Span.Kind,
			"has_parent": it.Span.HasParent, "status": it.Span.Status,
			"attrs": orEmpty(it.Span.Attrs),
		}
	case it.Metric != nil:
		act["metric"] = map[string]any{
			"name": it.Metric.Name, "type": it.Metric.Type, "unit": it.Metric.Unit,
			"has_exemplars": it.Metric.HasExemplars, "attrs": orEmpty(it.Metric.Attrs),
		}
	case it.Log != nil:
		act["log"] = map[string]any{
			"severity_text": it.Log.SeverityText, "severity_number": int64(it.Log.SeverityNumber),
			"has_trace_id": it.Log.HasTraceID, "body_len": int64(it.Log.BodyLen),
			"attrs": orEmpty(it.Log.Attrs),
		}
	}
	return act
}

func paramsOf(r *Rule) map[string]any {
	if r.Params == nil {
		return emptyMap
	}
	return r.Params
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return emptyMap
	}
	return m
}
