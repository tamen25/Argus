package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/bench"
)

// R10: a cleanup that fails is recorded on its run, next to the diagnosis,
// and the run's result stands. When the last cleanup failed, Run says the
// environment was not restored.
func TestRun_CleanupFailureIsRecorded(t *testing.T) {
	inj := &fakeInjector{cleanupErrs: []error{errors.New("delete networkchaos: forbidden")}}
	rep, err := Run(context.Background(), testScenario(), &scriptAgent{answers: []string{perfect}},
		nil, inj, okProbe{}, fastOpts(1))
	if !errors.Is(err, ErrNotRestored) {
		t.Fatalf("err = %v, want ErrNotRestored", err)
	}
	run := rep.Runs[0]
	if run.Score == nil || run.Score.Score != 1 {
		t.Errorf("the diagnosis was lost: %+v", run)
	}
	if !strings.Contains(run.CleanupError, "forbidden") {
		t.Errorf("cleanup_error = %q", run.CleanupError)
	}
	if !strings.Contains(RenderReportMarkdown(rep), "could not be removed after repeat 0") {
		t.Error("the markdown report does not say the cleanup failed")
	}
}

// R10: when the probe cannot check that the next repeat starts clean, a
// failed cleanup stops the run: nothing would notice a leftover fault.
func TestRun_UnverifiableCleanupFailureStops(t *testing.T) {
	inj := &fakeInjector{cleanupErrs: []error{errors.New("timeout")}}
	rep, err := Run(context.Background(), testScenario(), &scriptAgent{answers: []string{perfect, perfect, perfect}},
		nil, inj, okProbe{}, fastOpts(3))
	if !errors.Is(err, ErrNotRestored) || len(rep.Runs) != 1 {
		t.Errorf("err = %v, runs = %d; want ErrNotRestored after the first repeat", err, len(rep.Runs))
	}
}

// R10: when the probe checks the baseline before every injection, the next
// repeat's reset is verified, so the run goes on; a later successful cleanup
// leaves the environment restored.
func TestRun_VerifiableCleanupFailureContinues(t *testing.T) {
	p := &clockedProbe{reached: []bool{true}, clean: []bool{true}, now: time.Unix(0, 0), step: time.Second}
	inj := &fakeInjector{cleanupErrs: []error{errors.New("timeout")}}
	rep, err := Run(context.Background(), steadyScenario(""), &scriptAgent{answers: []string{perfect, perfect}},
		nil, inj, p, clockedOpts(p, 2))
	if err != nil {
		t.Fatalf("err = %v, want nil: the last cleanup succeeded", err)
	}
	if len(rep.Runs) != 2 || rep.Runs[0].CleanupError == "" || rep.Runs[1].CleanupError != "" {
		t.Errorf("runs = %+v", rep.Runs)
	}
}

// lockingInjector is a fakeInjector that takes the environment lock.
type lockingInjector struct {
	fakeInjector
	lockErr         error
	locks, unlocks  int
	lockedDuringRun bool
}

func (l *lockingInjector) Lock(context.Context, string) error {
	l.locks++
	return l.lockErr
}

func (l *lockingInjector) Unlock(context.Context) error {
	l.unlocks++
	return nil
}

func (l *lockingInjector) Reset(ctx context.Context, sc bench.Scenario) error {
	l.lockedDuringRun = l.locks == 1 && l.unlocks == 0
	return l.fakeInjector.Reset(ctx, sc)
}

// R11: a run holds the environment lock from before its first reset until
// after its last cleanup, and releases it even when the cleanup failed.
func TestRun_HoldsTheLock(t *testing.T) {
	inj := &lockingInjector{fakeInjector: fakeInjector{cleanupErrs: []error{errors.New("x")}}}
	_, err := Run(context.Background(), testScenario(), &scriptAgent{answers: []string{perfect}}, nil, inj, okProbe{}, fastOpts(1))
	if !errors.Is(err, ErrNotRestored) {
		t.Fatalf("err = %v", err)
	}
	if !inj.lockedDuringRun || inj.locks != 1 || inj.unlocks != 1 {
		t.Errorf("locks=%d unlocks=%d locked during the run=%v", inj.locks, inj.unlocks, inj.lockedDuringRun)
	}
}

// R11: when another run holds the environment, nothing is touched.
func TestRun_LockHeldElsewhere(t *testing.T) {
	inj := &lockingInjector{lockErr: errors.New("another bench run is using this cluster")}
	_, err := Run(context.Background(), testScenario(), &scriptAgent{answers: []string{perfect}}, nil, inj, okProbe{}, fastOpts(1))
	if err == nil || inj.resets != 0 || inj.unlocks != 0 {
		t.Errorf("err = %v, resets = %d, unlocks = %d; want an error and nothing touched", err, inj.resets, inj.unlocks)
	}
}
