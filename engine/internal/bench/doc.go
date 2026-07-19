// Package bench is Module C ("Prove"): the fault-injection benchmark that
// measures whether AI SRE agents can diagnose incidents from Argus telemetry.
// This file holds the shared scenario/diagnosis value types and loaders; the
// deterministic scoring lives in the bench/scoring subpackage (LLM-free,
// depguard-enforced), and orchestration/adapters land in later Phase 4 slices.
package bench
