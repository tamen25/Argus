package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/export"
	"github.com/tamen25/Argus/engine/internal/ingest"
	"github.com/tamen25/Argus/engine/internal/remediate"
	"github.com/tamen25/Argus/engine/internal/report"
	"github.com/tamen25/Argus/engine/internal/rules"
	"github.com/tamen25/Argus/engine/internal/rules/builtin"
	"github.com/tamen25/Argus/engine/internal/telemetry"
)

func newServeCmd() *cobra.Command {
	var (
		addr        string
		otlpAddr    string
		rulesDir    string
		specVerFile string
		interval    time.Duration
		maxPairs    int
		window      time.Duration
		cost        serveCostConfig
		bt          serveBacktestConfig
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the argus engine: OTLP receiver + /metrics score export + /healthz + /api/cost",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), serveConfig{
				addr: addr, otlpAddr: otlpAddr, rulesDir: rulesDir,
				specVersionFile: specVerFile,
				interval:        interval, maxPairs: maxPairs, window: window,
				cost: cost, backtest: bt,
			})
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "HTTP listen address (/healthz, /metrics, /api/report, /api/aggregates, /api/servicegraph, /api/cost, /api/backtest)")
	cmd.Flags().StringVar(&specVerFile, "spec-version-file", ".instrumentation-score-version", "file with the pinned Instrumentation Score spec version, echoed in reports")
	cmd.Flags().StringVar(&otlpAddr, "otlp-grpc", "", "OTLP gRPC listen address (e.g. :4317); empty disables ingest")
	cmd.Flags().StringVar(&rulesDir, "rules", "", "extra rule YAML directory overriding/extending built-ins")
	cmd.Flags().DurationVar(&interval, "score-interval", 30*time.Second, "how often scores are recomputed and exported")
	cmd.Flags().IntVar(&maxPairs, "max-tracked-pairs", ingest.DefaultMaxTrackedPairs, "cardinality sketch pair cap per window generation (LRU eviction beyond)")
	cmd.Flags().DurationVar(&window, "cardinality-window", ingest.DefaultWindow, "tumbling window for cardinality aggregates")

	// Cost showback endpoint (Phase 2): serves /api/cost when --cost-pricing is
	// set; otherwise /api/cost 404s and the plugin shows "not configured".
	f := cmd.Flags()
	f.StringVar(&cost.pricingPath, "cost-pricing", "", "pricing.yaml enabling the /api/cost showback endpoint")
	f.DurationVar(&cost.window, "cost-window", time.Hour, "measurement window for cost ingest-rate extrapolation")
	f.DurationVar(&cost.cacheTTL, "cost-cache-ttl", time.Minute, "how long /api/cost caches a showback before recomputing")
	f.StringVar(&cost.storeDSN, "cost-store-dsn", "", "Postgres DSN: persist cost snapshots and trend week-over-week")
	f.StringVar(&cost.mimirURL, "cost-mimir-url", "", "Mimir base URL for active-series attribution")
	f.StringVar(&cost.mimirTenant, "cost-mimir-tenant", "", "Mimir X-Scope-OrgID")
	f.StringVar(&cost.lokiURL, "cost-loki-url", "", "Loki base URL for log-bytes attribution")
	f.StringVar(&cost.lokiTenant, "cost-loki-tenant", "", "Loki X-Scope-OrgID")
	f.StringVar(&cost.serviceLabel, "cost-service-label", "service_name", "label used to attribute cost by service")
	f.StringVar(&cost.s3Bucket, "cost-s3-bucket", "", "object-storage bucket to inventory")
	f.StringVar(&cost.s3Prefix, "cost-s3-prefix", "", "key prefix to scope the inventory")
	f.StringVar(&cost.s3Region, "cost-s3-region", "", "AWS region (empty uses the default chain)")
	f.StringVar(&cost.s3Endpoint, "cost-s3-endpoint", "", "custom S3 endpoint (e.g. MinIO)")
	f.BoolVar(&cost.s3PathStyle, "cost-s3-path-style", false, "use path-style addressing (MinIO)")

	// Backtest endpoint (Phase 3): serves /api/backtest when --backtest-rules
	// and --backtest-mimir-url are set; otherwise /api/backtest 404s and the
	// plugin shows "not configured". Replays a rolling window against history.
	f.StringSliceVar(&bt.rulePaths, "backtest-rules", nil, "alert rule file(s) enabling the /api/backtest endpoint (repeatable)")
	f.StringVar(&bt.sloPath, "backtest-slo", "", "SLO policy file — burn-rate rules replayed alongside --backtest-rules")
	f.StringVar(&bt.incidentsPath, "backtest-incidents", "incidents.yaml", "incident registry (ground truth)")
	f.StringVar(&bt.mimirURL, "backtest-mimir-url", "", "Mimir base URL for replay (instant-query API)")
	f.StringVar(&bt.mimirTenant, "backtest-mimir-tenant", "", "Mimir X-Scope-OrgID")
	f.DurationVar(&bt.window, "backtest-window", 7*24*time.Hour, "rolling lookback replayed by /api/backtest")
	f.DurationVar(&bt.step, "backtest-step", time.Minute, "replay evaluation step")
	f.DurationVar(&bt.grace, "backtest-grace", 5*time.Minute, "incident attribution margin")
	f.StringVar(&bt.probeExpr, "backtest-probe-expr", "count(target_info)", "presence-probe expression for coverage mapping")
	f.BoolVar(&bt.synthesize, "backtest-synthesize", false, "inline defined recording rules during replay")
	f.DurationVar(&bt.cacheTTL, "backtest-cache-ttl", 15*time.Minute, "how long /api/backtest caches a report before recomputing")
	return cmd
}

