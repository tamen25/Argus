package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
)

func TestNoopInjector_DoesNothingSuccessfully(t *testing.T) {
	var n NoopInjector
	sc := testScenario()
	if err := n.Reset(context.Background(), sc); err != nil {
		t.Error(err)
	}
	if err := n.Inject(context.Background(), sc, sc.Spec.Inject[0]); err != nil {
		t.Error(err)
	}
	if err := n.Cleanup(context.Background(), sc); err != nil {
		t.Error(err)
	}
}

// Cluster-backed step types must fail loudly rather than silently pretending a
// fault was injected.
func TestScriptInjector_RejectsClusterStepTypes(t *testing.T) {
	sc := testScenario()
	for _, typ := range []string{bench.InjectChaosMesh, bench.InjectKubectl} {
		step := bench.InjectStep{Type: typ, Manifest: "faults/x.yaml", Duration: "1m"}
		err := ScriptInjector{}.Inject(context.Background(), sc, step)
		if err == nil {
			t.Fatalf("%s: expected error, got nil", typ)
		}
		if !strings.Contains(err.Error(), "cluster client") {
			t.Errorf("%s: error should explain the missing adapter: %v", typ, err)
		}
	}
}

func TestScriptInjector_RunsScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script execution test uses a POSIX shell script")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.txt")
	script := filepath.Join(dir, "inject.sh")
	body := "#!/bin/sh\necho \"$ARGUS_SCENARIO\" > " + marker + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}

	sc := testScenario()
	inj := ScriptInjector{Dir: dir}
	step := bench.InjectStep{Type: bench.InjectScript, Script: "inject.sh", Duration: "1m"}
	if err := inj.Inject(context.Background(), sc, step); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("script did not run: %v", err)
	}
	if !strings.Contains(string(got), sc.Metadata.Name) {
		t.Errorf("script did not receive ARGUS_SCENARIO: %q", got)
	}
}

func TestScriptInjector_FailingScriptIsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script execution test uses a POSIX shell script")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "boom.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho bad >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := ScriptInjector{Dir: dir}.Inject(context.Background(), testScenario(),
		bench.InjectStep{Type: bench.InjectScript, Script: "boom.sh", Duration: "1m"})
	if err == nil {
		t.Fatal("expected error from failing script")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("error should carry stderr: %v", err)
	}
}

func TestScriptInjector_NoHooksIsNoop(t *testing.T) {
	inj := ScriptInjector{}
	if err := inj.Reset(context.Background(), testScenario()); err != nil {
		t.Errorf("Reset with no hook: %v", err)
	}
	if err := inj.Cleanup(context.Background(), testScenario()); err != nil {
		t.Errorf("Cleanup with no hook: %v", err)
	}
}

func TestAlwaysReadyProbe(t *testing.T) {
	ok, err := AlwaysReadyProbe{}.Reached(context.Background(), testScenario())
	if err != nil || !ok {
		t.Errorf("Reached() = %v, %v", ok, err)
	}
}
