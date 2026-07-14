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

// RegisterAggregateStats exposes the aggregate store's bounds as
// self-metrics: pair count and LRU evictions (honest reporting of estimator
// pressure).
func RegisterAggregateStats(reg prometheus.Registerer, pairs func() int, evictions func() int64) {
	reg.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "argus_aggregate_pairs_tracked",
			Help: "Live (service, metric, attribute) sketch pairs across both window generations.",
		}, func() float64 { return float64(pairs()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "argus_aggregate_pair_evictions_total",
			Help: "Sketch pairs evicted by the LRU admission policy (estimates lost).",
		}, func() float64 { return float64(evictions()) }),
	)
}

// RegisterItemStats exposes per-signal item throughput
// (argus_items_consumed_total{signal}); items/sec derives from its rate.
func RegisterItemStats(reg prometheus.Registerer, items func() (traces, metrics, logs int64)) {
	desc := prometheus.NewDesc(
		"argus_items_consumed_total",
		"Telemetry items consumed since startup, by signal.",
		[]string{"signal"}, nil,
	)
	reg.MustRegister(itemStatsCollector{desc: desc, items: items})
}

type itemStatsCollector struct {
	desc  *prometheus.Desc
	items func() (int64, int64, int64)
}

func (c itemStatsCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c itemStatsCollector) Collect(ch chan<- prometheus.Metric) {
	tr, me, lo := c.items()
	ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, float64(tr), "traces")
	ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, float64(me), "metrics")
	ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, float64(lo), "logs")
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
