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
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the argus engine: OTLP receiver + /metrics score export + /healthz",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), serveConfig{
				addr: addr, otlpAddr: otlpAddr, rulesDir: rulesDir,
				specVersionFile: specVerFile,
				interval:        interval, maxPairs: maxPairs, window: window,
			})
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "HTTP listen address (/healthz, /metrics, /api/report, /api/aggregates)")
	cmd.Flags().StringVar(&specVerFile, "spec-version-file", ".instrumentation-score-version", "file with the pinned Instrumentation Score spec version, echoed in reports")
	cmd.Flags().StringVar(&otlpAddr, "otlp-grpc", "", "OTLP gRPC listen address (e.g. :4317); empty disables ingest")
	cmd.Flags().StringVar(&rulesDir, "rules", "", "extra rule YAML directory overriding/extending built-ins")
	cmd.Flags().DurationVar(&interval, "score-interval", 30*time.Second, "how often scores are recomputed and exported")
	cmd.Flags().IntVar(&maxPairs, "max-tracked-pairs", ingest.DefaultMaxTrackedPairs, "cardinality sketch pair cap per window generation (LRU eviction beyond)")
	cmd.Flags().DurationVar(&window, "cardinality-window", ingest.DefaultWindow, "tumbling window for cardinality aggregates")
	return cmd
}

type serveConfig struct {
	addr, otlpAddr, rulesDir string
	specVersionFile          string
	interval                 time.Duration
	maxPairs                 int
	window                   time.Duration
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

		prom := export.NewPrometheus(reg)
		go func() {
			t := time.NewTicker(cfg.interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					pipe.AggregateRows()
					prom.Update(col.Snapshot())
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
