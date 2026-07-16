package backtest

import (
	"fmt"
	"strings"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/promql/parser"
)

// Synthesize rewrites an expression for replay over history where the loaded
// set's recording rules never ran: every reference to a defined recording
// rule is replaced by its defining expression, parenthesized, in place
// (recursively — recording rules may reference other recording rules).
//
// It returns the rewritten expression and the caveats the substitution
// incurs; synthesis is never silent (architecture rule 7). It refuses two
// unsound cases outright:
//
//   - references to recording rules not defined in the loaded set — there is
//     nothing to inline, and guessing would fabricate data;
//   - references carrying extra label matchers (e.g. rule{service="x"}) —
//     matchers select on the recorded series' output labels, which do not
//     distribute soundly into an arbitrary defining expression.
func Synthesize(rs RuleSet, expr string) (string, []string, error) {
	defined := map[string]string{}
	for _, g := range rs.Groups {
		for _, r := range g.Rules {
			if !r.Alert {
				defined[r.Name] = r.Expr
			}
		}
	}

	promql := parser.NewParser(parser.Options{})
	root, err := promql.ParseExpr(expr)
	if err != nil {
		return "", nil, fmt.Errorf("parsing %q: %w", expr, err)
	}

	var caveats []string
	var rewrite func(node parser.Node) error
	rewrite = func(node parser.Node) error {
		return inspectChildren(node, func(child parser.Expr) (parser.Expr, error) {
			vs, ok := child.(*parser.VectorSelector)
			if !ok {
				return nil, rewrite(child)
			}
			def, isDefined := defined[vs.Name]
			if !isDefined {
				if strings.Contains(vs.Name, ":") {
					return nil, fmt.Errorf("%s references recording rule %q, which is not defined in the loaded set — cannot synthesize", expr, vs.Name)
				}
				return nil, nil // plain series, leave as-is
			}
			if hasExtraMatchers(vs) {
				return nil, fmt.Errorf("%s applies label matchers to recording rule %q — matcher pushdown into the defining expression is unsound, refusing to synthesize", expr, vs.Name)
			}
			inlined, err := promql.ParseExpr("(" + def + ")")
			if err != nil {
				return nil, fmt.Errorf("parsing definition of %q: %w", vs.Name, err)
			}
			// the definition may itself reference recording rules
			if err := rewrite(inlined); err != nil {
				return nil, err
			}
			caveats = append(caveats, fmt.Sprintf(
				"recording rule %q synthesized inline: replay evaluates its expression at query time over raw series, not the stored series the live ruler would have produced (staleness and evaluation-interval semantics differ)", vs.Name))
			return inlined, nil
		})
	}
	if err := rewrite(root); err != nil {
		return "", nil, err
	}

	// the root itself may be a bare recording-rule reference
	if vs, ok := root.(*parser.VectorSelector); ok {
		if def, isDefined := defined[vs.Name]; isDefined {
			if hasExtraMatchers(vs) {
				return "", nil, fmt.Errorf("%s applies label matchers to recording rule %q — refusing to synthesize", expr, vs.Name)
			}
			inlined, err := promql.ParseExpr("(" + def + ")")
			if err != nil {
				return "", nil, err
			}
			caveats = append(caveats, fmt.Sprintf("recording rule %q synthesized inline", vs.Name))
			root = inlined
		}
	}

	return root.String(), caveats, nil
}

// inspectChildren visits each child expression of node; when the callback
// returns a non-nil replacement, the child is swapped in place.
func inspectChildren(node parser.Node, fn func(parser.Expr) (parser.Expr, error)) error {
	children := parser.Children(node)
	for i, c := range children {
		expr, ok := c.(parser.Expr)
		if !ok {
			continue
		}
		repl, err := fn(expr)
		if err != nil {
			return err
		}
		if repl != nil {
			if err := setChild(node, i, repl); err != nil {
				return err
			}
		}
	}
	return nil
}

// setChild replaces the i-th child of node — the promql AST has no generic
// mutator, so the common node kinds are handled explicitly.
func setChild(node parser.Node, i int, repl parser.Expr) error {
	switch n := node.(type) {
	case *parser.BinaryExpr:
		if i == 0 {
			n.LHS = repl
		} else {
			n.RHS = repl
		}
	case *parser.AggregateExpr:
		if i == 0 {
			n.Expr = repl
		} else {
			n.Param = repl
		}
	case *parser.Call:
		n.Args[i] = repl
	case *parser.ParenExpr:
		n.Expr = repl
	case *parser.UnaryExpr:
		n.Expr = repl
	case *parser.SubqueryExpr:
		n.Expr = repl
	case *parser.StepInvariantExpr:
		n.Expr = repl
	case *parser.MatrixSelector:
		vs, ok := repl.(*parser.VectorSelector)
		if !ok {
			return fmt.Errorf("cannot inline an expression inside a range selector ([%s]) — a recording rule used in a matrix context needs the stored series", n.String())
		}
		n.VectorSelector = vs
	default:
		return fmt.Errorf("unsupported node %T during synthesis", node)
	}
	return nil
}

// hasExtraMatchers reports whether the selector carries matchers beyond its
// own __name__.
func hasExtraMatchers(vs *parser.VectorSelector) bool {
	for _, m := range vs.LabelMatchers {
		if m.Name != model.MetricNameLabel {
			return true
		}
	}
	return false
}
