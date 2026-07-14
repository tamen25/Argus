package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute version: %v", err)
	}
	if got := out.String(); !strings.HasPrefix(got, "argus ") {
		t.Errorf("version output = %q, want prefix %q", got, "argus ")
	}
}

// Container and `go install` builds have no pin file on disk; the build-time
// embedded version (ldflags) must back the report's spec disclosure.
func TestReadSpecVersionEmbeddedFallback(t *testing.T) {
	old := embeddedSpecVersion
	defer func() { embeddedSpecVersion = old }()

	embeddedSpecVersion = "deadbeef1234"
	if got := readSpecVersion("does/not/exist"); got != "deadbeef1234" {
		t.Errorf("readSpecVersion fallback = %q, want embedded version", got)
	}
	embeddedSpecVersion = ""
	if got := readSpecVersion("does/not/exist"); got != "unknown" {
		t.Errorf("readSpecVersion without embed = %q, want unknown", got)
	}
}

func TestServeHealthzAndShutdown(t *testing.T) {
	// Grab a free port, then hand it to serve.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, serveConfig{addr: addr}) }()

	// Poll until the server answers (it needs a moment to bind).
	url := fmt.Sprintf("http://%s/healthz", addr)
	var resp *http.Response
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err = http.Get(url)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "ok" {
		t.Errorf("healthz = %d %q, want 200 ok", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned error on shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not shut down after context cancel")
	}
}
