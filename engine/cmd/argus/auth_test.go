package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// R25: without a token, the API may listen on loopback only, unless the
// operator says --insecure-no-auth.
func TestCheckAuthBoundary(t *testing.T) {
	cases := []struct {
		addr, token string
		noAuth, ok  bool
	}{
		{":8080", "", false, false}, // every interface, open: refused
		{"0.0.0.0:8080", "", false, false},
		{"10.0.0.5:8080", "", false, false},
		{"127.0.0.1:8099", "", false, true}, // the CI dogfood gate
		{"[::1]:8080", "", false, true},
		{"localhost:8080", "", false, true},
		{":8080", "secret", false, true},
		{":8080", "", true, true},
	}
	for _, c := range cases {
		err := checkAuthBoundary(c.addr, c.token, c.noAuth)
		if (err == nil) != c.ok {
			t.Errorf("addr=%q token=%v no-auth=%v: err = %v, want ok %v", c.addr, c.token != "", c.noAuth, err, c.ok)
		}
	}
}

func TestHealthcheck(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	defer srv.Close()
	if err := healthcheck(context.Background(), srv.URL, time.Second); err != nil {
		t.Errorf("healthy engine: %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := healthcheck(context.Background(), srv.URL, time.Second); err == nil {
		t.Error("a 503 passed the health check")
	}
	if err := healthcheck(context.Background(), "http://127.0.0.1:1/healthz", time.Second); err == nil {
		t.Error("an engine that is not running passed the health check")
	}
}
