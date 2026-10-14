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
	s := &synthesizer{
		expr:     expr,
		defined:  rs.recordingRules(),
		inlining: map[string]bool{},
		parser:   parser.NewParser(parser.Options{}),
	}
	root, err := s.parser.ParseExpr(expr)
	if err != nil {
		return "", nil, fmt.Errorf("parsing %q: %w", expr, err)
	}
	// The root goes through replace like any other node: it may itself be a
	// bare recording-rule reference, which no parent node can swap out.
	repl, err := s.replace(root)
	if err != nil {
		return "", nil, err
	}
	if repl != nil {
		root = repl
	}
	return root.String(), s.caveats, nil
}

// synthesizer rewrites one expression, inlining recording rules.
type synthesizer struct {
	expr     string            // the expression being rewritten, for errors
	defined  map[string]string // recording rule name → defining expression
	inlining map[string]bool   // rules being inlined right now, to catch cycles
	parser   parser.Parser
	caveats  []string
}

// replace returns the expression node should become: a recording-rule
// reference becomes its definition, parenthesized and itself rewritten. It
// returns nil when node stays, after rewriting any references below it.
func (s *synthesizer) replace(node parser.Expr) (parser.Expr, error) {
	vs, ok := node.(*parser.VectorSelector)
	if !ok {
		return nil, inspectChildren(node, s.replace)
	}
	def, isDefined := s.defined[vs.Name]
	if !isDefined {
		if strings.Contains(vs.Name, ":") {
			return nil, fmt.Errorf("%s references recording rule %q, which is not defined in the loaded set — cannot synthesize", s.expr, vs.Name)
		}
		return nil, nil // a plain series stays as it is
	}
	if hasExtraMatchers(vs) {
		return nil, fmt.Errorf("%s applies label matchers to recording rule %q — matcher pushdown into the defining expression is unsound, refusing to synthesize", s.expr, vs.Name)
	}
	if s.inlining[vs.Name] {
		return nil, fmt.Errorf("%s: recording rule %q is defined in terms of itself — cannot synthesize", s.expr, vs.Name)
	}

	inlined, err := s.parser.ParseExpr("(" + def + ")")
	if err != nil {
		return nil, fmt.Errorf("parsing definition of %q: %w", vs.Name, err)
	}
	// The definition may itself reference recording rules.
	s.inlining[vs.Name] = true
	err = inspectChildren(inlined, s.replace)
	delete(s.inlining, vs.Name)
	if err != nil {
		return nil, err
	}
	s.caveats = append(s.caveats, fmt.Sprintf(
		"recording rule %q synthesized inline: replay evaluates its expression at query time over raw series, not the stored series the live ruler would have produced (staleness and evaluation-interval semantics differ)", vs.Name))
	return inlined, nil
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
