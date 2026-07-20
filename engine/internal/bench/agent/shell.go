package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ShellConfig configures a shell-wrapped agent: an existing open-source agent
// (HolmesGPT, K8sGPT, …) run as a subprocess and measured as a bench subject.
type ShellConfig struct {
	Name    string
	Command string
	Args    []string
	Env     []string // extra environment, appended to the parent environment
	Dir     string
	Timeout time.Duration // wall-clock cap; default 10m
}

// ShellAgent runs an external agent binary and captures its native output.
//
// Honesty note (architecture rule 7): token and tool-call budgets are NOT
// enforceable for a shell agent — its model calls and tool use happen inside a
// process we do not control. The only cap we can apply is wall-clock Timeout,
// and Usage is reported with the unknown dimensions left at zero rather than
// guessed. Reports must state that shell-agent runs were time-capped, not
// budget-capped.
//
// The captured output is the agent's native format, so it MUST go through a
// Normalizer (a deterministic per-agent one where possible, otherwise the
// LLM judge, whose method is recorded) before scoring.
type ShellAgent struct {
	cfg ShellConfig
}

// NewShell builds a shell agent, defaulting the timeout and the display name.
func NewShell(cfg ShellConfig) *ShellAgent {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Minute
	}
	if cfg.Name == "" {
		cfg.Name = cfg.Command
	}
	return &ShellAgent{cfg: cfg}
}

// Name identifies the agent in the run record.
func (s *ShellAgent) Name() string { return s.cfg.Name }

// Diagnose runs the external agent. The incident brief is delivered both on
// stdin and via ARGUS_BRIEF/ARGUS_SCENARIO, so wrappers can consume whichever
// suits the tool. Stdout is returned verbatim as the raw diagnosis.
func (s *ShellAgent) Diagnose(ctx context.Context, task Task) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.cfg.Command, s.cfg.Args...)
	cmd.Dir = s.cfg.Dir
	cmd.Env = append(os.Environ(), s.cfg.Env...)
	cmd.Env = append(cmd.Env,
		"ARGUS_SCENARIO="+task.Scenario,
		"ARGUS_BRIEF="+task.Brief,
	)
	cmd.Stdin = strings.NewReader(task.Brief)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	usage := Usage{Steps: 1}
	if err := cmd.Run(); err != nil {
		return Result{Usage: usage}, fmt.Errorf("shell agent %s: %w (stderr: %s)",
			s.cfg.Name, err, truncateStr(stderr.String(), 512))
	}
	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) == 0 {
		return Result{Usage: usage}, fmt.Errorf("shell agent %s: produced no output", s.cfg.Name)
	}
	return Result{Raw: json.RawMessage(out), Usage: usage}, nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var _ Agent = (*ShellAgent)(nil)
