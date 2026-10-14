package ingest

import (
	"context"
	"fmt"
	"strings"

	"github.com/tamen25/Argus/engine/internal/rules"
)

// MimirAPI is the port for the Mimir backend (hexagonal, architecture rule
// 1). The concrete HTTP client lives in ingest/mimir; unit tests use fakes.
type MimirAPI interface {
	// LabelValues returns all values of a label across the tenant.
	LabelValues(ctx context.Context, label string) ([]string, error)
	// LabelCardinality returns the series-count contribution of a label on a
	// metric (Mimir cardinality API) — an upper bound on distinct values.
	LabelCardinality(ctx context.Context, metric, label string) (int64, error)
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

// verifyCardinality re-checks flagged (metric, attribute) pairs against the
// Mimir cardinality API, which sees the unsampled stream.
func (p *Poller) verifyCardinality(ctx context.Context, col *rules.Collector, r *rules.Rule, services []string) error {
	limit := int64(10000)
	if v, ok := rules.AsFloat(r.Params["max_cardinality"]); ok {
		limit = int64(v)
	}

	var firstErr error
	for _, pair := range flaggedPairs(col.Snapshot(), r.ID, services) {
		n, err := p.api.LabelCardinality(ctx, pair.metric, pair.attribute)
		switch {
		case err != nil:
			if firstErr == nil {
				firstErr = err
			}
		case n == 0:
			// Mimir has no series for this pair (e.g. the metric only exists
			// on the sampled stream): nothing to verify, so the sampled
			// finding stands.
		default:
			col.RecordPollerResult(rules.PollerResult{
				Service: pair.service, RuleID: r.ID, Passed: n < limit,
				Details: map[string]any{
					"check": "mimir_label_cardinality", "metric": pair.metric,
					"attribute": pair.attribute, "series": n,
				},
			})
		}
	}
	return firstErr
}

// flaggedPair is one (metric, attribute) a cardinality finding named.
type flaggedPair struct{ service, metric, attribute string }

// flaggedPairs lists the (metric, attribute) pairs that rule ruleID's findings
// name as evidence, for the given services.
func flaggedPairs(snap *rules.Snapshot, ruleID string, services []string) []flaggedPair {
	var pairs []flaggedPair
	for _, svc := range services {
		rep := snap.Service(svc)
		if rep == nil {
			continue
		}
		for _, f := range rep.Findings {
			if f.RuleID != ruleID {
				continue
			}
			for _, ev := range f.Evidence {
				metric, _ := ev.Attrs["metric"].(string)
				attr, _ := ev.Attrs["attribute"].(string)
				if metric != "" && attr != "" {
					pairs = append(pairs, flaggedPair{svc, metric, attr})
				}
			}
		}
	}
	return pairs
}
