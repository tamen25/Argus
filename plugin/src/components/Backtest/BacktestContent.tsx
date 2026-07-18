import React, { useEffect, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { Alert, LoadingPlaceholder, useStyles2 } from '@grafana/ui';
import { fetchBacktest } from '../../api/client';
import { BacktestReport } from '../../api/types';
import { testIds } from '../testIds';

// Backtest answers: would these alert rules have fired on real history, how
// fast, how noisily — scored against the incident registry. Replay is not
// re-execution, so the fidelity caveats ride on every view and are never
// hidden (architecture rule 7).
export function BacktestContent() {
  const s = useStyles2(getStyles);
  const [data, setData] = useState<BacktestReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notConfigured, setNotConfigured] = useState(false);

  useEffect(() => {
    fetchBacktest().then(setData, (e) => {
      if (e?.status === 404) {
        setNotConfigured(true);
      } else {
        setError(e?.message ?? String(e));
      }
    });
  }, []);

  if (notConfigured) {
    return (
      <Alert title="Backtest is not configured" severity="info">
        Start the engine with <code>--backtest-rules</code>, <code>--backtest-mimir-url</code>, and an{' '}
        <code>--backtest-incidents</code> registry to replay your alert rules against history here. See the Backtest
        docs.
      </Alert>
    );
  }
  if (error) {
    return (
      <Alert title="Engine not reachable" severity="error">
        {error}
      </Alert>
    );
  }
  if (!data) {
    return <LoadingPlaceholder text="Replaying history…" />;
  }

  const rules = data.rules ?? [];
  const caveats = data.caveats ?? [];
  const coveragePct = data.window_seconds > 0 ? Math.round((data.coverage_seconds / data.window_seconds) * 100) : 0;

  return (
    <div data-testid={testIds.backtest.container}>
      <div className={s.total}>
        {dur(data.coverage_seconds)} covered
        <span className={s.totalSub}>
          {' '}
          of {dur(data.window_seconds)} ({coveragePct}%) · {data.segments} segment{data.segments === 1 ? '' : 's'} ·
          step {dur(data.step_seconds)}
        </span>
      </div>

      {/* Replay is not re-execution — the fidelity caveats are never hidden. */}
      <Alert title="Replay fidelity" severity="warning" className={s.note}>
        Replay steps instant queries through history; staleness, lookback, and ruler alignment differ from live
        evaluation. Verdicts apply to the covered segments only.
        {caveats.length > 0 && (
          <ul className={s.caveatList}>
            {caveats.map((c, i) => (
              <li key={i}>{c}</li>
            ))}
          </ul>
        )}
      </Alert>

      {rules.length === 0 && <p>No alerting rules were replayed.</p>}

      {rules.map((sc, i) => (
        <div key={i} className={s.card}>
          <h3 className={s.h}>{sc.rule}</h3>
          <table className={s.table}>
            <thead>
              <tr>
                <th className={s.num}>Detected</th>
                <th className={s.num}>Missed</th>
                <th className={s.num}>Unverifiable</th>
                <th className={s.num}>False positives</th>
                <th className={s.num}>Pages/week*</th>
                <th className={s.num}>Flappiness</th>
              </tr>
            </thead>
            <tbody>
              <tr>
                <td className={`${s.num} ${sc.detections.length > 0 ? s.good : ''}`}>{sc.detections.length}</td>
                <td className={`${s.num} ${sc.missed.length > 0 ? s.bad : ''}`}>{sc.missed.length}</td>
                <td className={s.num}>{sc.unverifiable.length}</td>
                <td className={s.num}>{sc.false_positives.length}</td>
                <td className={s.num}>{sc.pages_per_week.toFixed(1)}</td>
                <td className={s.num}>{sc.flappiness.toFixed(1)}</td>
              </tr>
            </tbody>
          </table>

          {sc.detections.length > 0 && (
            <table className={s.table}>
              <thead>
                <tr>
                  <th>Detected incident</th>
                  <th className={s.num}>Time to detection</th>
                </tr>
              </thead>
              <tbody>
                {sc.detections.map((d, j) => (
                  <tr key={j}>
                    <td>{d.incident_id}</td>
                    <td className={s.num}>{dur(d.ttd_seconds)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}

          {sc.missed.map((id, j) => (
            <div key={j} className={s.bad}>
              missed: {id}
            </div>
          ))}
          {sc.unverifiable.map((id, j) => (
            <div key={j} className={s.muted}>
              unverifiable (no telemetry coverage): {id}
            </div>
          ))}
        </div>
      ))}

      <p className={s.footnote}>*Pages/week extrapolates firing intervals over covered time only, not calendar time.</p>
    </div>
  );
}

// dur renders whole seconds as a compact human duration (2100 → "35m").
function dur(seconds: number): string {
  if (seconds <= 0) {
    return '0s';
  }
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const sec = Math.round(seconds % 60);
  const parts: string[] = [];
  if (h > 0) {
    parts.push(`${h}h`);
  }
  if (m > 0) {
    parts.push(`${m}m`);
  }
  if (sec > 0 && h === 0) {
    parts.push(`${sec}s`);
  }
  return parts.join('') || '0s';
}

const getStyles = (theme: GrafanaTheme2) => ({
  total: css`
    font-size: ${theme.typography.h2.fontSize};
    font-weight: ${theme.typography.fontWeightBold};
    margin-bottom: ${theme.spacing(1)};
  `,
  totalSub: css`
    font-size: ${theme.typography.body.fontSize};
    font-weight: ${theme.typography.fontWeightRegular};
    color: ${theme.colors.text.secondary};
  `,
  note: css`
    margin-bottom: ${theme.spacing(2)};
  `,
  caveatList: css`
    margin: ${theme.spacing(1)} 0 0 ${theme.spacing(2)};
  `,
  card: css`
    margin-bottom: ${theme.spacing(3)};
  `,
  h: css`
    margin-top: ${theme.spacing(2)};
    margin-bottom: ${theme.spacing(1)};
  `,
  table: css`
    width: 100%;
    border-collapse: collapse;
    margin-bottom: ${theme.spacing(1)};
    & th,
    & td {
      text-align: left;
      padding: ${theme.spacing(0.5, 1)};
      border-bottom: 1px solid ${theme.colors.border.weak};
    }
  `,
  num: css`
    text-align: right;
  `,
  good: css`
    color: ${theme.colors.success.text};
    font-weight: ${theme.typography.fontWeightBold};
  `,
  bad: css`
    color: ${theme.colors.error.text};
    font-weight: ${theme.typography.fontWeightBold};
  `,
  muted: css`
    color: ${theme.colors.text.secondary};
  `,
  footnote: css`
    color: ${theme.colors.text.secondary};
    font-size: ${theme.typography.bodySmall.fontSize};
    margin-top: ${theme.spacing(1)};
  `,
});
