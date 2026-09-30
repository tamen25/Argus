package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/mcp"
	"github.com/tamen25/Argus/engine/internal/mcp/backend"
)

func newMCPCmd() *cobra.Command {
	var (
		mimirURL, lokiURL, tempoURL string
		tenant                      string
		kubeTopology                bool
		kubeContext                 string
	)
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve the read-only observability tool surface over MCP (stdio)",
		Long: `Runs an MCP server on stdin/stdout exposing read-only tools over your LGTM
stack: query_prometheus, query_loki, search_traces and list_alerts, the discovery
tools list_metrics, list_metric_labels, list_log_labels and list_trace_tags, and
(with --kube-topology) get_k8s_topology. This is
the same tool surface the bench harness gives every agent, so a benchmark
compares agents rather than tool access.

Only tools whose backend URL is provided are exposed — an MCP client sees a
smaller, honest surface rather than tools that fail at call time. Every call is
a GET: the server holds no write credentials and cannot change your systems.

Point an MCP-capable client at:  argus mcp --mimir-url URL [--loki-url URL] [--tempo-url URL]`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var b mcp.Backends
			if mimirURL != "" {
				m := backend.NewMimir(mimirURL, tenant)
				b.Metrics = m
				b.Alerts = m
				b.MetricsCatalog = m
			}
			if lokiURL != "" {
				l := backend.NewLoki(lokiURL, tenant)
				b.Logs = l
				b.LogsCatalog = l
			}
			if tempoURL != "" {
				t := backend.NewTempo(tempoURL, tenant)
				b.Traces = t
				b.TracesCatalog = t
			}
			if kubeTopology {
				var m *backend.Mimir
				if mimirURL != "" {
					m = backend.NewMimir(mimirURL, tenant)
				}
				b.Topology = backend.NewKubeTopology(kubeContext, "", m)
			}
			reg, err := mcp.NewServer(b)
			if err != nil {
				return fmt.Errorf("%w (provide at least --mimir-url)", err)
			}
			return mcp.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), reg,
				mcp.ServerInfo{Name: "argus", Version: version})
		},
	}
	cmd.Flags().StringVar(&mimirURL, "mimir-url", "", "Mimir base URL (enables query_prometheus + list_alerts)")
	cmd.Flags().StringVar(&lokiURL, "loki-url", "", "Loki base URL (enables query_loki)")
	cmd.Flags().StringVar(&tempoURL, "tempo-url", "", "Tempo base URL (enables search_traces)")
	cmd.Flags().StringVar(&tenant, "tenant", "", "X-Scope-OrgID tenant header (empty = anonymous)")
	cmd.Flags().BoolVar(&kubeTopology, "kube-topology", false,
		"enable get_k8s_topology: workload identities via read-only `kubectl get`, plus the service graph when --mimir-url is set")
	cmd.Flags().StringVar(&kubeContext, "kube-context", "", "kube context for get_k8s_topology (empty = current)")
	return cmd
}