// serveCostConfig holds the /api/cost wiring for serve.
type serveCostConfig struct {
	costSourceConfig
	pricingPath string
	window      time.Duration
	cacheTTL    time.Duration
	storeDSN    string
}

type serveConfig struct {
	addr, otlpAddr, rulesDir string
	specVersionFile          string
	interval                 time.Duration
	maxPairs                 int
	window                   time.Duration
	cost                     serveCostConfig
	backtest                 serveBacktestConfig
}

// serve runs the HTTP endpoints (and, when configured, the OTLP receiver and
// periodic score export) until ctx is cancelled or SIGINT/SIGTERM.
func serve(ctx context.Context, cfg serveConfig) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	// Cost showback endpoint (independent of OTLP ingest). Configured →
	// cached live report; unconfigured → 404 the plugin renders gracefully.
	if closeStore, err := registerCostEndpoint(ctx, mux, cfg.cost); err != nil {
		return err
	} else if closeStore != nil {
		defer closeStore()
	}

	// Backtest endpoint (independent of OTLP ingest). Configured → cached
	// rolling-window report; unconfigured → 404 the plugin renders gracefully.
	if err := registerBacktestEndpoint(mux, cfg.backtest); err != nil {
		return err
	}

	// Optional ingest + score export loop.
	if cfg.otlpAddr != "" {
		rs, err := builtin.Load()
		if err != nil {
			return err
		}
		if cfg.rulesDir != "" {
			custom, err := rules.LoadDir(cfg.rulesDir)
			if err != nil {
				return err
			}
			rs = rules.Merge(rs, custom)
		}
		eng, err := rules.NewEngine(rs)
		if err != nil {
			return err
		}
		col := rules.NewCollector(eng)
		pipe := ingest.NewPipeline(col, ingest.TrackerOpts{MaxPairs: cfg.maxPairs, Window: cfg.window})
		export.RegisterAggregateStats(reg, pipe.PairsTracked, pipe.Evictions)
		export.RegisterItemStats(reg, pipe.ItemsConsumed)
		registerAPI(mux, col, pipe, rs, readSpecVersion(cfg.specVersionFile), cfg.window)
		lis, err := net.Listen("tcp", cfg.otlpAddr)
		if err != nil {
			return err
		}
		grpcSrv := ingest.NewGRPCServer(pipe)
		go func() { _ = grpcSrv.Serve(lis) }()
		defer grpcSrv.GracefulStop()

		// Self-instrumentation (dogfooding, §3.4): exports only when
		// OTEL_EXPORTER_OTLP_ENDPOINT is set — typically pointed at another
		// argus scoring this one (the CI dogfood gate does exactly that).
		tel, err := telemetry.Setup(ctx, telemetry.Config{
			Endpoint:       strings.TrimPrefix(strings.TrimPrefix(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), "http://"), "https://"),
			ServiceName:    "argus-engine",
			ServiceVersion: version,
			Environment:    os.Getenv("ARGUS_ENVIRONMENT"),
			ExportInterval: cfg.interval,
		})
		if err != nil {
			return err
		}
		defer func() {
			shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tel.Shutdown(shCtx)
		}()

		prom := export.NewPrometheus(reg)
		go func() {
			t := time.NewTicker(cfg.interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					tickCtx, span := tel.Tracer.Start(ctx, "score.export")
					pipe.AggregateRows()
					prom.Update(col.Snapshot())
					tel.ExportTicks.Add(tickCtx, 1)
					span.End()
				}
			}
		}()
	}

	return serveHTTP(ctx, cfg.addr, mux)
}

