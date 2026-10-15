// Package api is reserved for a public Go client of the engine's HTTP API, the
// layout docs/argus-master-build-plan.md §4 sets out. It is empty: the API is
// served by cmd/argus (serve.go and its serve_*.go handlers), and its JSON
// shapes are the types those handlers encode, mirrored for the plugin in
// plugin/src/api/types.ts.
package api
