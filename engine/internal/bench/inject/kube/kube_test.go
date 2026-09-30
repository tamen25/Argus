package kube

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
	"github.com/tamen25/Argus/engine/internal/bench/orchestrator"
)

// TestKubectlHelperProcess is re-executed as a stand-in for kubectl, so these
// tests run identically on Windows and Linux CI without a cluster.
func TestKubectlHelperProcess(t *testing.T) {
	if os.Getenv("ARGUS_WANT_KUBECTL_HELPER") != "1" {
		t.Skip("helper process; not a real test")
	}
	defer os.Exit(0)

	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	if log := os.Getenv("ARGUS_KUBECTL_LOG"); log != "" {
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintln(f, strings.Join(args, " "))
			_ = f.Close()
		}
	}
	// Simulate a cluster where one CRD is not installed.
	if kind := os.Getenv("ARGUS_KUBECTL_UNKNOWN_KIND"); kind != "" && strings.Contains(strings.Join(args, " "), " "+kind+" ") {
		fmt.Fprintf(os.Stderr, "error: the server doesn't have a resource type %q\n", kind)
		os.Exit(1)
	}
	if os.Getenv("ARGUS_KUBECTL_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "the server could not find the requested resource")
		os.Exit(1)
	}
	fmt.Println("ok")
}

func helperInjector(t *testing.T, fail bool) (*Injector, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	t.Setenv("ARGUS_WANT_KUBECTL_HELPER", "1")
	t.Setenv("ARGUS_KUBECTL_LOG", logPath)
	if fail {
		t.Setenv("ARGUS_KUBECTL_FAIL", "1")
	}
	inj := &Injector{
		Kubectl:   os.Args[0],
		Dir:       "scenarios",
		Namespace: "otel-demo",
		Context:   "kind-argus",
		// Route the re-executed test binary into the helper. Without this it
		// parses kubectl's flags as its own and fails — which once let a test
		// that expected a failure pass for the wrong reason.
		kubectlPrefix: []string{"-test.run=TestKubectlHelperProcess", "--"},
	}
	return inj, logPath
}