// registerAPI mounts the JSON endpoints backing the soak harness and the
// Grafana plugin: /api/report (score-CLI-equivalent envelope from live state)
// and /api/aggregates (raw rows, calibrate input). Both are read-only: they
// must not evaluate anything into the collector.
func registerAPI(mux *http.ServeMux, col *rules.Collector, pipe *ingest.Pipeline, rs []*rules.Rule, specVersion string, window time.Duration) {
	mux.HandleFunc("/api/report", func(w http.ResponseWriter, _ *http.Request) {
		snap := col.Snapshot()
		var notes []string
		if n := pipe.Evictions(); n > 0 {
			notes = append(notes, fmt.Sprintf("aggregate trackers evicted %d entries (LRU) — estimates for evicted entries are lost", n))
		}
		if len(snap.Services) == 0 {
			notes = append(notes, "no telemetry received on the OTLP listener during the window — the fleet score reflects an empty fleet, not healthy instrumentation")
		}
		writeJSON(w, &report.Report{
			GeneratedAt:     time.Now().UTC(),
			ArgusVersion:    version,
			SpecVersion:     specVersion,
			Window:          window.String(),
			RuleSetComplete: false, // Phase 1 implements a subset of official rules
			Notes:           notes,
			Snapshot:        snap,
		})
	})
	mux.HandleFunc("/api/aggregates", func(w http.ResponseWriter, _ *http.Request) {
		rows := pipe.CurrentRows()
		if rows == nil {
			rows = []rules.AggregateRow{}
		}
		writeJSON(w, rows)
	})
	// /api/servicegraph joins caller→callee edges (resolved cross-service
	// parent references, completed trace generation only) with the latest
	// per-service scores — the plugin's service graph page. Scores are
	// pointers: a service can appear in an edge before it has been scored.
	mux.HandleFunc("/api/servicegraph", func(w http.ResponseWriter, _ *http.Request) {
		type graphNode struct {
			Service   string   `json:"service"`
			SpecScore *float64 `json:"spec_score,omitempty"`
			Findings  int      `json:"findings"`
		}
		type graphEdge struct {
			Source string `json:"source"`
			Target string `json:"target"`
			Traces int64  `json:"traces"`
		}
		snap := col.Snapshot()
		nodes := map[string]*graphNode{}
		for i := range snap.Services {
			svc := &snap.Services[i]
			score := svc.SpecScore
			nodes[svc.ServiceName] = &graphNode{Service: svc.ServiceName, SpecScore: &score, Findings: len(svc.Findings)}
		}
		edges := []graphEdge{}
		for _, r := range pipe.CurrentRows() {
			if r.Aggregate != "service_dependency" {
				continue
			}
			callee, _ := r.Fields["callee"].(string)
			traces, _ := r.Fields["traces"].(int64)
			edges = append(edges, graphEdge{Source: r.Service, Target: callee, Traces: traces})
			for _, s := range []string{r.Service, callee} {
				if _, ok := nodes[s]; !ok {
					nodes[s] = &graphNode{Service: s}
				}
			}
		}
		nodeList := make([]graphNode, 0, len(nodes))
		for _, n := range nodes {
			nodeList = append(nodeList, *n)
		}
		sort.Slice(nodeList, func(i, j int) bool { return nodeList[i].Service < nodeList[j].Service })
		writeJSON(w, map[string]any{
			"generated_at": time.Now().UTC(),
			"window":       window.String(),
			"nodes":        nodeList,
			"edges":        edges,
		})
	})
	// /api/remediation?rule=ID&service=NAME renders the rule's patch template
	// for a finding present in the CURRENT snapshot — 404 otherwise, so the
	// plugin can never show a patch for a problem Argus didn't observe.
	mux.HandleFunc("/api/remediation", func(w http.ResponseWriter, r *http.Request) {
		ruleID, service := r.URL.Query().Get("rule"), r.URL.Query().Get("service")
		if ruleID == "" || service == "" {
			http.Error(w, "rule and service query params are required", http.StatusBadRequest)
			return
		}
		var tmpl string
		for _, rr := range rs {
			if rr.ID == ruleID {
				tmpl = rr.Remediation.Template
			}
		}
		if tmpl == "" {
			http.Error(w, fmt.Sprintf("rule %q has no remediation template", ruleID), http.StatusNotFound)
			return
		}
		svc := col.Snapshot().Service(service)
		if svc == nil {
			http.Error(w, "service not observed", http.StatusNotFound)
			return
		}
		for _, f := range svc.Findings {
			if f.RuleID != ruleID {
				continue
			}
			outs, err := remediate.Render(tmpl, remediate.Context{Service: service, Finding: f})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{
				"rule_id": ruleID, "service": service, "template": tmpl, "formats": outs,
			})
			return
		}
		http.Error(w, "no such finding in the current snapshot", http.StatusNotFound)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func serveHTTP(ctx context.Context, addr string, mux *http.ServeMux) error {
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		// ListenAndServe returns ErrServerClosed after a clean Shutdown; that
		// is the expected path, not an error.
		if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
