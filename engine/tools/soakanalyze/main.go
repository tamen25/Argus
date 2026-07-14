// Command soakanalyze summarizes a scripts/soak.sh output directory:
// memory/rotation/error verdicts against the soak success criteria, plus the
// threshold-relevant distributions that feed `argus rules calibrate`.
//
// Usage: go run ./tools/soakanalyze <soak-output-dir>
package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tamen25/Argus/engine/internal/report"
	"github.com/tamen25/Argus/engine/internal/rules"
	"github.com/tamen25/Argus/engine/internal/stats"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: soakanalyze <soak-output-dir>")
		os.Exit(2)
	}
	out, err := analyze(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "soakanalyze:", err)
		os.Exit(1)
	}
	fmt.Print(out)
}

// flatGrowthPercent is the RSS growth (first→last quarter median) above
// which the bounded-memory verdict fails.
const flatGrowthPercent = 15

type sample struct {
	ts                                time.Time
	rss, pairs, evictions, itemsTotal float64
}

func analyze(dir string) (string, error) {
	samples, err := readMetrics(filepath.Join(dir, "metrics.csv"))
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Soak analysis — %s\n\n", filepath.Base(dir))
	fmt.Fprintf(&b, "## Run verdicts\n\n")
	writeMemoryVerdict(&b, samples)
	writeRotationVerdict(&b, samples)
	writeErrorVerdict(&b, dir)
	writeThroughput(&b, samples)

	if rows, name, err := lastJSON[[]rules.AggregateRow](dir, "aggregates-*.json"); err == nil {
		fmt.Fprintf(&b, "\n## Distributions (from %s)\n\n", name)
		writeAggregateDistributions(&b, *rows)
	} else {
		fmt.Fprintf(&b, "\n> no aggregates snapshots found: %v\n", err)
	}

	writeReportStats(&b, dir)
	return b.String(), nil
}

func readMetrics(path string) ([]sample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	var out []sample
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		num := func(name string) float64 {
			v, _ := strconv.ParseFloat(rec[col[name]], 64)
			return v
		}
		ts, _ := time.Parse(time.RFC3339, rec[col["ts"]])
		out = append(out, sample{
			ts:         ts,
			rss:        num("rss_bytes"),
			pairs:      num("pairs_tracked"),
			evictions:  num("evictions_total"),
			itemsTotal: num("items_traces") + num("items_metrics") + num("items_logs"),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no samples", path)
	}
	return out, nil
}

func writeMemoryVerdict(b *strings.Builder, s []sample) {
	q := len(s) / 4
	if q == 0 {
		q = 1
	}
	first, last := make([]float64, 0, q), make([]float64, 0, q)
	for _, x := range s[:q] {
		first = append(first, x.rss)
	}
	for _, x := range s[len(s)-q:] {
		last = append(last, x.rss)
	}
	f, l := stats.Median(first), stats.Median(last)
	growth := 0.0
	if f > 0 {
		growth = (l - f) / f * 100
	}
	verdict := "flat"
	if growth > flatGrowthPercent {
		verdict = fmt.Sprintf("GROWING beyond the %d%% bound — investigate before trusting bounded-memory claims", flatGrowthPercent)
	}
	fmt.Fprintf(b, "- memory: %s (rss median %.1fMB → %.1fMB, %+.1f%%)\n", verdict, f/1e6, l/1e6, growth)
}

func writeRotationVerdict(b *strings.Builder, s []sample) {
	rotated := false
	for i := 1; i < len(s); i++ {
		if s[i].pairs < s[i-1].pairs && s[i-1].pairs > 0 {
			rotated = true
			break
		}
	}
	if rotated {
		fmt.Fprintf(b, "- window rotation: observed (pairs_tracked sawtooth)\n")
	} else {
		fmt.Fprintf(b, "- window rotation: NOT observed — run shorter than the window, or rotation broken\n")
	}
	fmt.Fprintf(b, "- evictions (last): %.0f\n", s[len(s)-1].evictions)
}

func writeErrorVerdict(b *strings.Builder, dir string) {
	data, err := os.ReadFile(filepath.Join(dir, "engine-errors.log"))
	lines := 0
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			lines++
		}
	}
	if err != nil || lines == 0 {
		fmt.Fprintf(b, "- receiver errors: none recorded\n")
	} else {
		fmt.Fprintf(b, "- receiver errors: %d log lines — see engine-errors.log\n", lines)
	}
}

func writeThroughput(b *strings.Builder, s []sample) {
	first, last := s[0], s[len(s)-1]
	elapsed := last.ts.Sub(first.ts).Seconds()
	if elapsed <= 0 {
		return
	}
	fmt.Fprintf(b, "- items/sec (avg): %.1f over %s\n", (last.itemsTotal-first.itemsTotal)/elapsed, last.ts.Sub(first.ts))
}

