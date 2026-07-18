import { getBackendSrv } from '@grafana/runtime';
import pluginJson from '../plugin.json';
import { BacktestReport, Remediation, Report, ServiceGraph, Showback } from './types';

const base = `/api/plugins/${pluginJson.id}/resources`;

export function fetchReport(): Promise<Report> {
  return getBackendSrv().get<Report>(`${base}/scores`);
}

export function fetchCost(): Promise<Showback> {
  return getBackendSrv().get<Showback>(`${base}/cost`);
}

export function fetchBacktest(): Promise<BacktestReport> {
  return getBackendSrv().get<BacktestReport>(`${base}/backtest`);
}

export function fetchServiceGraph(): Promise<ServiceGraph> {
  return getBackendSrv().get<ServiceGraph>(`${base}/servicegraph`);
}

export function fetchRemediation(rule: string, service: string): Promise<Remediation> {
  return getBackendSrv().get<Remediation>(`${base}/remediation`, { rule, service });
}
