package main

import (
	"context"
	"errors"
	"fmt"

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
	serviceLabel          string

	s3Bucket, s3Prefix, s3Region, s3Endpoint string
	s3PathStyle                              bool
}

// buildCostSources constructs the cost.Sources for whichever backends are
// configured, erroring when none are (a report with no source would read as a
// misleading $0).
func buildCostSources(ctx context.Context, cfg costSourceConfig) (cost.Sources, error) {
	var srcs cost.Sources
	if cfg.mimirURL != "" {
		srcs.Series = mimir.SeriesSource{Client: mimir.New(cfg.mimirURL, cfg.mimirTenant), Label: cfg.serviceLabel}
	}
	if cfg.lokiURL != "" {
		srcs.Logs = loki.New(cfg.lokiURL, cfg.lokiTenant).WithServiceLabel(cfg.serviceLabel)
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
