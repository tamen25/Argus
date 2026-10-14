package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/ingest"
	"github.com/tamen25/Argus/engine/internal/ingest/mimir"
	"github.com/tamen25/Argus/engine/internal/report"
	"github.com/tamen25/Argus/engine/internal/rules"
	"github.com/tamen25/Argus/engine/internal/store"
)

// errBelowThreshold marks the CI-gate failure (non-zero exit, distinct from
// operational errors in the message).
var errBelowThreshold = errors.New("fleet score below threshold")

type scoreOptions struct {
	rulesDir        string
	listenOTLP      string
	window          time.Duration
	mimirURL        string
	mimirTenant     string
	output          string
	outPath         string
	failBelow       float64
	storeDSN        string
	specVersionFile string

	// test seam: pre-bound listener overrides listenOTLP
	listener net.Listener
}

func newScoreCmd() *cobra.Command {
	opts := &scoreOptions{}
	cmd := &cobra.Command{
		Use:   "score",
		Short: "Evaluate instrumentation quality and print an Instrumentation Score report",
		Long: `Collects a window of sampled OTLP telemetry (--listen-otlp) and/or polls
backends (--mimir-url), evaluates the built-in and custom rules, and reports
per-service Instrumentation Scores. Exit code 2 when --fail-below-score trips
(the CI gate use case), 1 on any other error.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rep, err := runScore(cmd.Context(), opts)
			if err != nil {
				return err
			}
			out, err := renderReport(rep, opts.output)
			if err != nil {
				return err
			}
			if err := writeOutput(cmd, opts.outPath, out); err != nil {
				return err
			}
			if opts.failBelow > 0 && rep.Snapshot.FleetScore < opts.failBelow {
				return fmt.Errorf("%w: %.2f < %.2f", errBelowThreshold, rep.Snapshot.FleetScore, opts.failBelow)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.rulesDir, "rules", "", "extra rule YAML directory; same-ID rules override built-ins, new IDs extend them")
	f.StringVar(&opts.listenOTLP, "listen-otlp", "", "OTLP gRPC listen address for the sampled mirror (e.g. :4317); empty disables")
	f.DurationVar(&opts.window, "window", 60*time.Second, "collection window when --listen-otlp is set")
	f.StringVar(&opts.mimirURL, "mimir-url", "", "Mimir base URL for poller verification (e.g. http://mimir-gateway.lgtm.svc)")
	f.StringVar(&opts.mimirTenant, "mimir-tenant", "", "Mimir tenant (X-Scope-OrgID); empty for single-tenant")
	f.StringVar(&opts.output, "output", "markdown", "report format: markdown|json")
	f.StringVar(&opts.outPath, "out", "", "write report to file instead of stdout")
	f.Float64Var(&opts.failBelow, "fail-below-score", 0, "exit non-zero when fleet score is below this value (CI gate)")
	f.StringVar(&opts.storeDSN, "store-dsn", "", "Postgres DSN; when set, snapshot and findings are persisted")
	f.StringVar(&opts.specVersionFile, "spec-version-file", ".instrumentation-score-version", "file holding the pinned spec version")
	return cmd
}

// runScore executes one collection+evaluation cycle: receive the sampled OTLP
// mirror for the window, verify what Mimir can see, and build the report
// (persisting it when a store is configured).
func runScore(ctx context.Context, opts *scoreOptions) (*report.Report, error) {
	rs, err := loadRules(opts.rulesDir)
	if err != nil {
		return nil, fmt.Errorf("loading rules: %w", err)
	}
	eng, err := rules.NewEngine(rs)
	if err != nil {
		return nil, err
	}
	col := rules.NewCollector(eng)
	pipe := ingest.NewPipeline(col, ingest.TrackerOpts{})

	listened, err := receiveOTLP(ctx, opts, pipe)
	if err != nil {
		return nil, err
	}
	pipe.AggregateRows()
	notes := ingestNotes(pipe.Evictions(), len(col.Snapshot().Services), listened)

	if opts.mimirURL != "" {
		var services []string
		for _, s := range col.Snapshot().Services {
			services = append(services, s.ServiceName)
		}
		poller := ingest.NewPoller(mimir.New(opts.mimirURL, opts.mimirTenant), eng)
		if err := poller.Run(ctx, col, services); err != nil {
			// A failed verification downgrades nothing: the sampled results stand.
			notes = append(notes, fmt.Sprintf("poller verification incomplete: %v", err))
		}
	}

	rep := &report.Report{
		GeneratedAt:     time.Now().UTC(),
		ArgusVersion:    version,
		SpecVersion:     readSpecVersion(opts.specVersionFile),
		Window:          opts.window.String(),
		RuleSetComplete: false, // the official rule set is implemented in part
		Notes:           notes,
		Snapshot:        col.Snapshot(),
	}
	if opts.storeDSN != "" {
		if err := persistSnapshot(ctx, opts.storeDSN, rep); err != nil {
			return nil, err
		}
	}
	return rep, nil
}

// receiveOTLP serves the OTLP receiver for the window (or until ctx ends),
// feeding the pipeline. It reports whether a listener ran at all: without
// --listen-otlp, scoring relies on the poller alone.
func receiveOTLP(ctx context.Context, opts *scoreOptions, pipe *ingest.Pipeline) (listened bool, err error) {
	lis := opts.listener
	if lis == nil && opts.listenOTLP != "" {
		if lis, err = new(net.ListenConfig).Listen(ctx, "tcp", opts.listenOTLP); err != nil {
			return false, err
		}
	}
	if lis == nil {
		return false, nil
	}
	srv := ingest.NewGRPCServer(pipe)
	done := make(chan struct{})
	go func() { _ = srv.Serve(lis); close(done) }()
	select {
	case <-time.After(opts.window):
	case <-ctx.Done():
	}
	srv.GracefulStop()
	<-done
	return true, nil
}

// ingestNotes are the report notes about the ingest itself, shared by `score`
// and `serve`: lost estimates, and an empty fleet, which scores a vacuous 100
// and would pass any gate silently.
func ingestNotes(evictions int64, services int, listened bool) []string {
	var notes []string
	if evictions > 0 {
		notes = append(notes, fmt.Sprintf("aggregate trackers evicted %d entries (LRU) — estimates for evicted entries are lost", evictions))
	}
	if listened && services == 0 {
		notes = append(notes, "no telemetry received on the OTLP listener during the window — the fleet score reflects an empty fleet, not healthy instrumentation")
	}
	return notes
}

// persistSnapshot saves the report's snapshot to Postgres.
func persistSnapshot(ctx context.Context, dsn string, rep *report.Report) error {
	st, err := store.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer st.Close()
	if _, err := st.SaveSnapshot(ctx, rep.Snapshot, store.Meta{
		TakenAt: rep.GeneratedAt, SpecVersion: rep.SpecVersion, ArgusVersion: version,
	}); err != nil {
		return fmt.Errorf("persisting snapshot: %w", err)
	}
	return nil
}

func renderReport(rep *report.Report, format string) ([]byte, error) {
	switch format {
	case "json":
		return report.JSON(rep)
	case "markdown", "md":
		return []byte(report.Markdown(rep)), nil
	default:
		return nil, fmt.Errorf("unknown output format %q (markdown|json)", format)
	}
}

// embeddedSpecVersion is set at build time via
// -ldflags "-X main.embeddedSpecVersion=<sha>" for environments without the
// repo pin file on disk (container images, `go install` builds).
var embeddedSpecVersion string

func readSpecVersion(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		if embeddedSpecVersion != "" {
			return embeddedSpecVersion
		}
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}
