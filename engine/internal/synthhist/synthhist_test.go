package synthhist_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/prometheus/tsdb"

	"github.com/tamen25/Argus/engine/internal/backtest"
	"github.com/tamen25/Argus/engine/internal/synthhist"
)

func testSpec(t *testing.T) synthhist.Spec {
	t.Helper()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return synthhist.Spec{
		Schema: synthhist.SpecSchema,
		Seed:   42,
		From:   from,
		To:     from.Add(4 * time.Hour),
		Step:   time.Minute,
		Services: []synthhist.Service{
			{Name: "checkout", RatePerSec: 10, ErrorRatio: 0.005, Jitter: 0.1},
			{Name: "cart", RatePerSec: 5, ErrorRatio: 0.001},
		},
		Incidents: []synthhist.SynthIncident{
			{ID: "synth-1", Service: "checkout", Start: from.Add(time.Hour), End: from.Add(90 * time.Minute), ErrorRatio: 0.30},
		},
	}
}

// Generate writes real TSDB blocks (readable by the prometheus tsdb reader)
// and a matching incidents.yaml the backtest loader accepts — the demo's
// ground truth ships with its data.
func TestGenerateBlocksAndRegistry(t *testing.T) {
	dir := t.TempDir()
	if err := synthhist.Generate(context.Background(), testSpec(t), dir); err != nil {
		t.Fatal(err)
	}

	// blocks exist and open with the real reader
	db, err := tsdb.OpenDBReadOnly(filepath.Join(dir, "blocks"), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	blocks, err := db.Blocks()
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) == 0 {
		t.Fatal("no blocks written")
	}
	var minT, maxT int64 = 1 << 62, 0
	for _, b := range blocks {
		m := b.Meta()
		if m.MinTime < minT {
			minT = m.MinTime
		}
		if m.MaxTime > maxT {
			maxT = m.MaxTime
		}
	}
	spec := testSpec(t)
	if minT > spec.From.UnixMilli() || maxT < spec.To.Add(-spec.Step).UnixMilli() {
		t.Errorf("blocks cover [%d, %d], want at least [%d, %d]", minT, maxT, spec.From.UnixMilli(), spec.To.UnixMilli())
	}

	// the emitted registry loads through the backtest's strict loader
	reg, err := backtest.LoadIncidents(filepath.Join(dir, "incidents.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Incidents) != 1 || reg.Incidents[0].ID != "synth-1" {
		t.Errorf("registry = %+v", reg.Incidents)
	}
}

// Same spec, same seed → byte-identical registry and identical block
// contents (reproducible demos, architecture rule 6 in spirit).
func TestGenerateDeterministic(t *testing.T) {
	d1, d2 := t.TempDir(), t.TempDir()
	if err := synthhist.Generate(context.Background(), testSpec(t), d1); err != nil {
		t.Fatal(err)
	}
	if err := synthhist.Generate(context.Background(), testSpec(t), d2); err != nil {
		t.Fatal(err)
	}
	r1, _ := os.ReadFile(filepath.Join(d1, "incidents.yaml"))
	r2, _ := os.ReadFile(filepath.Join(d2, "incidents.yaml"))
	if string(r1) != string(r2) {
		t.Error("registries differ across runs of the same spec")
	}
}

func TestLoadSpecRejectsUnknownServiceIncident(t *testing.T) {
	spec := testSpec(t)
	spec.Incidents[0].Service = "nope"
	if err := spec.Validate(); err == nil {
		t.Error("incident on unknown service validated")
	}
}
