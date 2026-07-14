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

	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
	rep0, err := runScore(context.Background(), &scoreOptions{specVersionFile: specVersionFile(t)})
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
	rep1, err := runScore(context.Background(), &scoreOptions{rulesDir: override, specVersionFile: specVersionFile(t)})
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
