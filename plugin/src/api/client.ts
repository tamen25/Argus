import { getBackendSrv } from '@grafana/runtime';
import pluginJson from '../plugin.json';
import { Remediation, Report } from './types';

const base = `/api/plugins/${pluginJson.id}/resources`;

export function fetchReport(): Promise<Report> {
  return getBackendSrv().get<Report>(`${base}/scores`);
}

export function fetchRemediation(rule: string, service: string): Promise<Remediation> {
  return getBackendSrv().get<Remediation>(`${base}/remediation`, { rule, service });
}