// lastJSON decodes the lexicographically last match of pattern (zero-padded
// hour indexes make that the newest snapshot).
func lastJSON[T any](dir, pattern string) (*T, string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil || len(matches) == 0 {
		return nil, "", fmt.Errorf("no %s in %s", pattern, dir)
	}
	sort.Strings(matches)
	name := matches[len(matches)-1]
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, "", err
	}
	v := new(T)
	if err := json.Unmarshal(data, v); err != nil {
		return nil, "", fmt.Errorf("%s: %w", name, err)
	}
	return v, filepath.Base(name), nil
}

func writeAggregateDistributions(b *strings.Builder, rows []rules.AggregateRow) {
	field := func(r rules.AggregateRow, k string) float64 {
		v, _ := r.Fields[k].(float64)
		return v
	}

	type entry struct {
		label string
		val   float64
	}
	byAgg := map[string][]entry{}
	for _, r := range rows {
		switch r.Aggregate {
		case "span_name_cardinality":
			byAgg[r.Aggregate] = append(byAgg[r.Aggregate], entry{r.Service, field(r, "cardinality")})
		case "metric_attribute_cardinality":
			label := fmt.Sprintf("%s/%v/%v", r.Service, r.Fields["metric"], r.Fields["attribute"])
			byAgg[r.Aggregate] = append(byAgg[r.Aggregate], entry{label, field(r, "cardinality")})
		}
	}
	for _, agg := range []string{"span_name_cardinality", "metric_attribute_cardinality"} {
		es := byAgg[agg]
		if len(es) == 0 {
			continue
		}
		vals := make([]float64, len(es))
		top := es[0]
		for i, e := range es {
			vals[i] = e.val
			if e.val > top.val {
				top = e
			}
		}
		fmt.Fprintf(b, "- %s: n=%d · median %.0f · P90 %.0f · P99 %.0f · max %.0f (top: %s)\n",
			agg, len(es), stats.Median(vals), stats.Percentile(vals, 90), stats.Percentile(vals, 99), stats.Max(vals), top.label)
	}

	// resource attrs: one line per attribute key (ARG-RES-004 input)
	resVals := map[string][]float64{}
	for _, r := range rows {
		if r.Aggregate == "resource_attr_cardinality" {
			k := fmt.Sprintf("%v", r.Fields["attribute"])
			resVals[k] = append(resVals[k], field(r, "cardinality"))
		}
	}
	var resKeys []string
	for k := range resVals {
		resKeys = append(resKeys, k)
	}
	sort.Strings(resKeys)
	for _, k := range resKeys {
		v := resVals[k]
		fmt.Fprintf(b, "- resource attr %s: services %d · median %.0f · max %.0f\n", k, len(v), stats.Median(v), stats.Max(v))
	}

	// exemplar coverage (ARG-MET-001 input)
	withEx, total := 0, 0
	for _, r := range rows {
		if r.Aggregate == "exemplar_coverage" {
			total++
			if field(r, "with_exemplars") > 0 {
				withEx++
			}
		}
	}
	if total > 0 {
		fmt.Fprintf(b, "- exemplar coverage: %d/%d services with ≥1 exemplar\n", withEx, total)
	}

	// trace health ratios (SPA-002 / ARG-SPA-002 input)
	for _, k := range []string{"orphan_ratio", "missing_root_ratio"} {
		var vals []float64
		for _, r := range rows {
			if r.Aggregate == "trace_health" {
				vals = append(vals, field(r, k))
			}
		}
		if len(vals) > 0 {
			fmt.Fprintf(b, "- %s: services %d · median %.2f · P90 %.2f · max %.2f\n",
				k, len(vals), stats.Median(vals), stats.Percentile(vals, 90), stats.Max(vals))
		}
	}
}

// writeReportStats aggregates per-rule violation ratios across every hourly
// report; failing services only — passing services carry no stats, so these
// are distributions of the observed failures, not the whole fleet.
func writeReportStats(b *strings.Builder, dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, "report-*.json"))
	if len(matches) == 0 {
		return
	}
	sort.Strings(matches)

	ratios := map[string][]float64{}
	var lastRep report.Report
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var rep report.Report
		if err := json.Unmarshal(data, &rep); err != nil || rep.Snapshot == nil {
			continue
		}
		lastRep = rep
		for _, s := range rep.Snapshot.Services {
			for _, f := range s.Findings {
				ratios[f.RuleID] = append(ratios[f.RuleID], f.Stats.Ratio)
			}
		}
	}
	if lastRep.Snapshot == nil {
		return
	}

	fmt.Fprintf(b, "\n## Rule ratios across reports (failing services only)\n\n")
	lastCounts := map[string]int{}
	for _, s := range lastRep.Snapshot.Services {
		for _, f := range s.Findings {
			lastCounts[f.RuleID]++
		}
	}
	var ids []string
	for id := range ratios {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		v := ratios[id]
		noun := "services"
		if lastCounts[id] == 1 {
			noun = "service"
		}
		fmt.Fprintf(b, "- %s: %d %s (last report) · ratio median %.2f · max %.2f\n",
			id, lastCounts[id], noun, stats.Median(v), stats.Max(v))
	}
	fmt.Fprintf(b, "\nfleet score (last): %.1f\n", lastRep.Snapshot.FleetScore)
}
