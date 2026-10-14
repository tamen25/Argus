package httpclient

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// R28: a redirect is refused, so a credential header is never sent to the
// host it names.
func TestNewRefusesRedirects(t *testing.T) {
	var leaked string
	elsewhere := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-Api-Key")
	}))
	defer elsewhere.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()

	req, _ := http.NewRequest(http.MethodGet, redirecting.URL, nil)
	req.Header.Set("X-Api-Key", "secret")
	resp, err := New(5 * time.Second).Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("the redirect was followed")
	}
	if !strings.Contains(err.Error(), "refusing to follow a redirect to "+elsewhere.URL) {
		t.Errorf("err = %v, want the redirect target named", err)
	}
	if leaked != "" {
		t.Errorf("the other host received the key %q", leaked)
	}
}
