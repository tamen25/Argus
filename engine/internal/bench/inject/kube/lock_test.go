package kube

import (
	"context"
	"strings"
	"testing"
)

// R11: Lock creates the lock ConfigMap in default, outside the sweep's
// label, and Unlock deletes it.
func TestLockAndUnlock(t *testing.T) {
	inj, logPath := helperInjector(t, false)
	if err := inj.Lock(context.Background(), "scenario-1, agent x"); err != nil {
		t.Fatal(err)
	}
	if err := inj.Unlock(context.Background()); err != nil {
		t.Fatal(err)
	}
	log := readLog(t, logPath)
	for _, want := range []string{
		"-n default create configmap " + LockName + " --from-literal=holder=scenario-1, agent x",
		"-n default delete configmap " + LockName + " --ignore-not-found",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("kubectl calls missing %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "managed-by") && strings.Contains(log, "create configmap") {
		t.Error("the lock carries the sweep's label, so a reset would delete it")
	}
}

// R11: a lock held by another run is refused with the command that clears a
// stale one.
func TestLockHeldElsewhere(t *testing.T) {
	inj, _ := helperInjector(t, false)
	t.Setenv("ARGUS_KUBECTL_LOCK_HELD", "1")
	err := inj.Lock(context.Background(), "me")
	if err == nil || !strings.Contains(err.Error(), "another bench run") ||
		!strings.Contains(err.Error(), "kubectl delete configmap "+LockName) {
		t.Errorf("err = %v, want the held-lock message with the clearing command", err)
	}
}
