package cost

import (
	"sort"
	"time"
)

// hoursPerMonth is the flow→month extrapolation base (365 days / 12 months ×
// 24h). Ingest is a byte flow measured over Usage.Window; a monthly showback
// bill scales it by hoursPerMonth / window.
const hoursPerMonth = 730.0

// bytesPerGB converts raw byte counts to the GB the pricing rates are in.
const bytesPerGB = 1e9

// Usage is measured resource consumption over a window — the input the cost
// model prices. Pollers (Mimir/Loki/Tempo/S3) populate it behind interfaces;
// this package never sees a backend client (hexagonal, architecture rule 1).
type Usage struct {
	// Window is the period the ingest byte counts were measured over; it sets
	// the extrapolation to a monthly bill. Active series and storage are
	// point-in-time gauges and do not scale with it.
	Window  time.Duration
	Streams []Stream
	Storage []StorageObject
}

// Stream is per-(service, team, signal) ingest and active-series usage.
type Stream struct {
	Service      string
	Team         string // from the configurable team label; "" when unset
	Signal       string // metrics | logs | traces
	IngestBytes  int64  // bytes ingested over Usage.Window
	ActiveSeries int64  // metrics only; 0 for logs/traces
}

// StorageObject is object-storage bytes held in one storage class.
type StorageObject struct {
	Class string
	Bytes int64
}

// Report is the attributed monthly cost breakdown — the showback document.
type Report struct {
	Currency     string
	Lines        []Line        // per (service, team, signal), sorted
	Storage      []StorageLine // per storage class, sorted
	TotalMonthly float64
}

// Line is one service/signal's attributed monthly cost.
type Line struct {
	Service             string
	Team                string
	Signal              string
	IngestMonthly       float64
	ActiveSeriesMonthly float64
	TotalMonthly        float64
}

// StorageLine is one storage class's monthly cost.
type StorageLine struct {
	Class   string
	GB      float64
	Monthly float64
}

type lineKey struct{ service, team, signal string }

// Price attributes monthly cost. Deterministic and pure: same Usage + Pricing
// always yields the same Report, including line ordering.
func Price(p *Pricing, u Usage) Report {
	factor := 1.0
	if u.Window > 0 {
		factor = hoursPerMonth / u.Window.Hours()
	}

	agg := map[lineKey]*Line{}
	for _, s := range u.Streams {
		k := lineKey{s.Service, s.Team, s.Signal}
		l, ok := agg[k]
		if !ok {
			l = &Line{Service: s.Service, Team: s.Team, Signal: s.Signal}
			agg[k] = l
		}
		ingestGB := float64(s.IngestBytes) / bytesPerGB
		l.IngestMonthly += ingestGB * p.Ingest.RateFor(s.Signal) * factor
		l.ActiveSeriesMonthly += float64(s.ActiveSeries) / 1e6 * p.ActiveSeries.PerMillion
	}

	r := Report{Currency: p.Currency}
	for _, l := range agg {
		l.TotalMonthly = l.IngestMonthly + l.ActiveSeriesMonthly
		r.TotalMonthly += l.TotalMonthly
		r.Lines = append(r.Lines, *l)
	}
	sort.Slice(r.Lines, func(i, j int) bool { return lineLess(r.Lines[i], r.Lines[j]) })

	storAgg := map[string]float64{}
	for _, o := range u.Storage {
		storAgg[o.Class] += float64(o.Bytes)
	}
	for class, bytes := range storAgg {
		gb := bytes / bytesPerGB
		monthly := gb * p.Storage.RateFor(class)
		r.Storage = append(r.Storage, StorageLine{Class: class, GB: gb, Monthly: monthly})
		r.TotalMonthly += monthly
	}
	sort.Slice(r.Storage, func(i, j int) bool { return r.Storage[i].Class < r.Storage[j].Class })

	return r
}

func lineLess(a, b Line) bool {
	if a.Service != b.Service {
		return a.Service < b.Service
	}
	if a.Signal != b.Signal {
		return a.Signal < b.Signal
	}
	return a.Team < b.Team
}
