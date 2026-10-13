package plugin

import (
	"io"
	"net/http"

	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
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
	"/bench":        "/api/bench",
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
		pathAndQuery := enginePath
		if req.URL.RawQuery != "" {
			pathAndQuery += "?" + req.URL.RawQuery
		}
		resp, err := a.get(req.Context(), pathAndQuery)
		if err != nil {
			// Any Grafana viewer can see this response: the details, which
			// name the engine's internal address, go to the server log only.
			log.DefaultLogger.Error("argus engine unreachable", "path", enginePath, "error", err)
			http.Error(w, "engine unreachable (details in the Grafana server log)", http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}
