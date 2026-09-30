package orchestrator

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
)

// orderLog is one file that both the fake manifest adapter and the hook
// scripts append to, so tests can assert the order work happened in.
type orderLog struct{ path string }

func (l orderLog) add(t *testing.T, entry string) {
	t.Helper()
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(entry + "\n"); err != nil {
		t.Fatal(err)
	}
}

func (l orderLog) entries(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(l.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

// logManifests is a fake manifest adapter that records its calls.
type logManifests struct {
	t          *testing.T
	log        orderLog
	cleanupErr error
}

func (m logManifests) Reset(context.Context, bench.Scenario) error {
	m.log.add(m.t, "manifests-reset")
	return nil
}
func (m logManifests) Inject(_ context.Context, _ bench.Scenario, step bench.InjectStep) error {
	m.log.add(m.t, "manifests-inject:"+step.Manifest)
	return nil
}
func (m logManifests) Cleanup(context.Context, bench.Scenario) error {
	m.log.add(m.t, "manifests-cleanup")
	return m.cleanupErr
}

// hookEnv builds a scenario dir with scripts that append their name (and the
// namespace they were given) to the shared log. A script named fail-* exits 1.
func hookEnv(t *testing.T, names ...string) (string, orderLog) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH")
	}
	dir := t.TempDir()
	log := orderLog{path: filepath.Join(dir, "order.log")}
	for _, n := range names {
		body := "#!/usr/bin/env bash\necho \"hook:" + n + ":${ARGUS_NAMESPACE}\" >> \"$ORDER_LOG\"\n"
		if strings.HasPrefix(n, "fail-") {
			body += "echo 'restore failed' >&2\nexit 1\n"
		}
		if err := os.WriteFile(filepath.Join(dir, n+".sh"), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir, log
}

func autoInjector(t *testing.T, dir string, log orderLog, cleanupErr error) AutoInjector {
	return AutoInjector{
		Manifests: logManifests{t: t, log: log, cleanupErr: cleanupErr},
		Scripts: ScriptInjector{Dir: dir, Shell: "bash", Env: []string{
			// Forward slashes so Git Bash on Windows accepts the path.
			"ORDER_LOG=" + filepath.ToSlash(log.path),
			"ARGUS_NAMESPACE=otel-demo",
		}},
	}
}

// TestAutoInjectorResetStopsTheSourceFirst: the fault's objects are deleted
// before the reset hooks run, so a hook never restarts a service while the load
// driver is still feeding it the fault.
func TestAutoInjectorResetStopsTheSourceFirst(t *testing.T) {
	dir, log := hookEnv(t, "restart-frontend", "wait-ready")
	sc := testScenario()
	sc.Spec.Reset = []bench.Hook{{Script: "restart-frontend.sh"}, {Script: "wait-ready.sh"}}

	if err := autoInjector(t, dir, log, nil).Reset(context.Background(), sc); err != nil {
		t.Fatal(err)
	}
	want := "manifests-reset hook:restart-frontend:otel-demo hook:wait-ready:otel-demo"
	if got := strings.Join(log.entries(t), " "); got != want {
		t.Fatalf("order = %q, want %q", got, want)
	}
}

// TestAutoInjectorCleanupKeepsGoing: a failing restore must not stop the
// remaining restores or the manifest deletion — stopping early leaks more of
// the fault than it removes — and its error must still be reported.
func TestAutoInjectorCleanupKeepsGoing(t *testing.T) {
	dir, log := hookEnv(t, "fail-restore", "restore-b")
	sc := testScenario()
	sc.Spec.Cleanup = []bench.Hook{{Script: "fail-restore.sh"}, {Script: "restore-b.sh"}}

	err := autoInjector(t, dir, log, errors.New("delete failed")).Cleanup(context.Background(), sc)
	if err == nil || !strings.Contains(err.Error(), "cleanup[0]") || !strings.Contains(err.Error(), "restore failed") {
		t.Fatalf("err = %v, want the FIRST failure (cleanup[0]) with its stderr", err)
	}
	want := "hook:fail-restore:otel-demo hook:restore-b:otel-demo manifests-cleanup"
	if got := strings.Join(log.entries(t), " "); got != want {
		t.Fatalf("order = %q, want every step to run: %q", got, want)
	}
}

func TestAutoInjectorDispatchesByStepType(t *testing.T) {
	dir, log := hookEnv(t, "mutate")
	inj := autoInjector(t, dir, log, nil)
	sc := testScenario()

	for _, step := range []bench.InjectStep{
		{Type: bench.InjectKubectl, Manifest: "driver.yaml", Duration: "1m"},
		{Type: bench.InjectScript, Script: "mutate.sh", Duration: "1m"},
		{Type: bench.InjectChaosMesh, Manifest: "chaos.yaml", Duration: "1m"},
	} {
		if err := inj.Inject(context.Background(), sc, step); err != nil {
			t.Fatalf("%s step: %v", step.Type, err)
		}
	}
	want := "manifests-inject:driver.yaml hook:mutate:otel-demo manifests-inject:chaos.yaml"
	if got := strings.Join(log.entries(t), " "); got != want {
		t.Fatalf("order = %q, want %q", got, want)
	}
	if err := inj.Inject(context.Background(), sc, bench.InjectStep{Type: "helm"}); err == nil {
		t.Fatal("an unknown step type must be an error, never skipped")
	}
}
