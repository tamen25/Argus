// Package kube is the concrete injector adapter for scenario steps that apply
// Kubernetes manifests — both `kubectl` steps and `chaosmesh` steps, since a
// Chaos Mesh experiment is itself a CRD manifest.
//
// It shells out to kubectl rather than embedding client-go: the fault surface
// here is "apply this manifest, then delete it", the bench harness already runs
// beside a configured kubeconfig, and adding a Kubernetes API client to the
// engine binary would be a large dependency for no extra capability. This is a
// bench-time tool, not part of the read-only product path.
package kube

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tamen25/Argus/engine/internal/bench"
)

// Injector applies and removes a scenario's manifest-based faults.
type Injector struct {
	// Kubectl is the binary to invoke (default "kubectl").
	Kubectl string
	// Dir is the scenario root that relative manifest paths resolve against.
	Dir string
	// Namespace is passed to kubectl when set; manifests with their own
	// metadata.namespace win, as usual.
	Namespace string
	// Context selects a kube context; empty uses the current one.
	Context string
	// Timeout caps a single kubectl invocation (default 2m).
	Timeout time.Duration

	// kubectlPrefix is prepended to every invocation. Tests use it to re-exec
	// the test binary as a stand-in kubectl; production leaves it empty.
	kubectlPrefix []string
}

// New builds an injector with defaults applied.
func New(dir, namespace, kubeContext string) *Injector {
	return &Injector{Kubectl: "kubectl", Dir: dir, Namespace: namespace, Context: kubeContext}
}

// Reset deletes every manifest the scenario declares, ignoring absences, so a
// fault left behind by an earlier run cannot contaminate this one.
func (i *Injector) Reset(ctx context.Context, sc bench.Scenario) error {
	return i.deleteAll(ctx, sc)
}

// Inject applies one manifest step. Script steps are rejected: this injector
// only speaks manifests, and silently skipping a step would mean scoring an
// environment that was never faulted.
func (i *Injector) Inject(ctx context.Context, _ bench.Scenario, step bench.InjectStep) error {
	switch step.Type {
	case bench.InjectChaosMesh, bench.InjectKubectl:
		_, err := i.run(ctx, "apply", "-f", i.path(step.Manifest))
		return err
	case bench.InjectScript:
		return fmt.Errorf("kube injector cannot run script steps; use the script injector for scenario steps of type %q", step.Type)
	default:
		return fmt.Errorf("unknown inject type %q", step.Type)
	}
}

// Cleanup deletes every manifest the scenario declares. It runs even when the
// attempt failed partway, so a leaked fault cannot poison later repeats.
func (i *Injector) Cleanup(ctx context.Context, sc bench.Scenario) error {
	return i.deleteAll(ctx, sc)
}

func (i *Injector) deleteAll(ctx context.Context, sc bench.Scenario) error {
	var firstErr error
	for _, step := range sc.Spec.Inject {
		if step.Manifest == "" {
			continue
		}
		// Keep deleting the rest even if one fails: a partial cleanup that stops
		// at the first error leaves more faults behind than it removes.
		if _, err := i.run(ctx, "delete", "-f", i.path(step.Manifest), "--ignore-not-found"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (i *Injector) path(manifest string) string {
	if i.Dir == "" || filepath.IsAbs(manifest) {
		return manifest
	}
	return filepath.Join(i.Dir, manifest)
}

func (i *Injector) run(ctx context.Context, args ...string) (string, error) {
	bin := i.Kubectl
	if bin == "" {
		bin = "kubectl"
	}
	timeout := i.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	full := make([]string, 0, len(args)+len(i.kubectlPrefix)+4)
	full = append(full, i.kubectlPrefix...)
	if i.Context != "" {
		full = append(full, "--context", i.Context)
	}
	if i.Namespace != "" {
		full = append(full, "-n", i.Namespace)
	}
	full = append(full, args...)

	cmd := exec.CommandContext(ctx, bin, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%s %v: %w (stderr: %s)", bin, full, err, truncate(stderr.String(), 512))
	}
	return stdout.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
