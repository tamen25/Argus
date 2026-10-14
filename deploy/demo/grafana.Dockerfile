# The demo's Grafana: grafana/otel-lgtm with the Argus plugin built, installed
# and provisioned, and its Collector mirroring telemetry to the engine.
# Build context: the repository root (see docker-compose.yml). Everything is
# built here, for the architecture Docker runs on, so `make demo` needs no
# Node or Go on the host.

FROM node:22-alpine AS frontend
WORKDIR /plugin
COPY plugin/package.json plugin/package-lock.json ./
RUN npm ci
COPY plugin/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
ARG TARGETOS
ARG TARGETARCH
WORKDIR /plugin
COPY plugin/go.mod plugin/go.sum ./
RUN go mod download
COPY plugin/pkg ./pkg
# Grafana runs the backend named gpx_<executable>_<os>_<arch>.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -o /out/gpx_argus_${TARGETOS}_${TARGETARCH} ./pkg

FROM grafana/otel-lgtm:0.34.0
COPY --from=frontend /plugin/dist /data/grafana/plugins/tamen25-argus-app
COPY --from=backend /out/ /data/grafana/plugins/tamen25-argus-app/
# The plugin, pointed at the demo's engine.
COPY deploy/demo/argus-plugin.yaml /otel-lgtm/grafana/conf/provisioning/plugins/argus.yaml
# The image's own Collector config, plus a mirror of every signal to the engine.
COPY deploy/demo/otelcol-config.yaml /otel-lgtm/otelcol-config.yaml
