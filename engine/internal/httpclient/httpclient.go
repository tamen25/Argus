// Package httpclient builds the HTTP clients the engine uses to reach
// backends and model endpoints.
package httpclient

import (
	"fmt"
	"net/http"
	"time"
)

// New returns a client with the given timeout that does not follow
// redirects.
//
// Go's default client follows up to ten, and on a redirect to another host it
// drops only the standard credential headers (Authorization, Cookie). The
// headers these clients send are not all standard: x-api-key to Anthropic,
// X-Scope-OrgID to Mimir, Loki and Tempo. Following a redirect would hand
// them to whatever host it names. None of these APIs answers with a redirect
// in normal use, so one is an error that names where it pointed; the fix is
// to configure the endpoint it redirects to.
func New(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: refuseRedirect}
}

func refuseRedirect(req *http.Request, _ []*http.Request) error {
	return fmt.Errorf("refusing to follow a redirect to %s: configure that URL instead", req.URL.Redacted())
}
