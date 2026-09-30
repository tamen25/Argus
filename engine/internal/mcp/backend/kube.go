package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// workloadKinds are what a root cause is named as. Pods are left out: they
// churn, and a diagnosis names the workload that owns them.
const workloadKinds = "deployments,statefulsets,daemonsets"

// serviceGraphQuery reads the trace-derived service graph (Tempo's
// metrics-generator) over the recent window.
const serviceGraphQuery = `sum by (client, server) (rate(traces_service_graph_request_total[15m])) > 0`

// KubeTopology is the TopologyBackend adapter. Workloads come from the
// Kubernetes API through `kubectl get` — a read, never a write — and, when a
// metrics backend is given, the service graph comes from trace metrics.
//
// It lists identity only (kind, namespace, name), never status: the topology
// tells an agent what the entities are called, not which one is unhealthy.
// That still has to come from the telemetry.
type KubeTopology struct {
	// Context selects a kube context; empty uses the current one.
	Context string
	// Exclude is a label selector for objects to leave out. The bench harness
	// sets it to hide its own fault objects: they are the apparatus, not part
	// of the system under diagnosis, and their names would give the answer away.
	Exclude string
	// Metrics, when set, supplies the service graph.
	Metrics *Mimir
	// Timeout caps the kubectl call (default 30s).
	Timeout time.Duration

	// run executes kubectl; tests replace it.
	run func(ctx context.Context, args ...string) ([]byte, error)
}

// NewKubeTopology builds a topology adapter.
func NewKubeTopology(kubeContext, exclude string, metrics *Mimir) *KubeTopology {
	return &KubeTopology{Context: kubeContext, Exclude: exclude, Metrics: metrics}
}

// Workload is one entry in the topology answer.
type Workload struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// Edge is one caller → callee pair from the service graph.
type Edge struct {
	Client string `json:"client"`
	Server string `json:"server"`
}

type topologyAnswer struct {
	Workloads    []Workload `json:"workloads"`
	ServiceGraph []Edge     `json:"service_graph,omitempty"`
	Notes        []string   `json:"notes,omitempty"`
}

// Topology lists workloads in namespace (all namespaces when empty) and the
// service graph.
func (k *KubeTopology) Topology(ctx context.Context, namespace string) (json.RawMessage, error) {
	args := []string{"get", workloadKinds, "-o", "json"}
	if namespace == "" {
		args = append(args, "--all-namespaces")
	} else {
		args = append(args, "-n", namespace)
	}
	if k.Exclude != "" {
		args = append(args, "-l", k.Exclude)
	}
	if k.Context != "" {
		args = append([]string{"--context", k.Context}, args...)
	}
	out, err := k.kubectl(ctx, args...)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("decoding kubectl output: %w", err)
	}
	ans := topologyAnswer{Workloads: make([]Workload, 0, len(list.Items))}
	for _, it := range list.Items {
		ans.Workloads = append(ans.Workloads, Workload{Kind: it.Kind, Namespace: it.Metadata.Namespace, Name: it.Metadata.Name})
	}
	sort.Slice(ans.Workloads, func(i, j int) bool {
		a, b := ans.Workloads[i], ans.Workloads[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Kind < b.Kind
	})

	if k.Metrics != nil {
		edges, err := k.serviceGraph(ctx)
		switch {
		case err != nil:
			ans.Notes = append(ans.Notes, "service graph unavailable: "+err.Error())
		case len(edges) == 0:
			ans.Notes = append(ans.Notes, "no service-graph metrics in the last 15 minutes")
		default:
			ans.ServiceGraph = edges
			ans.Notes = append(ans.Notes,
				"service_graph names services as traces report them (service.name), not workloads")
		}
	}
	return json.Marshal(ans)
}

func (k *KubeTopology) serviceGraph(ctx context.Context) ([]Edge, error) {
	raw, err := k.Metrics.QueryInstant(ctx, serviceGraphQuery, time.Now())
	if err != nil {
		return nil, err
	}
	var doc struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decoding service graph: %w", err)
	}
	edges := make([]Edge, 0, len(doc.Data.Result))
	for _, r := range doc.Data.Result {
		if r.Metric["client"] == "" || r.Metric["server"] == "" {
			continue
		}
		edges = append(edges, Edge{Client: r.Metric["client"], Server: r.Metric["server"]})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Client != edges[j].Client {
			return edges[i].Client < edges[j].Client
		}
		return edges[i].Server < edges[j].Server
	})
	return edges, nil
}

func (k *KubeTopology) kubectl(ctx context.Context, args ...string) ([]byte, error) {
	if k.run != nil {
		return k.run(ctx, args...)
	}
	timeout := k.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, truncate(stderr.Bytes(), 512))
	}
	return stdout.Bytes(), nil
}
