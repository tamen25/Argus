package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

	"github.com/tamen25/Argus/engine/internal/cost"
	"github.com/tamen25/Argus/engine/internal/export"
	"github.com/tamen25/Argus/engine/internal/ingest"
	"github.com/tamen25/Argus/engine/internal/remediate"
	"github.com/tamen25/Argus/engine/internal/report"
	"github.com/tamen25/Argus/engine/internal/rules"
	"github.com/tamen25/Argus/engine/internal/telemetry"
)

func newServeCmd() *cobra.Command {
	var (
		addr        string
		otlpAddr    string
		rulesDir    string
		specVerFile string
		noAuth      bool
		interval    time.Duration
		maxPairs    int
		window      time.Duration
		cost        serveCostConfig
		bt          serveBacktestConfig
		bn          serveBenchConfig
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the argus engine: OTLP receiver + /metrics score export + /healthz + /api/cost",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), serveConfig{
				addr: addr, otlpAddr: otlpAddr, rulesDir: rulesDir,
				specVersionFile: specVerFile, insecureNoAuth: noAuth,
				interval: interval, maxPairs: maxPairs, window: window,
				cost: cost, backtest: bt, bench: bn,
			})
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "HTTP listen address (/healthz, /metrics, /api/report, /api/aggregates, /api/servicegraph, /api/cost, /api/backtest, /api/bench)")
	cmd.Flags().BoolVar(&noAuth, "insecure-no-auth", false,
		"serve /api without "+apiTokenEnv+" on a non-loopback --addr (only where the network itself keeps callers out)")
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

	// Bench endpoint (Phase 4): serves /api/bench — the leaderboard, and the
	// condition comparison — from a directory of `bench run --format json`
	// reports. Unconfigured, /api/bench 404s and the plugin says so.
	f.StringVar(&bn.reportsDir, "bench-reports", "", "directory of bench run reports (JSON) enabling the /api/bench endpoint")
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
	// insecureNoAuth allows an open API on a non-loopback address.
	insecureNoAuth bool
	interval       time.Duration
	maxPairs       int
	window         time.Duration
	cost           serveCostConfig
	backtest       serveBacktestConfig
	bench          serveBenchConfig
}

// validate checks the duration flags. They are checked whether or not the
// feature they belong to is enabled: the defaults are valid, so a bad value
// was typed, and is wrong either way.
func (cfg serveConfig) validate() error {
	return errors.Join(
		positive("score-interval", cfg.interval),
		positive("cardinality-window", cfg.window),
		positive("cost-window", cfg.cost.window),
		nonNegative("cost-cache-ttl", cfg.cost.cacheTTL),
		positive("backtest-window", cfg.backtest.window),
		positive("backtest-step", cfg.backtest.step),
		nonNegative("backtest-grace", cfg.backtest.grace),
		nonNegative("backtest-cache-ttl", cfg.backtest.cacheTTL),
	)
}

// serve runs the HTTP endpoints (and, when configured, the OTLP receiver and
// periodic score export) until ctx is cancelled or SIGINT/SIGTERM.
//
// Every route is registered as "GET /path" (Go 1.22 method patterns): the API
// is read-only, so anything else is answered 405 by the mux itself.
func serve(ctx context.Context, cfg serveConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	// The cost, backtest and bench endpoints work without OTLP ingest. Each
	// one 404s when it is not configured, and the plugin shows that plainly.
	closeStore, err := registerCostEndpoint(ctx, mux, cfg.cost)
	if err != nil {
		return err
	}
	if closeStore != nil {
		defer closeStore()
	}
	if err := registerBacktestEndpoint(mux, cfg.backtest); err != nil {
		return err
	}
	if err := registerBenchEndpoint(mux, cfg.bench); err != nil {
		return err
	}

	if cfg.otlpAddr != "" {
		stopScoring, err := startScoring(ctx, cfg, mux, reg)
		if err != nil {
			return err
		}
		defer stopScoring()
	}
	token := os.Getenv(apiTokenEnv)
	if err := checkAuthBoundary(cfg.addr, token, cfg.insecureNoAuth); err != nil {
		return err
	}
	return serveHTTP(ctx, cfg.addr, requireAPIToken(mux, token))
}

// apiTokenEnv names the variable holding the engine API's bearer token. The
// Helm chart sets it from a generated Secret (master plan §5.3: the API
// requires a token; the plugin backend is its only documented caller).
const apiTokenEnv = "ARGUS_API_TOKEN" //nolint:gosec // the variable's name, not a credential

