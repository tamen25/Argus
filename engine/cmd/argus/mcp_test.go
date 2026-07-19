package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPCommand_NoBackendsErrors(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"mcp"})
	root.SetIn(strings.NewReader(""))
	root.SetOut(&bytes.Buffer{})
	if err := root.Execute(); err == nil {
		t.Error("expected error when no backend URL is given")
	}
}

func TestMCPCommand_ToolsListOverStdio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	root := newRootCmd()
	root.SetArgs([]string{"mcp", "--mimir-url", srv.URL})
	root.SetIn(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"))
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("execute mcp: %v", err)
	}

	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (raw %q)", err, out.String())
	}
	// Only Mimir configured -> query_prometheus + list_alerts.
	names := map[string]bool{}
	for _, tl := range resp.Result.Tools {
		names[tl.Name] = true
	}
	if !names["query_prometheus"] || !names["list_alerts"] {
		t.Errorf("expected mimir-backed tools, got %v", names)
	}
	if names["query_loki"] {
		t.Error("query_loki should be absent without --loki-url")
	}
}
