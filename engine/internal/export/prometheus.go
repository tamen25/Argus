// Package export exposes scores as Prometheus metrics
// (argus_instrumentation_score{service=...}) so users can dashboard and alert
// on their own instrumentation quality — self-referential by design.
package export

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/tamen25/Argus/engine/internal/rules"
)

// Prometheus publishes snapshot-derived gauges.
type Prometheus struct {
	score    *prometheus.GaugeVec
	fleet    prometheus.Gauge
	findings *prometheus.GaugeVec
}

// NewPrometheus registers the Argus gauges on reg.
func NewPrometheus(reg prometheus.Registerer) *Prometheus {
	e := &Prometheus{
		score: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "argus_instrumentation_score",
			Help: "Instrumentation Score (0-100) per service, spec rules only.",
		}, []string{"service"}),
		fleet: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "argus_instrumentation_score_fleet",
			Help: "Mean Instrumentation Score across services.",
		}),
		findings: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "argus_findings",
			Help: "Open findings per service and impact.",
		}, []string{"service", "impact"}),
	}
	reg.MustRegister(e.score, e.fleet, e.findings)
	return e
}

// Update replaces all series with the snapshot's state (stale services drop).
func (e *Prometheus) Update(snap *rules.Snapshot) {
	e.score.Reset()
	e.findings.Reset()
	e.fleet.Set(snap.FleetScore)
	for _, s := range snap.Services {
		e.score.WithLabelValues(s.ServiceName).Set(s.SpecScore)
		for _, f := range s.Findings {
			e.findings.WithLabelValues(s.ServiceName, string(f.Impact)).Inc()
		}
	}
}
