package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestShellHelperProcess is not a real test: it is re-executed as the
// subprocess for the shell-agent tests, which keeps them portable (no /bin/sh
// assumption, works on Windows and Linux CI).
func TestShellHelperProcess(t *testing.T) {
	if os.Getenv("ARGUS_WANT_HELPER") != "1" {
		t.Skip("helper process; not a real test")
	}
	defer os.Exit(0)

	switch os.Getenv("ARGUS_HELPER_MODE") {
	case "fail":
		fmt.Fprintln(os.Stderr, "boom")
		os.Exit(3)
	case "empty":
		// print nothing
	case "echo-env":
		fmt.Printf("scenario=%s brief=%s", os.Getenv("ARGUS_SCENARIO"), os.Getenv("ARGUS_BRIEF"))
	case "hang":
		time.Sleep(30 * time.Second)
	default: // echo stdin back
		b, _ := io.ReadAll(os.Stdin)
		fmt.Printf("diagnosis from stdin: %s", strings.TrimSpace(string(b)))
	}
}

func helperShell(mode string, timeout time.Duration) *ShellAgent {
	return NewShell(ShellConfig{
		Name:    "helper",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestShellHelperProcess"},
		Env:     []string{"ARGUS_WANT_HELPER=1", "ARGUS_HELPER_MODE=" + mode},
		Timeout: timeout,
	})
}

func TestShell_CapturesStdoutFromStdin(t *testing.T) {
	res, err := helperShell("", 30*time.Second).Diagnose(context.Background(), Task{
		Scenario: "s1", Brief: "checkout is erroring",
	})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if got := string(res.Raw); !strings.Contains(got, "checkout is erroring") {
		t.Errorf("raw = %q, want brief echoed from stdin", got)
	}
	if res.Usage.Steps != 1 {
		t.Errorf("steps = %d, want 1", res.Usage.Steps)
	}
	// Budgets are not enforceable for shell agents; usage stays zero, not guessed.
	if res.Usage.Tokens != 0 || res.Usage.ToolCalls != 0 {
		t.Errorf("usage = %+v, want zero token/tool-call counts", res.Usage)
	}
}

func TestShell_PassesScenarioAndBriefEnv(t *testing.T) {
	res, err := helperShell("echo-env", 30*time.Second).Diagnose(context.Background(), Task{
		Scenario: "cardinality-explosion-checkout", Brief: "series spike",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(res.Raw)
	if !strings.Contains(got, "scenario=cardinality-explosion-checkout") || !strings.Contains(got, "brief=series spike") {
		t.Errorf("raw = %q", got)
	}
}

func TestShell_NonZeroExitIsError(t *testing.T) {
	_, err := helperShell("fail", 30*time.Second).Diagnose(context.Background(), Task{Scenario: "s"})
	if err == nil {
		t.Fatal("expected error on non-zero exit")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should carry stderr: %v", err)
	}
}

func TestShell_EmptyOutputIsError(t *testing.T) {
	_, err := helperShell("empty", 30*time.Second).Diagnose(context.Background(), Task{Scenario: "s"})
	if err == nil {
		t.Fatal("expected error on empty output")
	}
}

func TestShell_TimeoutEnforced(t *testing.T) {
	start := time.Now()
	_, err := helperShell("hang", 300*time.Millisecond).Diagnose(context.Background(), Task{Scenario: "s"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("timeout not enforced, took %v", elapsed)
	}
}

func TestShell_NameFallsBackToCommand(t *testing.T) {
	if n := NewShell(ShellConfig{Command: "holmesgpt"}).Name(); n != "holmesgpt" {
		t.Errorf("Name() = %q, want holmesgpt", n)
	}
}
