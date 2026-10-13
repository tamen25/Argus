package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
)

var (
	_ backend.CallResourceHandler   = (*App)(nil)
	_ instancemgmt.InstanceDisposer = (*App)(nil)
	_ backend.CheckHealthHandler    = (*App)(nil)
)

// defaultEngineURL matches the in-cluster deployment
// (deploy/kind/argus-engine.yaml); `make demo` overrides via jsonData.
const defaultEngineURL = "http://argus-engine.argus.svc:8080"

// settings is the plugin jsonData contract: the engine URL, set on the app's
// configuration page. The engine's API token is a secure setting instead
// (secureJsonData.apiToken): Grafana stores it encrypted and never sends it to
// a browser.
type settings struct {
	EngineURL string `json:"engineUrl"`
}

// apiTokenKey is the secureJsonData key holding the engine's bearer token
// (the engine's ARGUS_API_TOKEN).
const apiTokenKey = "apiToken"

// App proxies the browser's resource calls to the argus engine, so the
// engine never needs to be reachable from user browsers.
type App struct {
	backend.CallResourceHandler
	engineURL string
	apiToken  string
	client    *http.Client
}

// NewApp reads instance settings and mounts the resource routes.
func NewApp(_ context.Context, as backend.AppInstanceSettings) (instancemgmt.Instance, error) {
	var s settings
	if len(as.JSONData) > 0 {
		if err := json.Unmarshal(as.JSONData, &s); err != nil {
			return nil, err
		}
	}
	engineURL, err := normalizeEngineURL(s.EngineURL)
	if err != nil {
		return nil, err
	}
	app := &App{
		engineURL: engineURL,
		apiToken:  as.DecryptedSecureJSONData[apiTokenKey],
		client:    &http.Client{Timeout: 10 * time.Second},
	}

	mux := http.NewServeMux()
	app.registerRoutes(mux)
	app.CallResourceHandler = httpadapter.New(mux)
	return app, nil
}

// Dispose tells the SDK the instance can be dropped on settings change.
func (a *App) Dispose() {}

// CheckHealth reports whether the engine answers, and whether it accepts the
// configured API token: a reachable engine that rejects every API call would
// otherwise look healthy here and fail on every page.
func (a *App) CheckHealth(ctx context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	fail := func(msg string) (*backend.CheckHealthResult, error) {
		return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: msg}, nil
	}

	resp, err := a.get(ctx, "/healthz")
	if err != nil {
		return fail("engine unreachable: " + err.Error()) // admins only: details help
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail("engine /healthz returned " + resp.Status)
	}

	// Any /api/ path goes through the engine's token check before routing,
	// so a 401 here means the token is wrong; anything else means it passed.
	resp, err = a.get(ctx, "/api/")
	if err != nil {
		return fail("engine API unreachable: " + err.Error())
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fail("the engine rejected the API token: set it to the engine's ARGUS_API_TOKEN")
	}
	return &backend.CheckHealthResult{Status: backend.HealthStatusOk, Message: "engine reachable"}, nil
}

// get sends an authenticated GET to the engine.
func (a *App) get(ctx context.Context, pathAndQuery string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.engineURL+pathAndQuery, nil)
	if err != nil {
		return nil, err
	}
	if a.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.apiToken)
	}
	return a.client.Do(req)
}

// normalizeEngineURL checks the configured engine URL and returns it without a
// trailing slash, so engineURL+"/api/..." is always one path. Empty means the
// in-cluster default. Anything that is not an http(s) URL with a host is an
// error, shown on the app's configuration page, rather than a proxy that fails
// on every request.
func normalizeEngineURL(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return defaultEngineURL, nil
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("engine URL %q: want http(s)://host[:port]", raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}
