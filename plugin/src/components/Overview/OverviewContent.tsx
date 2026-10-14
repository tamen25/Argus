import React from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { Alert, LinkButton, useStyles2, useTheme2 } from '@grafana/ui';
import { fetchReport } from '../../api/client';
import { Report } from '../../api/types';
import { useEngineData } from '../../api/useEngineData';
import { EngineGate } from '../EngineGate/EngineGate';
import { prefixRoute } from '../../utils/utils.routing';
import { ROUTES } from '../../constants';
import { testIds } from '../testIds';

// Overview answers one question: how healthy is the fleet's
// instrumentation right now? Numbers first, adjectives never (§8).
export function OverviewContent() {
  const state = useEngineData(fetchReport);
  return (
    <EngineGate state={state} loading="Loading fleet score…">
      {(report) => <Overview report={report} />}
    </EngineGate>
  );
}

function Overview({ report }: { report: Report }) {
  const s = useStyles2(getStyles);
  const theme = useTheme2();
  const snap = report.snapshot;
  // An engine before v1.1 sent null for an empty fleet.
  const services = snap.services ?? [];
  if (services.length === 0) {
    return (
      <Alert title="No services observed yet" severity="info">
        The engine has not received telemetry from any service, so there is no score yet. A fleet score of{' '}
        {snap.fleet_score.toFixed(0)} here would describe an empty fleet, not healthy instrumentation.
      </Alert>
    );
  }
  const worstFirst = [...services].sort((a, b) => a.spec_score - b.spec_score);

  return (
    <div data-testid={testIds.overview.container}>
      <div className={s.scoreRow}>
        <div className={s.scoreValue} style={{ color: scoreColor(snap.fleet_score, theme) }}>
          {snap.fleet_score.toFixed(1)}
        </div>
        <div className={s.scoreLabel}>
          Fleet Instrumentation Score · {services.length} services · window {report.window}
        </div>
      </div>

      {!report.rule_set_complete && (
        <Alert title="Partial rule set" severity="info">
          This implementation does not yet implement the full rule set of the Instrumentation Score specification (rules
          evaluated: {(snap.rules_evaluated ?? []).join(', ')} · spec {report.spec_version}). Scores may differ from a
          complete implementation.
        </Alert>
      )}
      {(report.notes ?? []).map((n) => (
        <Alert key={n} title="Engine note" severity="warning">
          {n}
        </Alert>
      ))}

      <h3 className={s.tableTitle}>Which services need attention first?</h3>
      <table className={s.table} data-testid={testIds.overview.servicesTable}>
        <thead>
          <tr>
            <th>Service</th>
            <th className={s.num}>Score</th>
            <th>Category</th>
            <th className={s.num}>Extension</th>
            <th className={s.num}>Findings</th>
          </tr>
        </thead>
        <tbody>
          {worstFirst.map((svc) => (
            <tr key={svc.service}>
              <td>{svc.service}</td>
              <td className={s.num}>{svc.spec_score.toFixed(1)}</td>
              <td>{svc.category}</td>
              <td className={s.num}>{svc.extension_score != null ? svc.extension_score.toFixed(1) : '—'}</td>
              <td className={s.num}>{svc.findings?.length ?? 0}</td>
            </tr>
          ))}
        </tbody>
      </table>

      <LinkButton href={prefixRoute(ROUTES.Scores)} className={s.drill}>
        Drill into findings
      </LinkButton>
    </div>
  );
}

function scoreColor(score: number, theme: GrafanaTheme2): string {
  if (score >= 90) {
    return theme.colors.success.text;
  }
  if (score >= 75) {
    return theme.colors.warning.text;
  }
  return theme.colors.error.text;
}

const getStyles = (theme: GrafanaTheme2) => ({
  scoreRow: css`
    margin-bottom: ${theme.spacing(2)};
  `,
  scoreValue: css`
    font-size: 64px;
    font-weight: ${theme.typography.fontWeightBold};
    line-height: 1.1;
  `,
  scoreLabel: css`
    color: ${theme.colors.text.secondary};
  `,
  tableTitle: css`
    margin-top: ${theme.spacing(3)};
  `,
  table: css`
    width: 100%;
    border-collapse: collapse;
    th,
    td {
      text-align: left;
      padding: ${theme.spacing(1)};
      border-bottom: 1px solid ${theme.colors.border.weak};
    }
  `,
  num: css`
    text-align: right !important;
  `,
  drill: css`
    margin-top: ${theme.spacing(2)};
  `,
});
