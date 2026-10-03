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

// Mirrors engine/internal/cost Showback JSON.
export interface CostLine {
  service: string;
  team?: string;
  signal: string;
  ingest_monthly: number;
  active_series_monthly: number;
  total_monthly: number;
}

export interface StorageLine {
  class: string;
  gb: number;
  monthly: number;
}

export interface CostReport {
  currency: string;
  lines: CostLine[];
  storage: StorageLine[];
  total_monthly: number;
}

export interface LifecycleRec {
  from_class: string;
  to_class: string;
  gb: number;
  current_monthly: number;
  projected_monthly: number;
  savings_monthly: number;
}

export interface TrendLine {
  service: string;
  team?: string;
  signal: string;
  current: number;
  previous: number;
  delta: number;
  percent_delta: number;
}

export interface CostTrend {
  currency: string;
  lines: TrendLine[];
  total_delta: number;
}

export interface Showback {
  generated_at: string;
  window: string;
  report: CostReport;
  lifecycle?: LifecycleRec[];
  trend?: CostTrend;
  notes?: string[];
}

// Mirrors engine/internal/backtest Report.MarshalJSON (durations are seconds).
export interface BacktestDetection {
  incident_id: string;
  ttd_seconds: number;
}

export interface BacktestFiring {
  series: string;
  fired_at: string;
  resolved_at?: string;
  unresolved_at_end: boolean;
}

export interface BacktestScorecard {
  rule: string;
  detections: BacktestDetection[];
  missed: string[];
  unverifiable: string[];
  false_positives: BacktestFiring[];
  coverage_seconds: number;
  pages_per_week: number;
  flappiness: number;
}

export interface BacktestReport {
  generated_at: string;
  from: string;
  to: string;
  step_seconds: number;
  coverage_seconds: number;
  window_seconds: number;
  segments: number;
  rules: BacktestScorecard[];
  caveats: string[];
}

// Mirrors engine/internal/bench/leaderboard (served by /api/bench).
export interface BenchCell {
  scenario: string;
  attempts: number;
  diagnoses: number;
  budget_exhausted: number;
  answer_rate: number;
  mean_score: number;
  stddev_score: number;
}

export interface BenchRow {
  agent: string;
  cells: BenchCell[];
  scenarios_run: number;
  scenarios_answered: number;
  mean_score: number;
  attempts: number;
  diagnoses: number;
  answer_rate: number;
}

export interface BenchBoard {
  condition: string;
  scenarios: string[];
  rows: BenchRow[];
}

export interface BenchLeaderboard {
  boards: BenchBoard[];
  caveats: string[];
}

export interface BenchScenarioComparison {
  scenario: string;
  baseline?: BenchCell;
  treatment?: BenchCell;
  delta?: number;
}

export interface BenchAgentComparison {
  agent: string;
  scenarios: BenchScenarioComparison[];
  paired: number;
  baseline_mean: number;
  treatment_mean: number;
  delta: number;
  baseline_attempts: number;
  baseline_diagnoses: number;
  baseline_answer_rate: number;
  treatment_attempts: number;
  treatment_diagnoses: number;
  treatment_answer_rate: number;
  excluded?: string[];
}

export interface BenchComparison {
  baseline: string;
  treatment: string;
  agents: BenchAgentComparison[];
  caveats: string[];
}

export interface BenchResponse {
  reports: number;
  conditions: string[];
  leaderboard?: BenchLeaderboard;
  comparison?: BenchComparison;
}
