// Package bench is Module C ("Prove"): the fault-injection benchmark that
// measures whether AI SRE agents can diagnose incidents from Argus telemetry.
// This package holds the shared scenario, category and diagnosis types and
// their loaders. The run loop is bench/orchestrator, the agent adapters are
// bench/agent, and the deterministic scoring is bench/scoring (LLM-free,
// depguard-enforced).
package bench
