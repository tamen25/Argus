package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/cost"
	"github.com/tamen25/Argus/engine/internal/ingest/loki"
	"github.com/tamen25/Argus/engine/internal/ingest/mimir"
	"github.com/tamen25/Argus/engine/internal/ingest/objstore"
	"github.com/tamen25/Argus/engine/internal/store"
)

// errOverBudget marks the cost CI-gate failure (--fail-over-monthly).
var errOverBudget = errors.New("monthly cost over budget")

type costOptions struct {
	pricingPath string
	window      time.Duration
	output      string
	outPath     string
	storeDSN    string
	failOver    float64

	mimirURL, mimirTenant string
	lokiURL, lokiTenant   string
	serviceLabel          string

	s3Bucket, s3Prefix, s3Region, s3Endpoint string
	s3PathStyle                              bool
}

func newCostCmd() *cobra.Command {
	opts := &costOptions{}
	cmd := &cobra.Command{
		Use:   "cost",
		Short: "Attribute and price your LGTM spend (showback) from backend usage",
		Long: `Gathers usage from the configured backends (Mimir active series, Loki log
bytes, S3/MinIO storage inventory), prices it against a pricing.yaml, models
storage lifecycle savings, and — with --store-dsn — reports week-over-week
trends. Costs are modeled from your rates, not billed.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sb, err := runCost(cmd.Context(), opts)
			if err != nil {
				return err
			}
			out, err := renderShowback(sb, opts.output)
			if err != nil {
				return err
			}
			if opts.outPath != "" {
				if err := os.WriteFile(opts.outPath, []byte(out), 0o644); err != nil {
					return err
				}
			} else {
				cmd.Print(out)
			}
			if opts.failOver > 0 && sb.Report.TotalMonthly > opts.failOver {
				return fmt.Errorf("%w: %.2f > %.2f", errOverBudget, sb.Report.TotalMonthly, opts.failOver)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.pricingPath, "pricing", "", "path to pricing.yaml (required)")
	f.DurationVar(&opts.window, "window", time.Hour, "measurement window for ingest-rate extrapolation")
	f.StringVar(&opts.output, "output", "md", "output format: md | json")
	f.StringVar(&opts.outPath, "out", "", "write the report to this file instead of stdout")
	f.StringVar(&opts.storeDSN, "store-dsn", "", "Postgres DSN: persist this snapshot and trend against the last one")
	f.Float64Var(&opts.failOver, "fail-over-monthly", 0, "exit non-zero when total monthly cost exceeds this (CI budget gate)")
	f.StringVar(&opts.mimirURL, "mimir-url", "", "Mimir base URL for active-series attribution")
	f.StringVar(&opts.mimirTenant, "mimir-tenant", "", "Mimir X-Scope-OrgID")
	f.StringVar(&opts.lokiURL, "loki-url", "", "Loki base URL for log-bytes attribution")
	f.StringVar(&opts.lokiTenant, "loki-tenant", "", "Loki X-Scope-OrgID")
	f.StringVar(&opts.serviceLabel, "service-label", "service_name", "label/stream selector used to attribute by service")
	f.StringVar(&opts.s3Bucket, "s3-bucket", "", "object-storage bucket to inventory")
	f.StringVar(&opts.s3Prefix, "s3-prefix", "", "key prefix to scope the inventory")
	f.StringVar(&opts.s3Region, "s3-region", "", "AWS region (empty uses the default chain)")
	f.StringVar(&opts.s3Endpoint, "s3-endpoint", "", "custom S3 endpoint (e.g. MinIO); empty for AWS")
	f.BoolVar(&opts.s3PathStyle, "s3-path-style", false, "use path-style addressing (MinIO)")
	_ = cmd.MarkFlagRequired("pricing")
	return cmd
}

func runCost(ctx context.Context, opts *costOptions) (cost.Showback, error) {
	pricing, err := cost.LoadPricing(opts.pricingPath)
	if err != nil {
		return cost.Showback{}, err
	}

	var srcs cost.Sources
	if opts.mimirURL != "" {
		srcs.Series = mimir.SeriesSource{Client: mimir.New(opts.mimirURL, opts.mimirTenant), Label: opts.serviceLabel}
	}
	if opts.lokiURL != "" {
		srcs.Logs = loki.New(opts.lokiURL, opts.lokiTenant).WithServiceLabel(opts.serviceLabel)
	}
	if opts.s3Bucket != "" {
		lister, err := objstore.NewS3Lister(ctx, objstore.S3Config{
			Bucket: opts.s3Bucket, Prefix: opts.s3Prefix, Region: opts.s3Region,
			Endpoint: opts.s3Endpoint, PathStyle: opts.s3PathStyle,
		})
		if err != nil {
			return cost.Showback{}, fmt.Errorf("s3: %w", err)
		}
		srcs.Storage = objstore.StorageSource{Lister: lister}
	}
	if srcs.Series == nil && srcs.Logs == nil && srcs.Storage == nil {
		return cost.Showback{}, errors.New("configure at least one source: --mimir-url, --loki-url, or --s3-bucket")
	}

	var snapStore cost.SnapshotStore
	if opts.storeDSN != "" {
		pg, err := store.Open(ctx, opts.storeDSN)
		if err != nil {
			return cost.Showback{}, fmt.Errorf("store: %w", err)
		}
		defer pg.Close()
		snapStore = pg
	}

	return cost.Assemble(ctx, pricing, srcs, opts.window, snapStore, time.Now())
}

func renderShowback(sb cost.Showback, format string) (string, error) {
	switch format {
	case "json":
		b, err := cost.RenderJSON(sb)
		if err != nil {
			return "", err
		}
		return string(b) + "\n", nil
	case "md", "":
		return cost.RenderMarkdown(sb), nil
	default:
		return "", fmt.Errorf("unknown output format %q (want md or json)", format)
	}
}
