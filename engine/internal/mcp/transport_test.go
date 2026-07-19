package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// drive feeds newline-delimited requests through Serve and returns the decoded
// responses (notifications produce none).
func drive(t *testing.T, reg *Registry, requests ...string) []rpcResponse {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out strings.Builder
	if err := Serve(context.Background(), in, &out, reg, ServerInfo{Name: "argus", Version: "test"}); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resps []rpcResponse
	dec := json.NewDecoder(strings.NewReader(out.String()))
	for dec.More() {
		var r rpcResponse
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("decode response: %v (raw: %q)", err, out.String())
		}
		resps = append(resps, r)
	}
	return resps
}

func testReg(t *testing.T) *Registry {
	t.Helper()
	r, err := NewServer(fullBackends(&fakeBackend{}))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestServe_Initialize(t *testing.T) {
	resps := drive(t, testReg(t), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if len(resps) != 1 {
		t.Fatalf("responses = %d, want 1", len(resps))
	}
	var res map[string]any
	if err := json.Unmarshal(mustResult(t, resps[0]), &res); err != nil {
		t.Fatal(err)
	}
	if res["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v", res["protocolVersion"])
	}
	if _, ok := res["capabilities"].(map[string]any)["tools"]; !ok {
		t.Error("missing tools capability")
	}
}

func TestServe_ToolsList(t *testing.T) {
	resps := drive(t, testReg(t), `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var res struct {
		Tools []toolDescriptor `json:"tools"`
	}
	if err := json.Unmarshal(mustResult(t, resps[0]), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 5 {
		t.Fatalf("tools = %d, want 5", len(res.Tools))
	}
	for _, td := range res.Tools {
		if !td.Annotations.ReadOnlyHint {
			t.Errorf("tool %q missing readOnlyHint", td.Name)
		}
		if !json.Valid(td.InputSchema) {
			t.Errorf("tool %q invalid schema", td.Name)
		}
	}
}

func TestServe_ToolsCall(t *testing.T) {
	resps := drive(t, testReg(t),
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"query_prometheus","arguments":{"query":"up"}}}`)
	var res toolCallResult
	if err := json.Unmarshal(mustResult(t, resps[0]), &res); err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res)
	}
	if len(res.Content) != 1 || res.Content[0].Text != `{"ok":"instant"}` {
		t.Errorf("content = %+v", res.Content)
	}
}

func TestServe_ToolError_IsResultNotProtocolError(t *testing.T) {
	// Missing required arg -> handler error -> isError result, not a JSON-RPC error.
	resps := drive(t, testReg(t),
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"query_prometheus","arguments":{}}}`)
	if resps[0].Error != nil {
		t.Fatalf("expected result, got protocol error: %+v", resps[0].Error)
	}
	var res toolCallResult
	if err := json.Unmarshal(mustResult(t, resps[0]), &res); err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected isError=true")
	}
}

func TestServe_UnknownMethod(t *testing.T) {
	resps := drive(t, testReg(t), `{"jsonrpc":"2.0","id":5,"method":"does/not/exist"}`)
	if resps[0].Error == nil || resps[0].Error.Code != codeMethodNotFnd {
		t.Errorf("expected method-not-found, got %+v", resps[0].Error)
	}
}

func TestServe_NotificationHasNoResponse(t *testing.T) {
	resps := drive(t, testReg(t),
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":6,"method":"ping"}`)
	// Only the ping (which has an id) gets a response.
	if len(resps) != 1 {
		t.Fatalf("responses = %d, want 1 (notification suppressed)", len(resps))
	}
	if string(resps[0].ID) != "6" {
		t.Errorf("id = %s, want 6", resps[0].ID)
	}
}

func mustResult(t *testing.T, r rpcResponse) json.RawMessage {
	t.Helper()
	if r.Error != nil {
		t.Fatalf("unexpected error response: %+v", r.Error)
	}
	b, err := json.Marshal(r.Result)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
