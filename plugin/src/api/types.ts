// Mirrors engine/internal/report JSON (snake_case comes from Go).
export interface Stats {
  observed: number;
  violations: number;
  ratio: number;
}

export interface Evidence {
  kind: string;
  summary?: string;
  attrs?: Record<string, unknown>;
}

export interface Finding {
  rule_id: string;
  rule_name: string;
  source: string;
  service: string;
  impact: string;
  description: string;
  confidence: 'sampled' | 'verified';
  stats: Stats;
  evidence?: Evidence[];
  estimated_monthly_cost?: number;
}

export interface ServiceReport {
  service: string;
  spec_score: number;
  category: string;
  extension_score?: number;
  findings?: Finding[];
}

export interface Snapshot {
  fleet_score: number;
  services: ServiceReport[];
  rules_evaluated: string[];
}

export interface Report {
  generated_at: string;
  argus_version: string;
  spec_version: string;
  window: string;
  rule_set_complete: boolean;
  notes?: string[];
  finding_counts?: Record<string, number>;
  snapshot: Snapshot;
}

export interface GraphNode {
  service: string;
  // absent when the service was seen in trace edges but not scored yet
  spec_score?: number;
  findings: number;
}

export interface GraphEdge {
  source: string;
  target: string;
  traces: number;
}

export interface ServiceGraph {
  generated_at: string;
  window: string;
  nodes: GraphNode[];
  edges: GraphEdge[];
}

export interface Remediation {
  rule_id: string;
  service: string;
  template: string;
  formats: Record<string, string>;
}
