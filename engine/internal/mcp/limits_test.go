package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// R30: arguments that a backend would refuse, or that would make it do
// unbounded work, are refused before the backend is called.
func TestToolsRejectBadArgumentsBeforeTheBackend(t *testing.T) {
	cases := map[string]struct{ tool, args string }{
		"reversed range":    {ToolQueryPrometheus, `{"query":"up","start":"2026-07-20T01:00:00Z","end":"2026-07-20T00:00:00Z","step":"30s"}`},
		"zero step":         {ToolQueryPrometheus, `{"query":"up","start":"2026-07-20T00:00:00Z","end":"2026-07-20T01:00:00Z","step":"0s"}`},
		"too many points":   {ToolQueryPrometheus, `{"query":"up","start":"2026-07-01T00:00:00Z","end":"2026-07-20T00:00:00Z","step":"1s"}`},
		"reversed log span": {ToolQueryLoki, `{"query":"{app=\"x\"}","start":"2026-07-20T01:00:00Z","end":"2026-07-20T00:00:00Z"}`},
		"negative limit":    {ToolQueryLoki, `{"query":"{app=\"x\"}","limit":-1}`},
		"huge log limit":    {ToolQueryLoki, `{"query":"{app=\"x\"}","limit":1000000}`},
		"huge trace limit":  {ToolSearchTraces, `{"query":"{}","limit":1000000}`},
		"trailing data":     {ToolQueryPrometheus, `{"query":"up"} {"query":"down"}`},
	}
	for name, c := range cases {
		f := &fakeBackend{}
		r, _ := NewServer(fullBackends(f))
		if _, err := r.Call(context.Background(), c.tool, json.RawMessage(c.args)); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if f.lastQuery != "" {
			t.Errorf("%s: the backend was called (query %q)", name, f.lastQuery)
		}
	}
}

// R30: a bad line costs that message only (one message per line, so the
// stream cannot desynchronize); jsonrpc must be "2.0"; a line longer than
// maxFrame ends the session.
func TestServe_BadMessages(t *testing.T) {
	resps := drive(t, testReg(t),
		`{not json`,
		`{"jsonrpc":"1.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"} {"jsonrpc":"2.0","id":4,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":5,"method":"ping"}`,
	)
	if len(resps) != 4 {
		t.Fatalf("responses = %d, want one per line", len(resps))
	}
	wantCodes := []int{codeParseError, codeInvalidRequest, codeParseError}
	for i, code := range wantCodes {
		if resps[i].Error == nil || resps[i].Error.Code != code {
			t.Errorf("response %d = %+v, want error code %d", i, resps[i], code)
		}
	}
	if resps[1].Error == nil || string(resps[1].ID) != "2" {
		t.Errorf("the invalid request's error should carry its id: %+v", resps[1])
	}
	if resps[3].Error != nil || string(resps[3].ID) != "5" {
		t.Errorf("the line after the bad ones was not served: %+v", resps[3])
	}

	var out strings.Builder
	huge := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"` + strings.Repeat("x", maxFrame) + `"}` + "\n")
	if err := Serve(context.Background(), huge, &out, testReg(t), ServerInfo{}); err == nil {
		t.Error("a line longer than maxFrame was accepted")
	}
}

// blockingReader never returns, like stdin with nothing typed.
type blockingReader struct{ unblock chan struct{} }

func (b blockingReader) Read([]byte) (int, error) {
	<-b.unblock
	return 0, context.Canceled
}

// R30: Serve stops when its context is cancelled, even while a read is
// blocked waiting for the next message.
func TestServe_StopsWhileWaitingForInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	in := blockingReader{unblock: make(chan struct{})}
	defer close(in.unblock)
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, in, &strings.Builder{}, testReg(t), ServerInfo{}) }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("Serve returned no error on cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve kept waiting on input after its context was cancelled")
	}
}
