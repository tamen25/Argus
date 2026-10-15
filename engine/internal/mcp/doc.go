// Package mcp is the read-only tool surface Argus exposes to bench agents (and
// usable standalone as an MCP server). Every backend port here is a query with
// no mutating method, so the surface cannot change user infrastructure
// (architecture rule 5). ports.go declares those backend ports, catalog.go and
// server.go build the tools over them, and transport.go serves them as JSON-RPC
// 2.0 on stdio (`argus mcp`); the concrete backends live in mcp/backend.
package mcp
