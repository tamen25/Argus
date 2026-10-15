package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/pflag"

	"github.com/tamen25/Argus/engine/internal/cost"
	"github.com/tamen25/Argus/engine/internal/ingest/loki"
	"github.com/tamen25/Argus/engine/internal/ingest/mimir"
	"github.com/tamen25/Argus/engine/internal/ingest/objstore"
)

// costSourceConfig is the backend wiring shared by `argus cost` and the
// `serve` /api/cost endpoint: whichever of Mimir / Loki / S3 is configured.
type costSourceConfig struct {
	mimirURL, mimirTenant string
	lokiURL, lokiTenant   string
	// The label that names a service differs by backend: metrics that
	// arrive over OTLP carry service.name in Mimir as job, while Loki keeps
	// it as the service_name stream label.
	mimirServiceLabel, lokiServiceLabel string

	s3Bucket, s3Prefix, s3Region, s3Endpoint string
	s3PathStyle                              bool
}

// buildCostSources constructs the cost.Sources for whichever backends are
// configured, erroring when none are (a report with no source would read as a
// misleading $0).
func buildCostSources(ctx context.Context, cfg costSourceConfig) (cost.Sources, error) {
	var srcs cost.Sources
	if cfg.mimirURL != "" {
		srcs.Series = mimir.SeriesSource{Client: mimir.New(cfg.mimirURL, cfg.mimirTenant), Label: cfg.mimirServiceLabel}
	}
	if cfg.lokiURL != "" {
		srcs.Logs = loki.New(cfg.lokiURL, cfg.lokiTenant).WithServiceLabel(cfg.lokiServiceLabel)
	}
	if cfg.s3Bucket != "" {
		lister, err := objstore.NewS3Lister(ctx, objstore.S3Config{
			Bucket: cfg.s3Bucket, Prefix: cfg.s3Prefix, Region: cfg.s3Region,
			Endpoint: cfg.s3Endpoint, PathStyle: cfg.s3PathStyle,
		})
		if err != nil {
			return cost.Sources{}, fmt.Errorf("s3: %w", err)
		}
		srcs.Storage = objstore.StorageSource{Lister: lister}
	}
	if srcs.Series == nil && srcs.Logs == nil && srcs.Storage == nil {
		return cost.Sources{}, errors.New("configure at least one source: Mimir, Loki, or S3")
	}
	return srcs, nil
}

// addServiceLabelFlags registers the per-backend service label flags, named
// with prefix ("" for `argus cost`, "cost-" for `serve`), and the older single
// flag that set one label for both. That flag is kept, deprecated, so existing
// command lines still work; like any flag, a later one on the command line
// overrides it.
func addServiceLabelFlags(f *pflag.FlagSet, prefix string, cfg *costSourceConfig) {
	f.StringVar(&cfg.mimirServiceLabel, prefix+"mimir-service-label", "job",
		"Mimir label that names the service (OTLP metrics carry service.name as job)")
	f.StringVar(&cfg.lokiServiceLabel, prefix+"loki-service-label", "service_name",
		"Loki stream label that names the service")
	f.Var(bothServiceLabels{cfg}, prefix+"service-label", "set the Mimir and the Loki service label to one value")
	_ = f.MarkDeprecated(prefix+"service-label",
		"Mimir and Loki name services differently; use --"+prefix+"mimir-service-label and --"+prefix+"loki-service-label")
}

// bothServiceLabels is the deprecated single label flag: setting it sets both.
type bothServiceLabels struct{ cfg *costSourceConfig }

func (b bothServiceLabels) String() string { return "" }
func (b bothServiceLabels) Type() string   { return "string" }
func (b bothServiceLabels) Set(v string) error {
	b.cfg.mimirServiceLabel, b.cfg.lokiServiceLabel = v, v
	return nil
}
