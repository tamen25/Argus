// Package mcp is the read-only tool surface Argus exposes to bench agents (and
// usable standalone as an MCP server). Every backend port here is a query with
// no mutating method, so the surface cannot change user infrastructure
// (architecture rule 5). This file set is the transport-agnostic core —
// registry, ports, and the five tools; the JSON-RPC/stdio MCP transport and the
// `argus mcp` command land in a later Phase 4 slice.
package mcp
