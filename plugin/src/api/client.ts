import { getBackendSrv } from '@grafana/runtime';
import pluginJson from '../plugin.json';
import { BacktestReport, BenchResponse, Remediation, Report, ServiceGraph, Showback } from './types';

const base = `/api/plugins/${pluginJson.id}/resources`;

// Pages show their own errors, and poll an engine that answers 503 while it
// computes, so Grafana's error toast is turned off for these requests.
export const quiet = { showErrorAlert: false };

function get<T>(path: string, params?: Record<string, string>): Promise<T> {
  return getBackendSrv().get<T>(`${base}${path}`, params, undefined, quiet);
}

export function fetchReport(): Promise<Report> {
  return get<Report>('/scores');
}

export function fetchCost(): Promise<Showback> {
  return get<Showback>('/cost');
}

export function fetchBacktest(): Promise<BacktestReport> {
  return get<BacktestReport>('/backtest');
}

// fetchBench returns the leaderboard, plus the comparison of two telemetry
// conditions when compare is given ("baseline,treatment").
export function fetchBench(compare?: string): Promise<BenchResponse> {
  return get<BenchResponse>('/bench', compare ? { compare } : undefined);
}

export function fetchServiceGraph(): Promise<ServiceGraph> {
  return get<ServiceGraph>('/servicegraph');
}

export function fetchRemediation(rule: string, service: string): Promise<Remediation> {
  return get<Remediation>('/remediation', { rule, service });
}