func scenarioWith(steps ...bench.InjectStep) bench.Scenario {
	return bench.Scenario{
		APIVersion: bench.APIVersion,
		Kind:       bench.Kind,
		Metadata:   bench.Metadata{Name: "s"},
		Spec: bench.ScenarioSpec{
			Environment: bench.Environment{App: "otel-demo"},
			Inject:      steps,
			GroundTruth: bench.GroundTruth{
				RootCauseEntities: []bench.Entity{{Kind: "Deployment", Name: "ad"}},
				Category:          "c",
			},
		},
	}
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func TestInject_RejectsScriptSteps(t *testing.T) {
	inj := &Injector{}
	err := inj.Inject(context.Background(), scenarioWith(), bench.InjectStep{
		Type: bench.InjectScript, Script: "x.sh", Duration: "1m",
	})
	if err == nil {
		t.Fatal("expected script steps to be rejected")
	}
	if !strings.Contains(err.Error(), "script injector") {
		t.Errorf("error should point at the right injector: %v", err)
	}
}

func TestInject_UnknownType(t *testing.T) {
	err := (&Injector{}).Inject(context.Background(), scenarioWith(), bench.InjectStep{Type: "wat", Duration: "1m"})
	if err == nil {
		t.Fatal("expected unknown type to be rejected")
	}
}

func TestPath_ResolvesRelativeToDir(t *testing.T) {
	i := &Injector{Dir: filepath.Join("a", "b")}
	if got := i.path("faults/x.yaml"); got != filepath.Join("a", "b", "faults/x.yaml") {
		t.Errorf("path = %q", got)
	}
	abs, err := filepath.Abs(filepath.Join(t.TempDir(), "x.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := i.path(abs); got != abs {
		t.Errorf("absolute path rewritten: %q", got)
	}
	bare := &Injector{}
	if got := bare.path("x.yaml"); got != "x.yaml" {
		t.Errorf("path without Dir = %q", got)
	}
}

// Cleanup must attempt every manifest even when an earlier delete fails —
// stopping at the first error would leave more faults behind than it removes.
func TestCleanup_ContinuesPastFailures(t *testing.T) {
	inj, logPath := helperInjector(t, true)
	inj.Kubectl = os.Args[0]
	inj.kubectlPrefix = []string{"-test.run=TestKubectlHelperProcess", "--"}

	sc := scenarioWith(
		bench.InjectStep{Type: bench.InjectKubectl, Manifest: "faults/a.yaml", Duration: "1m"},
		bench.InjectStep{Type: bench.InjectChaosMesh, Manifest: "faults/b.yaml", Duration: "1m"},
	)
	err := inj.Cleanup(context.Background(), sc)
	if err == nil {
		t.Fatal("expected the first delete failure to be reported")
	}
	log := readLog(t, logPath)
	if !strings.Contains(log, "a.yaml") || !strings.Contains(log, "b.yaml") {
		t.Errorf("both manifests should have been attempted, got:\n%s", log)
	}
}

func TestInjectAndCleanup_BuildExpectedKubectlArgs(t *testing.T) {
	inj, logPath := helperInjector(t, false)
	inj.kubectlPrefix = []string{"-test.run=TestKubectlHelperProcess", "--"}

	step := bench.InjectStep{Type: bench.InjectChaosMesh, Manifest: "faults/net.yaml", Duration: "1m"}
	if err := inj.Inject(context.Background(), scenarioWith(step), step); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if err := inj.Cleanup(context.Background(), scenarioWith(step)); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	log := readLog(t, logPath)
	if !strings.Contains(log, "--context kind-argus") {
		t.Errorf("context not passed:\n%s", log)
	}
	if !strings.Contains(log, "-n otel-demo") {
		t.Errorf("namespace not passed:\n%s", log)
	}
	if !strings.Contains(log, "apply -f") {
		t.Errorf("apply not issued:\n%s", log)
	}
	if !strings.Contains(log, "--ignore-not-found") {
		t.Errorf("delete should ignore absent objects:\n%s", log)
	}
}

// Compile-time proof the adapter satisfies the orchestrator port.
var _ orchestrator.Injector = (*Injector)(nil)

// TestResetSweepsEveryArgusFault is the guard for the leak found on
// 2026-09-30: a StressChaos from a manual test sat on checkout for two months
// because a scenario's reset only deletes the manifests it declares. Reset now
// also sweeps every object carrying the argus-bench label, cluster-wide.
func TestResetSweepsEveryArgusFault(t *testing.T) {
	inj, logPath := helperInjector(t, false)
	if err := inj.Reset(context.Background(), scenarioWith()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(b)
	for _, kind := range sweepKinds {
		want := "delete " + kind + " --all-namespaces -l " + ManagedBy + " --ignore-not-found"
		if !strings.Contains(log, want) {
			t.Errorf("reset did not sweep %s; want a call containing %q", kind, want)
		}
	}
}

// TestSweepToleratesMissingCRDs: a cluster without Chaos Mesh must still be able
// to run manifest-only scenarios.
func TestSweepToleratesMissingCRDs(t *testing.T) {
	inj, _ := helperInjector(t, false)
	t.Setenv("ARGUS_KUBECTL_UNKNOWN_KIND", "dnschaos")
	if err := inj.Reset(context.Background(), scenarioWith()); err != nil {
		t.Fatalf("a missing CRD must be skipped, got %v", err)
	}
}

// TestSweepSurfacesRealFailures: anything other than a missing CRD means a fault
// may still be live, and must not be swallowed.
func TestSweepSurfacesRealFailures(t *testing.T) {
	inj, _ := helperInjector(t, true)
	err := inj.Reset(context.Background(), scenarioWith())
	// Assert the simulated failure's own text, not just "sweeping": a harness
	// misconfiguration also fails the sweep, and must not satisfy this test.
	if err == nil || !strings.Contains(err.Error(), "sweeping") ||
		!strings.Contains(err.Error(), "could not find the requested resource") {
		t.Fatalf("err = %v, want the simulated sweep failure", err)
	}
}
