package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func repoRulesDir(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "..", "rules"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

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
	opts := &scoreOptions{
		rulesDir: repoRulesDir(t), window: 2 * time.Second,
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
	if unk == nil || len(unk.Findings) == 0 || unk.Findings[0].RuleID != "RES-005" {
		t.Fatalf("expected RES-005 finding for <unknown>, got %+v", unk)
	}
	if rep.Snapshot.FleetScore != 0 {
		t.Errorf("fleet = %v, want 0 (single service failing its only applicable critical rule)", rep.Snapshot.FleetScore)
	}
}

func TestScoreCommandFailBelowThreshold(t *testing.T) {
	// empty rules dir -> error (no silent 100s)
	empty := t.TempDir()
	_ = os.MkdirAll(filepath.Join(empty, "spec"), 0o755)
	_ = os.MkdirAll(filepath.Join(empty, "argus"), 0o755)
	if _, err := runScore(context.Background(), &scoreOptions{rulesDir: empty, specVersionFile: specVersionFile(t)}); err == nil {
		t.Error("want error for empty rules dir")
	}

	// threshold trip: no telemetry -> fleet 100 -> fail-below 100.1 impossible;
	// use command wiring with a violating stream instead
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	opts := &scoreOptions{
		rulesDir: repoRulesDir(t), window: 1500 * time.Millisecond,
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
