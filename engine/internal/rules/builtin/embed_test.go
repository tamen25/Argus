package builtin

import "testing"

func TestBuiltinRulesLoad(t *testing.T) {
	rs, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ids := map[string]bool{}
	for _, r := range rs {
		ids[r.ID] = true
	}
	for _, want := range []string{"RES-005", "MET-001", "ARG-RES-001"} {
		if !ids[want] {
			t.Errorf("builtin missing %s (run `go generate ./...` after editing /rules)", want)
		}
	}
}
