package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tamen25/Argus/engine/internal/bench"
)

// NoopInjector performs no injection. Use it to score an agent against an
// environment you have already put into the desired state by hand — the run
// record still carries the scenario hash and ground truth, but nothing was
// injected by Argus, and reports must not imply otherwise.
type NoopInjector struct{}

// Reset does nothing.
func (NoopInjector) Reset(context.Context, bench.Scenario) error { return nil }

// Inject does nothing.
func (NoopInjector) Inject(context.Context, bench.Scenario, bench.InjectStep) error { return nil }

// Cleanup does nothing.
func (NoopInjector) Cleanup(context.Context, bench.Scenario) error { return nil }

// ScriptInjector runs a scenario's `type: script` steps as local commands,
// resolved relative to Dir. Steps of type chaosmesh or kubectl are rejected
// with a clear error: those adapters need a cluster client and land with the
// Kubernetes slice. Failing loudly beats pretending a fault was injected.
type ScriptInjector struct {
	// Dir is the scenario root that script paths resolve against.
	Dir string
	// Shell is the interpreter used to run a script (default: the script is
	// executed directly).
	Shell string
	// Timeout caps a single script step (default 5m).
	Timeout time.Duration
	// ResetScript and CleanupScript are optional lifecycle hooks.
	ResetScript   string
	CleanupScript string
}

// Reset runs the optional reset hook.
func (s ScriptInjector) Reset(ctx context.Context, sc bench.Scenario) error {
	if s.ResetScript == "" {
		return nil
	}
	return s.run(ctx, sc, s.ResetScript)
}

// Inject runs one script step, rejecting step types that need a cluster client.
func (s ScriptInjector) Inject(ctx context.Context, sc bench.Scenario, step bench.InjectStep) error {
	switch step.Type {
	case bench.InjectScript:
		return s.run(ctx, sc, step.Script)
	case bench.InjectChaosMesh, bench.InjectKubectl:
		return fmt.Errorf("inject type %q needs a cluster client; the Kubernetes injector adapter is not wired yet", step.Type)
	default:
		return fmt.Errorf("unknown inject type %q", step.Type)
	}
}

// Cleanup runs the optional cleanup hook.
func (s ScriptInjector) Cleanup(ctx context.Context, sc bench.Scenario) error {
	if s.CleanupScript == "" {
		return nil
	}
	return s.run(ctx, sc, s.CleanupScript)
}

func (s ScriptInjector) run(ctx context.Context, sc bench.Scenario, script string) error {
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	path := script
	if s.Dir != "" && !filepath.IsAbs(script) {
		path = filepath.Join(s.Dir, script)
	}

	var cmd *exec.Cmd
	if s.Shell != "" {
		cmd = exec.CommandContext(ctx, s.Shell, path)
	} else {
		cmd = exec.CommandContext(ctx, path)
	}
	cmd.Env = append(os.Environ(),
		"ARGUS_SCENARIO="+sc.Metadata.Name,
		"ARGUS_APP="+sc.Spec.Environment.App,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("script %s: %w (stderr: %s)", script, err, truncateStr(stderr.String(), 512))
	}
	return nil
}

// AlwaysReadyProbe reports the environment as immediately steady. It is the
// honest default while telemetry-based steady-state detection is unwired: the
// injector's script is expected to return only once the fault is established.
// A report produced with it must not claim steady state was verified.
type AlwaysReadyProbe struct{}

// Reached always returns true.
func (AlwaysReadyProbe) Reached(context.Context, bench.Scenario) (bool, error) { return true, nil }

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