// checkAuthBoundary refuses to serve the API without a token on an address
// other machines can reach. The API returns findings, evidence and cost data,
// and the master plan requires a token for it. Without one, serve still runs
// on loopback (a laptop, the CI dogfood gate), and anywhere else only with
// --insecure-no-auth: an open API should be a choice someone made.
func checkAuthBoundary(addr, token string, insecureNoAuth bool) error {
	if token != "" || insecureNoAuth || isLoopback(addr) {
		return nil
	}
	return fmt.Errorf("%s is not set and --addr %s accepts connections from other machines: "+
		"set the token, listen on 127.0.0.1, or pass --insecure-no-auth to serve the API open on purpose", apiTokenEnv, addr)
}

// isLoopback reports whether a listen address only accepts local connections.
// ":8080" (no host) listens on every interface, so it is not.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// requireAPIToken guards every /api/ route with the bearer token. /healthz
// and /metrics stay open for probes and Prometheus; they carry no findings.
// Without a token the API is open, and the engine says so at startup.
func requireAPIToken(next http.Handler, token string) http.Handler {
	if token == "" {
		log.Printf("warning: %s is not set: /api endpoints are unauthenticated", apiTokenEnv)
		return next
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") &&
			subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="argus"`)
			http.Error(w, "missing or wrong API token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// startScoring starts live scoring: the OTLP receiver feeding the rule
// engine, the /api endpoints that read its state, the engine's own telemetry,
// and the loop that exports scores to /metrics every interval. The returned
// function stops all of it.
func startScoring(ctx context.Context, cfg serveConfig, mux *http.ServeMux, reg *prometheus.Registry) (stop func(), err error) {
	rs, err := loadRules(cfg.rulesDir)
	if err != nil {
		return nil, err
	}
	eng, err := rules.NewEngine(rs)
	if err != nil {
		return nil, err
	}
	col := rules.NewCollector(eng, rules.CollectorOptions{Window: cfg.window})
	pipe := ingest.NewPipeline(col, ingest.TrackerOpts{MaxPairs: cfg.maxPairs, Window: cfg.window})
	export.RegisterAggregateStats(reg, pipe.PairsTracked, pipe.Evictions)
	export.RegisterItemStats(reg, pipe.ItemsConsumed)
	api := scoringAPI{col: col, pipe: pipe, rules: rs, specVersion: readSpecVersion(cfg.specVersionFile), window: cfg.window}
	if cfg.cost.pricingPath != "" {
		// Already validated by registerCostEndpoint; loaded again here so the
		// report can price its findings.
		if api.pricing, err = cost.LoadPricing(cfg.cost.pricingPath); err != nil {
			return nil, err
		}
	}
	registerAPI(mux, api)

	lis, err := new(net.ListenConfig).Listen(ctx, "tcp", cfg.otlpAddr)
	if err != nil {
		return nil, err
	}
	grpcSrv := ingest.NewGRPCServer(pipe)
	go func() { _ = grpcSrv.Serve(lis) }()

	// Self-instrumentation (dogfooding, §3.4): exports only when
	// OTEL_EXPORTER_OTLP_ENDPOINT is set, typically to another argus scoring
	// this one (the CI dogfood gate does exactly that).
	endpoint, insecure := otlpEndpointFromEnv()
	tel, err := telemetry.Setup(ctx, telemetry.Config{
		Endpoint:       endpoint,
		Insecure:       insecure,
		ServiceName:    "argus-engine",
		ServiceVersion: version,
		Environment:    os.Getenv("ARGUS_ENVIRONMENT"),
		ExportInterval: cfg.interval,
	})
	if err != nil {
		grpcSrv.GracefulStop()
		return nil, err
	}

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

	return func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tel.Shutdown(shCtx)
		grpcSrv.GracefulStop()
	}, nil
}

// otlpEndpointFromEnv reads OTEL_EXPORTER_OTLP_ENDPOINT. The OTel convention
// allows a URL, while the gRPC exporter wants host:port and a separate
// transport choice, and the scheme is that choice: https:// means TLS. A bare
// host:port, or http://, is plaintext, the setting for a mirror inside a
// cluster. OTEL_EXPORTER_OTLP_INSECURE=true forces plaintext, as in the SDKs.
//
// The scheme used to be stripped from both, so an https:// endpoint was sent
// plaintext.
func otlpEndpointFromEnv() (endpoint string, insecure bool) {
	ep := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	forced := strings.EqualFold(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"), "true")
	if rest, ok := strings.CutPrefix(ep, "https://"); ok {
		return strings.TrimSuffix(rest, "/"), forced
	}
	return strings.TrimSuffix(strings.TrimPrefix(ep, "http://"), "/"), true
}

// registerAPI mounts the JSON endpoints backing the soak harness and the
// Grafana plugin. All of them are read-only: none may evaluate anything into
// the collector.
func registerAPI(mux *http.ServeMux, api scoringAPI) {
	mux.HandleFunc("GET /api/report", reportHandler(api))
	mux.HandleFunc("GET /api/aggregates", aggregatesHandler(api.pipe))
	mux.HandleFunc("GET /api/servicegraph", serviceGraphHandler(api.col, api.pipe, api.window))
	mux.HandleFunc("GET /api/remediation", remediationHandler(api.col, api.rules))
}

// scoringAPI is what the live-scoring endpoints read.
type scoringAPI struct {
	col         *rules.Collector
	pipe        *ingest.Pipeline
	rules       []*rules.Rule
	specVersion string
	window      time.Duration
	// pricing, when set, prices the report's findings (--cost-pricing).
	pricing *cost.Pricing
}

// reportHandler serves /api/report: the same envelope the score CLI writes,
// built from live state. The soak harness snapshots it hourly.
func reportHandler(api scoringAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		snap := api.col.Snapshot()
		if api.pricing != nil {
			cost.PriceFindings(snap, api.rules, api.pricing)
		}
		notes := ingestNotes(api.pipe.Evictions(), snap, true)
		writeJSON(w, &report.Report{
			GeneratedAt:     time.Now().UTC(),
			ArgusVersion:    version,
			SpecVersion:     api.specVersion,
			Window:          api.window.String(),
			RuleSetComplete: false, // the official rule set is implemented in part
			Notes:           notes,
			Snapshot:        snap,
		})
	}
}

// aggregatesHandler serves /api/aggregates: the raw aggregate rows, which are
// `argus rules calibrate`'s input.
func aggregatesHandler(pipe *ingest.Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		rows := pipe.CurrentRows()
		if rows == nil {
			rows = []rules.AggregateRow{} // [] rather than null for the plugin
		}
		writeJSON(w, rows)
	}
}

type graphNode struct {
	Service string `json:"service"`
	// SpecScore is a pointer: a service can appear in an edge before it has
	// been scored.
	SpecScore *float64 `json:"spec_score,omitempty"`
	Findings  int      `json:"findings"`
}

type graphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Traces int64  `json:"traces"`
}

// serviceGraphHandler serves /api/servicegraph, the plugin's service graph:
// caller→callee edges (cross-service parent references resolved in the last
// completed trace generation) joined with each service's latest score.
func serviceGraphHandler(col *rules.Collector, pipe *ingest.Pipeline, window time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		snap := col.Snapshot()
		nodes := map[string]*graphNode{}
		for i := range snap.Services {
			svc := &snap.Services[i]
			score := svc.SpecScore
			nodes[svc.ServiceName] = &graphNode{Service: svc.ServiceName, SpecScore: &score, Findings: len(svc.Findings)}
		}

		edges := []graphEdge{}
		for _, row := range pipe.CurrentRows() {
			if row.Aggregate != "service_dependency" {
				continue
			}
			callee, _ := row.Fields["callee"].(string)
			traces, _ := row.Fields["traces"].(int64)
			edges = append(edges, graphEdge{Source: row.Service, Target: callee, Traces: traces})
			for _, s := range []string{row.Service, callee} {
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
			// Edges left out past the graph's cap (the busiest are kept).
			"edges_omitted": pipe.EdgesOmitted(),
		})
	}
}

// remediationHandler serves /api/remediation?rule=ID&service=NAME: the rule's
// patch template, rendered for a finding present in the CURRENT snapshot. Any
// other request is a 404, so the plugin can never show a patch for a problem
// Argus did not observe.
func remediationHandler(col *rules.Collector, rs []*rules.Rule) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ruleID, service := r.URL.Query().Get("rule"), r.URL.Query().Get("service")
		if ruleID == "" || service == "" {
			http.Error(w, "rule and service query params are required", http.StatusBadRequest)
			return
		}
		tmpl := remediationTemplate(rs, ruleID)
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
	}
}

// writeJSON writes v as indented JSON. It encodes before writing anything, so
// an encoding failure is a clean 500 rather than a 200 with half a body.
func writeJSON(w http.ResponseWriter, v any) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(append(body, '\n'))
}

// serveHTTP serves handler on addr until ctx is cancelled, then shuts down
// gracefully.
func serveHTTP(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		// Every route is a GET with no body worth waiting for.
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 2 * time.Minute,
		// No WriteTimeout: the first /api/backtest of a cache period replays a
		// week of history before it answers, which can take minutes.
	}
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
