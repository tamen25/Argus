package main

import (
	"context"
	"errors"
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
	"github.com/tamen25/Argus/engine/internal/rules"
)

func newServeCmd() *cobra.Command {
	var (
		addr     string
		otlpAddr string
		rulesDir string
		interval time.Duration
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the argus engine: OTLP receiver + /metrics score export + /healthz",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), serveConfig{addr: addr, otlpAddr: otlpAddr, rulesDir: rulesDir, interval: interval})
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "HTTP listen address (/healthz, /metrics)")
	cmd.Flags().StringVar(&otlpAddr, "otlp-grpc", "", "OTLP gRPC listen address (e.g. :4317); empty disables ingest")
	cmd.Flags().StringVar(&rulesDir, "rules", "rules", "rules directory (used when ingest is enabled)")
	cmd.Flags().DurationVar(&interval, "score-interval", 30*time.Second, "how often scores are recomputed and exported")
	return cmd
}

type serveConfig struct {
	addr, otlpAddr, rulesDir string
	interval                 time.Duration
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
		rs, err := rules.LoadDir(cfg.rulesDir+"/spec", cfg.rulesDir+"/argus")
		if err != nil {
			return err
		}
		eng, err := rules.NewEngine(rs)
		if err != nil {
			return err
		}
		col := rules.NewCollector(eng)
		pipe := ingest.NewPipeline(col, ingest.NewCardinalityTracker(ingest.DefaultMaxTrackedPairs))
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
					pipe.CardinalityRows()
					prom.Update(col.Snapshot())
				}
			}
		}()
	}

	srv := &http.Server{Addr: cfg.addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
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
