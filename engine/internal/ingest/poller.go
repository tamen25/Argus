package ingest

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/tamen25/Argus/engine/internal/rules"
)

// MimirAPI is the port for the Mimir backend (hexagonal, architecture rule
// 1). The concrete HTTP client lives in ingest/mimir; unit tests use fakes.
type MimirAPI interface {
	// LabelValues returns all values of a label across the tenant.
	LabelValues(ctx context.Context, label string) ([]string, error)
	// LabelCardinality returns how many distinct values label has among the
	// series the selector matches, or 0 when it matches none (Mimir
	// cardinality API).
	LabelCardinality(ctx context.Context, selector, label string) (int64, error)
}

// serviceSelector selects one metric's series for one service. The metric is
// matched under its Prometheus names (promNameRegexp). Mimir carries the
// service in job, as "<namespace>/<service>" or "<service>" (see
// verifyServicePresence), so the match allows either.
func serviceSelector(m metricAttribute, service string) string {
	return fmt.Sprintf(`{__name__=~%q, job=~%q}`, promNameRegexp(m.metric, m.unit, m.typ), "(.+/)?"+regexp.QuoteMeta(service))
}

// Poller runs backend verification for rules that declare a
// confidence.poller check. The backend sees unsampled data, so its verdicts
// override sampled stream results (confidence: verified).
type Poller struct {
	api MimirAPI
	eng *rules.Engine
}

// NewPoller builds a poller over a Mimir client and the loaded rules.
func NewPoller(api MimirAPI, eng *rules.Engine) *Poller {
	return &Poller{api: api, eng: eng}
}

// Run executes every known poller check for the given services, recording
// verified results into the collector. Errors are returned (never swallowed);
// sampled results stand when verification fails.
func (p *Poller) Run(ctx context.Context, col *rules.Collector, services []string) error {
	var errs []string
	for _, r := range p.eng.Rules() {
		switch r.Confidence.Poller {
		case "":
			continue
		case "mimir_service_presence":
			if err := p.verifyServicePresence(ctx, col, r, services); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", r.ID, err))
			}
		case "mimir_label_cardinality":
			if err := p.verifyCardinality(ctx, col, r, services); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", r.ID, err))
			}
		default:
			errs = append(errs, fmt.Sprintf("%s: unknown poller check %q", r.ID, r.Confidence.Poller))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("poller checks failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

// verifyServicePresence marks services that appear with a proper job label in
// Mimir as verified-passing. Absence is NOT failure (the service may simply
// not emit metrics), so only positive verification is recorded.
func (p *Poller) verifyServicePresence(ctx context.Context, col *rules.Collector, r *rules.Rule, services []string) error {
	jobs, err := p.api.LabelValues(ctx, "job")
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, j := range jobs {
		// OTLP-ingested metrics carry job="<namespace>/<service>" or "<service>"
		present[j] = true
		if i := strings.LastIndex(j, "/"); i >= 0 {
			present[j[i+1:]] = true
		}
	}
	for _, svc := range services {
		if present[svc] {
			col.RecordPollerResult(rules.PollerResult{
				Service: svc, RuleID: r.ID, Passed: true,
				Details: map[string]any{"check": "mimir_service_presence"},
			})
		}
	}
	return nil
}

// verifyCardinality re-checks each service's flagged (metric, attribute)
// pairs against the Mimir cardinality API, which sees the unsampled stream.
//
// One result is recorded per service, combining its pairs: the rule fails if
// any pair Mimir measured is over the limit. Recording a result per pair
// instead let the last pair checked overwrite the others, so a passing pair
// could hide a failing one. A pair Mimir has no series for (a metric only on
// the sampled stream, or translated in a way promNameRegexp does not know) is
// not measured, and a service with no measured pair keeps its sampled result.
func (p *Poller) verifyCardinality(ctx context.Context, col *rules.Collector, r *rules.Rule, services []string) error {
	limit := int64(10000)
	if v, ok := rules.AsFloat(r.Params["max_cardinality"]); ok {
		limit = int64(v)
	}

	flagged := flaggedPairs(col.Snapshot(), r.ID, services)
	var firstErr error
	for _, svc := range services {
		passed, measured := true, []map[string]any{}
		for _, pair := range flagged[svc] {
			n, err := p.api.LabelCardinality(ctx, serviceSelector(pair, svc), promLabelName(pair.attribute))
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if n == 0 {
				continue
			}
			passed = passed && n < limit
			measured = append(measured, map[string]any{"metric": pair.metric, "attribute": pair.attribute, "cardinality": n})
		}
		if len(measured) == 0 {
			continue
		}
		col.RecordPollerResult(rules.PollerResult{
			Service: svc, RuleID: r.ID, Passed: passed,
			Details: map[string]any{"check": "mimir_label_cardinality", "pairs": measured},
		})
	}
	return firstErr
}

// metricAttribute is one (metric, attribute) pair a cardinality finding named,
// in OTLP terms, with the metric's unit and type for the name translation.
type metricAttribute struct{ metric, attribute, unit, typ string }

// flaggedPairs lists, per service, the distinct (metric, attribute) pairs
// that rule ruleID's findings name as evidence, in evidence order.
func flaggedPairs(snap *rules.Snapshot, ruleID string, services []string) map[string][]metricAttribute {
	out := map[string][]metricAttribute{}
	for _, svc := range services {
		rep := snap.Service(svc)
		if rep == nil {
			continue
		}
		seen := map[metricAttribute]bool{}
		for _, f := range rep.Findings {
			if f.RuleID != ruleID {
				continue
			}
			for _, ev := range f.Evidence {
				metric, _ := ev.Attrs["metric"].(string)
				attr, _ := ev.Attrs["attribute"].(string)
				// Rows from before unit and type were recorded have neither;
				// they are then matched by the plain name only.
				unit, _ := ev.Attrs["unit"].(string)
				typ, _ := ev.Attrs["type"].(string)
				pair := metricAttribute{metric, attr, unit, typ}
				if metric != "" && attr != "" && !seen[pair] {
					seen[pair] = true
					out[svc] = append(out[svc], pair)
				}
			}
		}
	}
	return out
}
