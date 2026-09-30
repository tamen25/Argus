package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// labelServer answers every request with body and records the last request.
func labelServer(t *testing.T, body string, got *http.Request) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = *r
		_, _ = w.Write([]byte(body))
	}))
}

func TestMimir_CatalogCallsTheLabelAPIsOverARecentWindow(t *testing.T) {
	var got http.Request
	srv := labelServer(t, `{"status":"success","data":["up","http_requests_total"]}`, &got)
	defer srv.Close()
	m := NewMimir(srv.URL, "team-a")

	names, err := m.MetricNames(context.Background(), `{job="x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "up,http_requests_total" {
		t.Errorf("names = %v", names)
	}
	if got.URL.Path != "/prometheus/api/v1/label/__name__/values" {
		t.Errorf("path = %s", got.URL.Path)
	}
	q := got.URL.Query()
	if q.Get("match[]") != `{job="x"}` || q.Get("start") == "" || q.Get("end") == "" {
		t.Errorf("query = %v, want the selector and a start/end window", q)
	}
	if got.Header.Get("X-Scope-OrgID") != "team-a" {
		t.Errorf("tenant header = %q", got.Header.Get("X-Scope-OrgID"))
	}

	if _, err := m.LabelNames(context.Background(), ""); err != nil || got.URL.Path != "/prometheus/api/v1/labels" {
		t.Errorf("LabelNames: path %s err %v", got.URL.Path, err)
	}
	if got.URL.Query().Has("match[]") {
		t.Error("an empty selector must not be sent as match[]")
	}
	if _, err := m.LabelValues(context.Background(), "job", ""); err != nil || got.URL.Path != "/prometheus/api/v1/label/job/values" {
		t.Errorf("LabelValues: path %s err %v", got.URL.Path, err)
	}
}

func TestStringList_RejectsAnErrorAnswer(t *testing.T) {
	if _, err := stringList(json.RawMessage(`{"status":"error","data":[]}`)); err == nil {
		t.Error("an error status was read as an empty list")
	}
	if _, err := stringList(json.RawMessage(`not json`)); err == nil {
		t.Error("an undecodable body was accepted")
	}
}

func TestLoki_CatalogPaths(t *testing.T) {
	var got http.Request
	srv := labelServer(t, `{"status":"success","data":["job","service_name"]}`, &got)
	defer srv.Close()
	l := NewLoki(srv.URL, "")
	if v, err := l.LogLabelNames(context.Background()); err != nil || len(v) != 2 || got.URL.Path != "/loki/api/v1/labels" {
		t.Errorf("LogLabelNames: %v %v %s", v, err, got.URL.Path)
	}
	if _, err := l.LogLabelValues(context.Background(), "service_name"); err != nil ||
		got.URL.Path != "/loki/api/v1/label/service_name/values" {
		t.Errorf("LogLabelValues: %v %s", err, got.URL.Path)
	}
}

// TraceQL writes attributes with their scope (resource.service.name), and an
// agent has to use the name exactly as the query language wants it.
func TestTempo_TagNamesAreScopedLikeTraceQL(t *testing.T) {
	var got http.Request
	srv := labelServer(t, `{"scopes":[`+
		`{"name":"resource","tags":["service.name"]},`+
		`{"name":"span","tags":["http.method"]},`+
		`{"name":"intrinsic","tags":["duration","status"]}]}`, &got)
	defer srv.Close()
	names, err := NewTempo(srv.URL, "").TraceTagNames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "resource.service.name,span.http.method,duration,status" {
		t.Errorf("names = %v", names)
	}
	if got.URL.Path != "/api/v2/search/tags" {
		t.Errorf("path = %s", got.URL.Path)
	}
}

func TestTempo_TagValues(t *testing.T) {
	var got http.Request
	srv := labelServer(t, `{"tagValues":[{"type":"string","value":"a"},{"type":"string","value":"b"}]}`, &got)
	defer srv.Close()
	values, err := NewTempo(srv.URL, "").TraceTagValues(context.Background(), "resource.service.name")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(values, ",") != "a,b" || got.URL.Path != "/api/v2/search/tag/resource.service.name/values" {
		t.Errorf("values %v from %s", values, got.URL.Path)
	}
}

const kubectlWorkloads = `{"items":[
 {"kind":"Deployment","metadata":{"name":"payment","namespace":"shop"},"status":{"readyReplicas":0}},
 {"kind":"DaemonSet","metadata":{"name":"collector","namespace":"obs"}},
 {"kind":"Deployment","metadata":{"name":"cart","namespace":"shop"}}]}`

func TestKubeTopology_ListsWorkloadIdentitiesAndTheServiceGraph(t *testing.T) {
	var got http.Request
	mimir := labelServer(t, `{"status":"success","data":{"resultType":"vector","result":[`+
		`{"metric":{"client":"web","server":"cart"},"value":[1,"0.5"]},`+
		`{"metric":{"client":"checkout","server":"payment"},"value":[1,"0.1"]}]}}`, &got)
	defer mimir.Close()

	var args []string
	k := NewKubeTopology("kind-test", "app.kubernetes.io/managed-by!=argus-bench", NewMimir(mimir.URL, ""))
	k.run = func(_ context.Context, a ...string) ([]byte, error) {
		args = a
		return []byte(kubectlWorkloads), nil
	}

	raw, err := k.Topology(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	cmdline := strings.Join(args, " ")
	for _, want := range []string{
		"--context kind-test", "get deployments,statefulsets,daemonsets", "--all-namespaces",
		"-l app.kubernetes.io/managed-by!=argus-bench",
	} {
		if !strings.Contains(cmdline, want) {
			t.Errorf("kubectl %s: missing %q", cmdline, want)
		}
	}
	if !strings.HasPrefix(cmdline, "--context kind-test get ") {
		t.Errorf("kubectl %s: only a read is allowed", cmdline)
	}

	var ans topologyAnswer
	if err := json.Unmarshal(raw, &ans); err != nil {
		t.Fatal(err)
	}
	var ws []string
	for _, w := range ans.Workloads {
		ws = append(ws, w.Kind+"/"+w.Namespace+"/"+w.Name)
	}
	if strings.Join(ws, ",") != "DaemonSet/obs/collector,Deployment/shop/cart,Deployment/shop/payment" {
		t.Errorf("workloads = %v, want identities sorted by namespace then name", ws)
	}
	// Identity only: nothing about which workload is unhealthy.
	if strings.Contains(string(raw), "readyReplicas") || strings.Contains(string(raw), "status") {
		t.Errorf("topology leaks workload status: %s", raw)
	}
	if len(ans.ServiceGraph) != 2 || ans.ServiceGraph[0] != (Edge{Client: "checkout", Server: "payment"}) {
		t.Errorf("service graph = %+v, want both edges sorted", ans.ServiceGraph)
	}
	if !strings.Contains(got.URL.RawQuery, "traces_service_graph_request_total") {
		t.Errorf("service graph query = %s", got.URL.RawQuery)
	}

	// Scoped to one namespace.
	if _, err := k.Topology(context.Background(), "shop"); err != nil {
		t.Fatal(err)
	}
	if cmdline := strings.Join(args, " "); !strings.Contains(cmdline, "-n shop") || strings.Contains(cmdline, "--all-namespaces") {
		t.Errorf("namespaced call = kubectl %s", cmdline)
	}
}

func TestKubeTopology_ServiceGraphIsOptional(t *testing.T) {
	k := NewKubeTopology("", "", nil)
	k.run = func(context.Context, ...string) ([]byte, error) { return []byte(kubectlWorkloads), nil }
	raw, err := k.Topology(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "service_graph") {
		t.Errorf("no metrics backend, yet a service graph: %s", raw)
	}

	// A metrics backend that fails is noted, not fatal: the workload list still helps.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer broken.Close()
	k.Metrics = NewMimir(broken.URL, "")
	raw, err = k.Topology(context.Background(), "")
	if err != nil {
		t.Fatalf("a failing service graph failed the whole call: %v", err)
	}
	if !strings.Contains(string(raw), "service graph unavailable") {
		t.Errorf("the failure is not noted: %s", raw)
	}
}

func TestKubeTopology_KubectlFailureIsAnError(t *testing.T) {
	k := NewKubeTopology("", "", nil)
	k.run = func(context.Context, ...string) ([]byte, error) { return nil, errors.New("connection refused") }
	if _, err := k.Topology(context.Background(), ""); err == nil {
		t.Error("a kubectl failure was swallowed")
	}
}
