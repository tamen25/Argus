# Grafana app plugin

The Argus app plugin (`tamen25-argus-app`) puts scores and findings where
your team already lives. The plugin **backend** (Go) proxies every browser
request to the engine (`/resources/scores` → `/api/report`,
`/resources/aggregates`, `/resources/remediation`) — the browser never
needs engine access, and engine status codes pass through unchanged so a
404 for an absent finding stays a 404.

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

Spend, Backtest, and Bench pages arrive with their phases (§3.3).

## Development

```bash
cd plugin
npm run test:ci   # jest (components mock the backend)
npm run e2e       # playwright smoke: Overview + Scores against mocked resources
npm run build
```
