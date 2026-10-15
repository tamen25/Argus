// Package export exposes scores as Prometheus metrics
// (argus_instrumentation_score{service=...}) so users can dashboard and alert
// on their own instrumentation quality — self-referential by design.
package export

import (
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/tamen25/Argus/engine/internal/rules"
)

// Prometheus publishes the latest snapshot's scores as gauges. It is a
// collector over one snapshot pointer: Update swaps the pointer, and a scrape
// builds every series from the snapshot it loads. Resetting gauge vectors and
// filling them again would let a scrape that lands in between see some
// services or none, and an alert on an absent series would fire for nothing.
type Prometheus struct {
	snap     atomic.Pointer[rules.Snapshot]
	score    *prometheus.Desc
	fleet    *prometheus.Desc
	findings *prometheus.Desc
}

// NewPrometheus registers the Argus gauges on reg.
func NewPrometheus(reg prometheus.Registerer) *Prometheus {
	e := &Prometheus{
		score: prometheus.NewDesc("argus_instrumentation_score",
			"Instrumentation Score (0-100) per service, spec rules only.", []string{"service"}, nil),
		fleet: prometheus.NewDesc("argus_instrumentation_score_fleet",
			"Mean Instrumentation Score across services.", nil, nil),
		findings: prometheus.NewDesc("argus_findings",
			"Open findings per service and impact.", []string{"service", "impact"}, nil),
	}
	reg.MustRegister(e)
	return e
}

// Update makes snap the state the next scrape reports. Services missing from
// it drop out. The snapshot must not be changed afterwards.
func (e *Prometheus) Update(snap *rules.Snapshot) { e.snap.Store(snap) }

// Describe implements prometheus.Collector.
func (e *Prometheus) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.score
	ch <- e.fleet
	ch <- e.findings
}

// Collect implements prometheus.Collector. Before the first Update, and for a
// fleet with no services, there is no fleet series: an empty fleet's score is
// a vacuous 100, which a dashboard would show as perfect instrumentation.
func (e *Prometheus) Collect(ch chan<- prometheus.Metric) {
	snap := e.snap.Load()
	if snap == nil || len(snap.Services) == 0 {
		return
	}
	ch <- prometheus.MustNewConstMetric(e.fleet, prometheus.GaugeValue, snap.FleetScore)
	for _, s := range snap.Services {
		ch <- prometheus.MustNewConstMetric(e.score, prometheus.GaugeValue, s.SpecScore, s.ServiceName)
		byImpact := map[rules.Impact]int{}
		for _, f := range s.Findings {
			byImpact[f.Impact]++
		}
		for impact, n := range byImpact {
			ch <- prometheus.MustNewConstMetric(e.findings, prometheus.GaugeValue, float64(n), s.ServiceName, string(impact))
		}
	}
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
