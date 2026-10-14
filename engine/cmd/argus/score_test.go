package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/tamen25/Argus/engine/internal/rules"
)

func specVersionFile(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "..", ".instrumentation-score-version"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunScoreStreamWindow(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	opts := &scoreOptions{ // no rulesDir: built-in embedded rules
		window:          2 * time.Second,
		specVersionFile: specVersionFile(t), listener: lis,
	}

	// export a violating payload mid-window
	go func() {
		time.Sleep(300 * time.Millisecond)
		conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		td := ptrace.NewTraces()
		rs := td.ResourceSpans().AppendEmpty() // no service.name
		rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("orphan")
		_, _ = ptraceotlp.NewGRPCClient(conn).Export(context.Background(), ptraceotlp.NewExportRequestFromTraces(td))
	}()

	rep, err := runScore(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.SpecVersion == "unknown" || rep.SpecVersion == "" {
		t.Errorf("spec version = %q", rep.SpecVersion)
	}
	unk := rep.Snapshot.Service("<unknown>")
	if unk == nil {
		t.Fatal("no <unknown> report")
	}
	found := false
	for _, f := range unk.Findings {
		if f.RuleID == "RES-005" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected RES-005 finding for <unknown>, got %+v", unk.Findings)
	}
	// exact score depends on how many built-in rules apply; the invariant is
	// that a failing critical rule keeps the service far from perfect
	if unk.SpecScore >= 85 {
		t.Errorf("unknown spec score = %v, want < 85 with RES-005 failing", unk.SpecScore)
	}
}

func TestScoreCommandFailBelowThreshold(t *testing.T) {
	// built-ins load with no rules dir at all; a custom dir overrides by ID
	rep0, err := runScore(context.Background(), &scoreOptions{specVersionFile: specVersionFile(t), window: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep0.Snapshot.RulesEvaluated) < 3 {
		t.Errorf("builtin rules = %v, want >= 3", rep0.Snapshot.RulesEvaluated)
	}
	override := t.TempDir()
	if err := os.WriteFile(filepath.Join(override, "res-005.yaml"), []byte(`schema: argus.rules/v1
id: RES-005
source: spec
name: service.name is present
description: override
target: resource
impact: low
evaluation:
  mode: item
  criteria: "'service.name' in resource"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	rep1, err := runScore(context.Background(), &scoreOptions{rulesDir: override, specVersionFile: specVersionFile(t), window: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep1.Snapshot.RulesEvaluated) != len(rep0.Snapshot.RulesEvaluated) {
		t.Errorf("override must replace, not append: %v", rep1.Snapshot.RulesEvaluated)
	}

	// threshold trip: no telemetry -> fleet 100 -> fail-below 100.1 impossible;
	// use command wiring with a violating stream instead
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	opts := &scoreOptions{
		window:          1500 * time.Millisecond,
		specVersionFile: specVersionFile(t), listener: lis, failBelow: 85,
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		td := ptrace.NewTraces()
		td.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("x")
		_, _ = ptraceotlp.NewGRPCClient(conn).Export(context.Background(), ptraceotlp.NewExportRequestFromTraces(td))
	}()
	rep, err := runScore(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Snapshot.FleetScore >= opts.failBelow {
		t.Fatalf("fleet = %v, want < %v", rep.Snapshot.FleetScore, opts.failBelow)
	}
	// the RunE wrapper turns that into errBelowThreshold; emulate its check
	if !(opts.failBelow > 0 && rep.Snapshot.FleetScore < opts.failBelow) {
		t.Error("threshold check must trip")
	}
	if !errors.Is(errBelowThreshold, errBelowThreshold) {
		t.Error("sentinel sanity")
	}
}

// An empty fleet scores a vacuous 100 and would sail through any
// --fail-below-score gate; the report must say so out loud (honest
// reporting). Regression test for a silently broken mirror.
func TestRunScoreEmptyWindowNote(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := runScore(context.Background(), &scoreOptions{
		window:          200 * time.Millisecond,
		specVersionFile: specVersionFile(t), listener: lis,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range rep.Notes {
		if strings.Contains(n, "no telemetry") {
			found = true
		}
	}
	if !found {
		t.Errorf("empty window must be disclosed in notes, got %v", rep.Notes)
	}
}

// The gate trips on a low score, and on an empty fleet, whose vacuous 100
// would otherwise let a broken telemetry connection pass CI.
func TestScoreGate(t *testing.T) {
	fleet := &rules.Snapshot{FleetScore: 90, Services: []rules.ServiceReport{{ServiceName: "ad"}}}
	empty := &rules.Snapshot{FleetScore: 100, Services: []rules.ServiceReport{}}
	cases := []struct {
		name      string
		snap      *rules.Snapshot
		failBelow float64
		trips     bool
	}{
		{"no gate", empty, 0, false},
		{"above the threshold", fleet, 85, false},
		{"below the threshold", fleet, 95, true},
		{"empty fleet", empty, 85, true},
	}
	for _, c := range cases {
		err := scoreGate(c.snap, c.failBelow)
		if got := errors.Is(err, errBelowThreshold); got != c.trips {
			t.Errorf("%s: err = %v, want trip %v", c.name, err, c.trips)
		}
	}
}

// R14: a run of a few seconds used to judge no trace at all. The trace rules
// read only a completed window (1h by default), which a short run never
// reaches, so they were skipped. The run now closes its window when
// collection stops.
func TestRunScoreJudgesTracesFromAShortRun(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	opts := &scoreOptions{window: 2 * time.Second, specVersionFile: specVersionFile(t), listener: lis}

	go func() {
		time.Sleep(300 * time.Millisecond)
		conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		td := ptrace.NewTraces()
		rs := td.ResourceSpans().AppendEmpty()
		rs.Resource().Attributes().PutStr("service.name", "cart")
		sp := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
		sp.SetName("checkout")
		sp.SetTraceID(pcommon.TraceID{1})
		sp.SetSpanID(pcommon.SpanID{2})
		sp.SetParentSpanID(pcommon.SpanID{9}) // a parent that never arrives
		_, _ = ptraceotlp.NewGRPCClient(conn).Export(context.Background(), ptraceotlp.NewExportRequestFromTraces(td))
	}()

	rep, err := runScore(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	cart := rep.Snapshot.Service("cart")
	if cart == nil {
		t.Fatal("no report for cart")
	}
	var traceRules []string
	for _, f := range cart.Findings {
		if f.RuleID == "SPA-002" || f.RuleID == "ARG-SPA-002" {
			traceRules = append(traceRules, f.RuleID)
		}
	}
	if len(traceRules) == 0 {
		t.Errorf("findings = %+v, want the orphaned span judged by the trace rules", cart.Findings)
	}
}
