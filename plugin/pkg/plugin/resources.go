package plugin

import (
	"io"
	"net/http"
)

// engineRoutes maps plugin resource paths to engine API paths. The browser
// only ever talks to Grafana; this backend holds the engine connection
// (master plan §3.3 — plus §8: /resources/scores et al.).
var engineRoutes = map[string]string{
	"/scores":       "/api/report",
	"/aggregates":   "/api/aggregates",
	"/remediation":  "/api/remediation",
	"/servicegraph": "/api/servicegraph",
	"/cost":         "/api/cost",
	"/backtest":     "/api/backtest",
}

func (a *App) registerRoutes(mux *http.ServeMux) {
	for resource, enginePath := range engineRoutes {
		mux.HandleFunc(resource, a.proxyTo(enginePath))
	}
}

// proxyTo forwards GETs (with query string) to the engine and relays the
// response verbatim — status codes included, so honest engine errors (404
// for absent findings, notes in reports) reach the UI unchanged.
func (a *App) proxyTo(enginePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		url := a.engineURL + enginePath
		if req.URL.RawQuery != "" {
			url += "?" + req.URL.RawQuery
		}
		outReq, err := http.NewRequestWithContext(req.Context(), http.MethodGet, url, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp, err := a.client.Do(outReq)
		if err != nil {
			http.Error(w, "engine unreachable: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}
