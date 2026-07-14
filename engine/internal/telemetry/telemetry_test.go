package telemetry

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/ingest"
	"github.com/tamen25/Argus/engine/internal/rules"
	"github.com/tamen25/Argus/engine/internal/rules/builtin"
)

// Dogfood loop closed in one test: the engine's own telemetry, exported over
// real OTLP, lands in the engine's own receiver and scores like any other
// service. This is the instrumentation the CI dogfood gate scores.
func TestSetupExportsScorableTelemetry(t *testing.T) {
	rs, err := builtin.Load()
	if err != nil {
		t.Fatal(err)
	}
	eng, err := rules.NewEngine(rs)
	if err != nil {
		t.Fatal(err)
	}
	col := rules.NewCollector(eng)
	pipe := ingest.NewPipeline(col, ingest.TrackerOpts{})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := ingest.NewGRPCServer(pipe)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	ctx := context.Background()
	tel, err := Setup(ctx, Config{
		Endpoint:       lis.Addr().String(),
		ServiceName:    "argus-engine",
		ServiceVersion: "test",
		Environment:    "ci",
		ExportInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	// emit the same shapes serve emits: a span around an export tick and the
	// tick counter
	ctx2, span := tel.Tracer.Start(ctx, "score.export")
	tel.ExportTicks.Add(ctx2, 1)
	span.End()
	time.Sleep(250 * time.Millisecond) // one metric export interval
	if err := tel.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown/flush: %v", err)
	}

	pipe.AggregateRows()
	rep := col.Snapshot().Service("argus-engine")
	if rep == nil {
		t.Fatal("engine's own telemetry not observed by its own receiver")
	}
	tr, me, _ := pipe.ItemsConsumed()
	if tr == 0 || me == 0 {
		t.Errorf("items consumed = traces %d, metrics %d — want both > 0", tr, me)
	}

	// The gate's bar: the engine must pass its own spec rules. Findings here
	// mean the self-instrumentation is dishonest about what it demands of
	// others — fix the instrumentation, not the test.
	for _, f := range rep.Findings {
		if f.Source == "spec" {
			t.Errorf("engine fails its own spec rule %s: %+v", f.RuleID, f.Stats)
		}
	}
	if rep.SpecScore < 85 {
		t.Errorf("engine self-score = %.1f, below the dogfood gate (85)", rep.SpecScore)
	}
}

// Telemetry is opt-in: no endpoint, no exporters, zero overhead.
func TestSetupDisabledWithoutEndpoint(t *testing.T) {
	tel, err := Setup(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	// must be safe to use anyway (no-op providers)
	_, span := tel.Tracer.Start(context.Background(), "noop")
	span.End()
	tel.ExportTicks.Add(context.Background(), 1)
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
