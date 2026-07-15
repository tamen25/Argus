package cost_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/cost"
)

type fakeSeries struct {
	m   map[string]int64
	err error
}

func (f fakeSeries) ActiveSeriesByService(context.Context) (map[string]int64, error) {
	return f.m, f.err
}

type fakeLogs struct {
	m   map[string]int64
	err error
}

func (f fakeLogs) LogBytesByService(context.Context, time.Duration) (map[string]int64, error) {
	return f.m, f.err
}

type fakeStorage struct {
	m   map[string]int64
	err error
}

func (f fakeStorage) StorageBytesByClass(context.Context) (map[string]int64, error) {
	return f.m, f.err
}

// Gather composes whatever sources are wired into a Usage: Mimir series become
// metrics streams, Loki bytes become logs streams, storage becomes objects.
func TestGatherComposesSources(t *testing.T) {
	s := cost.Sources{
		Series:  fakeSeries{m: map[string]int64{"checkout": 2_000_000, "cart": 500_000}},
		Logs:    fakeLogs{m: map[string]int64{"checkout": 3_000_000_000}},
		Storage: fakeStorage{m: map[string]int64{"STANDARD": 100_000_000_000}},
	}
	u, err := cost.Gather(context.Background(), s, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if u.Window != time.Hour {
		t.Errorf("window = %v", u.Window)
	}

	// checkout appears as a metrics stream (active series) and a logs stream
	var checkoutMetrics, checkoutLogs *cost.Stream
	for i := range u.Streams {
		s := &u.Streams[i]
		if s.Service == "checkout" && s.Signal == "metrics" {
			checkoutMetrics = s
		}
		if s.Service == "checkout" && s.Signal == "logs" {
			checkoutLogs = s
		}
	}
	if checkoutMetrics == nil || checkoutMetrics.ActiveSeries != 2_000_000 {
		t.Errorf("checkout metrics stream = %+v, want ActiveSeries 2e6", checkoutMetrics)
	}
	if checkoutLogs == nil || checkoutLogs.IngestBytes != 3_000_000_000 {
		t.Errorf("checkout logs stream = %+v, want IngestBytes 3e9", checkoutLogs)
	}
	if len(u.Storage) != 1 || u.Storage[0].Class != "STANDARD" || u.Storage[0].Bytes != 100_000_000_000 {
		t.Errorf("storage = %+v", u.Storage)
	}
}

// Nil sources are skipped — a user with only Mimir still gets a report.
func TestGatherSkipsNilSources(t *testing.T) {
	u, err := cost.Gather(context.Background(), cost.Sources{
		Series: fakeSeries{m: map[string]int64{"a": 1}},
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Streams) != 1 || len(u.Storage) != 0 {
		t.Errorf("usage = %+v, want 1 stream, no storage", u)
	}
}

// A source error is returned, not swallowed — a partial cost report presented
// as complete is a lie.
func TestGatherReturnsSourceError(t *testing.T) {
	_, err := cost.Gather(context.Background(), cost.Sources{
		Series: fakeSeries{err: errors.New("mimir down")},
	}, time.Hour)
	if err == nil {
		t.Fatal("want error from failing source")
	}
}
