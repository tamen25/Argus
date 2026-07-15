# Grafana app plugin

The Argus app plugin (`tamen25-argus-app`) puts scores and findings where
your team already lives. The plugin **backend** (Go) proxies every browser
request to the engine (`/resources/scores` → `/api/report`,
`/resources/aggregates`, `/resources/remediation`,
`/resources/servicegraph`) — the browser never needs engine access, and
engine status codes pass through unchanged so a 404 for an absent finding
stays a 404.

Engine connection: plugin settings `jsonData.engineUrl` (default
`http://argus-engine.argus.svc:8080`, matching the kind deployment). The
plugin health check pings the engine's `/healthz` through the proxy.

## Pages (Phase 1)

Built on **@grafana/scenes** with `@grafana/ui` components only — the app
inherits Grafana theming untouched.

### Overview

Answers: *how healthy is the fleet's instrumentation right now?* Fleet
Instrumentation Score (color-graded), the spec-mandated partial-rule-set
disclosure, every engine degradation note (eviction pressure, empty
windows, poller failures — never hidden), and a worst-first service table.

### Scores

Answers: *what exactly is wrong per service, how sure is Argus, and what
patch fixes it?* Service picker → finding cards with impact, evidence
samples, violation stats, and a **confidence badge** — `verified` (green,
poller-confirmed against unsampled backend data) vs `sampled` (orange,
mirror-only). Each finding's remediation panel renders the actual patch
from the engine (both Alloy River and Collector YAML) with a copy button,
plus the standing notice: generated file, review before applying.

### Service graph

Answers: *who calls whom, and how healthy is each hop's instrumentation?*
Rendered with Grafana's core node-graph panel. Nodes are services — the
arc shows the spec score (green fraction = score, red = the gap; unscored
services draw no arc), with findings count as the secondary stat. Edges
are **resolved cross-service parent references** from the engine's last
completed aggregation window (`/api/servicegraph`): the same global trace
state that feeds SPA-002/ARG-SPA-002, so the graph and the trace-health
findings can never disagree about topology.

Honesty caveat, stated on the page itself: edges come from the sampled
mirror. A missing edge can simply mean its traces were not sampled —
absence is not evidence of absence, and edge trace counts are lower
bounds.

Spend, Backtest, and Bench pages arrive with their phases (§3.3).

## Development

```bash
cd plugin
npm run test:ci   # jest (components mock the backend)
npm run e2e       # playwright smoke: Overview + Scores + Service graph against mocked resources
npm run build
```
