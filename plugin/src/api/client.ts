import { getBackendSrv } from '@grafana/runtime';
import pluginJson from '../plugin.json';
import { Remediation, Report, ServiceGraph } from './types';

const base = `/api/plugins/${pluginJson.id}/resources`;

export function fetchReport(): Promise<Report> {
  return getBackendSrv().get<Report>(`${base}/scores`);
}

export function fetchServiceGraph(): Promise<ServiceGraph> {
  return getBackendSrv().get<ServiceGraph>(`${base}/servicegraph`);
}

export function fetchRemediation(rule: string, service: string): Promise<Remediation> {
  return getBackendSrv().get<Remediation>(`${base}/remediation`, { rule, service });
}
