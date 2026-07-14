package plugin

import (
	"context"
	"encoding/json"
	"net/http"
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

// settings is the plugin jsonData contract (Settings page, Phase 1: engine
// URL only; token lands with the Settings page work).
type settings struct {
	EngineURL string `json:"engineUrl"`
}

// App proxies the browser's resource calls to the argus engine, so the
// engine never needs to be reachable from user browsers.
type App struct {
	backend.CallResourceHandler
	engineURL string
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
	app := &App{
		engineURL: s.EngineURL,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
	if app.engineURL == "" {
		app.engineURL = defaultEngineURL
	}

	mux := http.NewServeMux()
	app.registerRoutes(mux)
	app.CallResourceHandler = httpadapter.New(mux)
	return app, nil
}

// Dispose tells the SDK the instance can be dropped on settings change.
func (a *App) Dispose() {}

// CheckHealth reports whether the engine answers /healthz through the proxy.
func (a *App) CheckHealth(ctx context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.engineURL+"/healthz", nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: "engine unreachable: " + err.Error(),
		}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: "engine /healthz returned " + resp.Status,
		}, nil
	}
	return &backend.CheckHealthResult{Status: backend.HealthStatusOk, Message: "engine reachable"}, nil
}
