// Package cost is module A2, Spend: it prices observed usage (active series,
// ingest, object storage) from a pricing file, attributes it by service and
// signal, models lifecycle savings, prices findings, and computes trends.
// Deterministic: it never imports the LLM client (architecture rule 2).
package cost
