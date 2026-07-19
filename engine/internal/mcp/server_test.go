package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fakeBackend records the last call to each port and returns canned payloads,
// so tests assert argument plumbing without a live backend. The metrics and
// logs ports both declare a QueryRange (with different signatures), so each port
// gets a thin adapter over this shared recorder.
type fakeBackend struct {
	lastQuery string
	lastAt    time.Time
	lastStart time.Time
	lastEnd   time.Time
	lastStep  time.Duration
	lastLimit int
	lastNS    string
	lastState string
	ranged    bool
}

type fakeMetrics struct{ f *fakeBackend }
type fakeLogs struct{ f *fakeBackend }
type fakeTraces struct{ f *fakeBackend }
type fakeTopo struct{ f *fakeBackend }
type fakeAlerts struct{ f *fakeBackend }

func (m fakeMetrics) QueryInstant(_ context.Context, q string, at time.Time) (json.RawMessage, error) {
	m.f.lastQuery, m.f.lastAt, m.f.ranged = q, at, false
	return json.RawMessage(`{"ok":"instant"}`), nil
}
func (m fakeMetrics) QueryRange(_ context.Context, q string, s, e time.Time, step time.Duration) (json.RawMessage, error) {
	m.f.lastQuery, m.f.lastStart, m.f.lastEnd, m.f.lastStep, m.f.ranged = q, s, e, step, true
	return json.RawMessage(`{"ok":"range"}`), nil
}
func (l fakeLogs) QueryRange(_ context.Context, q string, s, e time.Time, limit int) (json.RawMessage, error) {
	l.f.lastQuery, l.f.lastStart, l.f.lastEnd, l.f.lastLimit = q, s, e, limit
	return json.RawMessage(`{"ok":"logs"}`), nil
}
func (t fakeTraces) Search(_ context.Context, q string, limit int) (json.RawMessage, error) {
	t.f.lastQuery, t.f.lastLimit = q, limit
	return json.RawMessage(`{"ok":"traces"}`), nil
}
func (t fakeTopo) Topology(_ context.Context, ns string) (json.RawMessage, error) {
	t.f.lastNS = ns
	return json.RawMessage(`{"ok":"topology"}`), nil
}
func (a fakeAlerts) ListAlerts(_ context.Context, state string) (json.RawMessage, error) {
	a.f.lastState = state
	return json.RawMessage(`{"ok":"alerts"}`), nil
}

func fullBackends(f *fakeBackend) Backends {
	return Backends{
		Metrics:  fakeMetrics{f},
		Logs:     fakeLogs{f},
		Traces:   fakeTraces{f},
		Topology: fakeTopo{f},
		Alerts:   fakeAlerts{f},
	}
}

func TestNewServer_RegistersFullSurface(t *testing.T) {
	r, err := NewServer(fullBackends(&fakeBackend{}))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tl := range r.List() {
		got[tl.Name] = true
		if !tl.ReadOnly {
			t.Errorf("tool %q not marked read-only", tl.Name)
		}
		if !json.Valid(tl.InputSchema) {
			t.Errorf("tool %q has invalid input schema", tl.Name)
		}
	}
	for _, want := range []string{ToolQueryPrometheus, ToolQueryLoki, ToolSearchTraces, ToolGetK8sTopology, ToolListAlerts} {
		if !got[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestNewServer_PartialSurface(t *testing.T) {
	r, err := NewServer(Backends{Metrics: fakeMetrics{&fakeBackend{}}})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(r.List()); n != 1 {
		t.Fatalf("tools = %d, want 1 (only metrics backend)", n)
	}
}

func TestNewServer_EmptyIsError(t *testing.T) {
	if _, err := NewServer(Backends{}); err == nil {
		t.Error("expected error for empty backend set")
	}
}

func TestPromTool_InstantVsRange(t *testing.T) {
	f := &fakeBackend{}
	r, _ := NewServer(fullBackends(f))

	// Instant (no start/end/step).
	out, err := r.Call(context.Background(), ToolQueryPrometheus, json.RawMessage(`{"query":"up"}`))
	if err != nil {
		t.Fatal(err)
	}
	if f.ranged {
		t.Error("expected instant query")
	}
	if string(out) != `{"ok":"instant"}` {
		t.Errorf("out = %s", out)
	}

	// Range.
	_, err = r.Call(context.Background(), ToolQueryPrometheus,
		json.RawMessage(`{"query":"up","start":"2026-07-20T00:00:00Z","end":"2026-07-20T01:00:00Z","step":"30s"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !f.ranged {
		t.Error("expected range query")
	}
	if f.lastStep != 30*time.Second {
		t.Errorf("step = %v, want 30s", f.lastStep)
	}
}

func TestLokiTool_Defaults(t *testing.T) {
	f := &fakeBackend{}
	r, _ := NewServer(fullBackends(f))
	if _, err := r.Call(context.Background(), ToolQueryLoki, json.RawMessage(`{"query":"{app=\"x\"}"}`)); err != nil {
		t.Fatal(err)
	}
	if f.lastLimit != 100 {
		t.Errorf("default limit = %d, want 100", f.lastLimit)
	}
	if d := f.lastEnd.Sub(f.lastStart); d != time.Hour {
		t.Errorf("default window = %v, want 1h", d)
	}
}

func TestTools_RequireQuery(t *testing.T) {
	f := &fakeBackend{}
	r, _ := NewServer(fullBackends(f))
	for _, name := range []string{ToolQueryPrometheus, ToolQueryLoki, ToolSearchTraces} {
		if _, err := r.Call(context.Background(), name, json.RawMessage(`{}`)); err == nil {
			t.Errorf("%s: expected error on missing query", name)
		}
	}
}

func TestTools_RejectUnknownArg(t *testing.T) {
	f := &fakeBackend{}
	r, _ := NewServer(fullBackends(f))
	_, err := r.Call(context.Background(), ToolListAlerts, json.RawMessage(`{"bogus":1}`))
	if err == nil || !strings.Contains(err.Error(), "bad arguments") {
		t.Errorf("expected bad-arguments error, got %v", err)
	}
}

func TestOptionalArgTools_EmptyOK(t *testing.T) {
	f := &fakeBackend{}
	r, _ := NewServer(fullBackends(f))
	// topology and alerts have no required args; empty object and empty raw both OK.
	if _, err := r.Call(context.Background(), ToolGetK8sTopology, nil); err != nil {
		t.Errorf("topology empty args: %v", err)
	}
	if _, err := r.Call(context.Background(), ToolListAlerts, json.RawMessage(`{"state":"firing"}`)); err != nil {
		t.Errorf("alerts: %v", err)
	}
	if f.lastState != "firing" {
		t.Errorf("state = %q, want firing", f.lastState)
	}
}

func TestRegistry_UnknownTool(t *testing.T) {
	r, _ := NewServer(Backends{Metrics: fakeMetrics{&fakeBackend{}}})
	if _, err := r.Call(context.Background(), "nope", nil); err == nil {
		t.Error("expected unknown-tool error")
	}
}

func TestRegistry_DuplicateRejected(t *testing.T) {
	r := NewRegistry()
	tl := Tool{Name: "x", Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil }}
	if err := r.Register(tl); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(tl); err == nil {
		t.Error("expected duplicate error")
	}
}
