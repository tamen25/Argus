package cost

import (
	"context"
	"sort"
	"time"
)

// Usage-source ports (hexagonal, architecture rule 1). Concrete clients live
// in adapter packages (ingest/mimir, ingest/loki, …) and satisfy these by
// method set — they never import this package. Every source is optional: a
// user with only Mimir still gets an active-series report.

// SeriesSource reports active series per service — Mimir's dominant cost
// driver (ingester memory).
type SeriesSource interface {
	ActiveSeriesByService(ctx context.Context) (map[string]int64, error)
}

// LogBytesSource reports log bytes ingested per service over a window (Loki
// `bytes_over_time`).
type LogBytesSource interface {
	LogBytesByService(ctx context.Context, window time.Duration) (map[string]int64, error)
}

// StorageSource reports object-storage bytes by storage class (S3/MinIO
// inventory).
type StorageSource interface {
	StorageBytesByClass(ctx context.Context) (map[string]int64, error)
}

// Sources bundles the optional usage inputs. A nil source is skipped.
type Sources struct {
	Series  SeriesSource
	Logs    LogBytesSource
	Storage StorageSource
}

// Gather composes the wired sources into a Usage over the window. Errors are
// returned, never swallowed: a partial report presented as complete is a lie
// (honest-reporting rule 7). Output ordering is deterministic.
func Gather(ctx context.Context, s Sources, window time.Duration) (Usage, error) {
	u := Usage{Window: window}

	if s.Series != nil {
		series, err := s.Series.ActiveSeriesByService(ctx)
		if err != nil {
			return Usage{}, err
		}
		for _, svc := range sortedKeys(series) {
			u.Streams = append(u.Streams, Stream{Service: svc, Signal: "metrics", ActiveSeries: series[svc]})
		}
	}

	if s.Logs != nil {
		logs, err := s.Logs.LogBytesByService(ctx, window)
		if err != nil {
			return Usage{}, err
		}
		for _, svc := range sortedKeys(logs) {
			u.Streams = append(u.Streams, Stream{Service: svc, Signal: "logs", IngestBytes: logs[svc]})
		}
	}

	if s.Storage != nil {
		stor, err := s.Storage.StorageBytesByClass(ctx)
		if err != nil {
			return Usage{}, err
		}
		for _, class := range sortedKeys(stor) {
			u.Storage = append(u.Storage, StorageObject{Class: class, Bytes: stor[class]})
		}
	}

	return u, nil
}

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
