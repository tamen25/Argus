# Argus — development targets (CLAUDE.md contract: created in Phase 0, kept working forever).
# Run from repo root on Linux/WSL2. Requires: go, node/npm, docker, kind, kubectl, helm.

SHELL := /bin/bash
ENGINE_DIR := engine
PLUGIN_DIR := plugin

.PHONY: dev-up dev-down test test-integration lint build demo demo-down help

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'

dev-up: ## kind cluster: LGTM + otel-demo + chaos-mesh (+ argus from Phase 1)
	bash deploy/kind/bootstrap.sh

dev-down: ## Delete the kind dev cluster
	bash deploy/kind/teardown.sh

test: ## Unit tests, all modules
	cd $(ENGINE_DIR) && go test ./...
	@if [ -f $(PLUGIN_DIR)/package.json ]; then cd $(PLUGIN_DIR) && npm run test:ci --if-present; fi

test-integration: ## Integration tests (testcontainers; needs docker)
	cd $(ENGINE_DIR) && go test -tags integration -count=1 ./...

lint: ## golangci-lint (incl. depguard) + eslint
	cd $(ENGINE_DIR) && golangci-lint run ./...
	@if [ -f $(PLUGIN_DIR)/package.json ]; then cd $(PLUGIN_DIR) && npm run lint --if-present; fi

build: ## Engine binary + plugin dist
	cd $(ENGINE_DIR) && CGO_ENABLED=0 go build -trimpath -o bin/argus ./cmd/argus
	@if [ -f $(PLUGIN_DIR)/package.json ]; then cd $(PLUGIN_DIR) && npm run build --if-present; fi

demo: ## docker compose demo: engine + postgres + grafana + mini-LGTM
	docker compose -f deploy/demo/docker-compose.yml up --build -d
	@echo "Grafana: http://localhost:3000 (anonymous admin) — engine health: http://localhost:8080/healthz"

demo-down: ## Stop the demo compose stack
	docker compose -f deploy/demo/docker-compose.yml down -v
