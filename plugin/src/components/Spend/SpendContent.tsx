import React, { useEffect, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { Alert, LoadingPlaceholder, useStyles2 } from '@grafana/ui';
import { fetchCost } from '../../api/client';
import { Showback } from '../../api/types';
import { testIds } from '../testIds';

// Spend answers: what does this stack cost, attributed by service and signal,
// and where are the easy savings? Costs are modeled from the user's pricing,
// never billed — the note says so and is never hidden.
export function SpendContent() {
  const s = useStyles2(getStyles);
  const [data, setData] = useState<Showback | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notConfigured, setNotConfigured] = useState(false);

  useEffect(() => {
    fetchCost().then(setData, (e) => {
      if (e?.status === 404) {
        setNotConfigured(true);
      } else {
        setError(e?.message ?? String(e));
      }
    });
  }, []);

  if (notConfigured) {
    return (
      <Alert title="Cost reporting is not configured" severity="info">
        Start the engine with a <code>--cost-pricing</code> file and at least one backend (Mimir, Loki, or S3) to see
        showback here. See the Cost &amp; showback docs.
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
    return <LoadingPlaceholder text="Loading spend…" />;
  }

  const cur = data.report.currency;
  // The engine marshals empty Go slices as JSON null (e.g. storage with no S3
  // configured), so every list is coerced to an array before use.
  const lines = data.report.lines ?? [];
  const storage = data.report.storage ?? [];
  return (
    <div data-testid={testIds.spend.container}>
      <div className={s.total}>
        {money(data.report.total_monthly)} {cur}
        <span className={s.totalSub}> / month · window {data.window}</span>
      </div>
      {(data.notes ?? []).map((n, i) => (
        <Alert key={i} title="" severity="info" className={s.note}>
          {n}
        </Alert>
      ))}

      <h3 className={s.h}>By service and signal</h3>
      <table className={s.table}>
        <thead>
          <tr>
            <th>Service</th><th>Signal</th><th>Team</th>
            <th className={s.num}>Ingest</th><th className={s.num}>Active series</th><th className={s.num}>Total /mo</th>
          </tr>
        </thead>
        <tbody>
          {lines.map((l, i) => (
            <tr key={i}>
              <td>{l.service}</td><td>{l.signal}</td><td>{l.team ?? '—'}</td>
              <td className={s.num}>{money(l.ingest_monthly)}</td>
              <td className={s.num}>{money(l.active_series_monthly)}</td>
              <td className={s.num}>{money(l.total_monthly)}</td>
            </tr>
          ))}
        </tbody>
      </table>

      {storage.length > 0 && (
        <>
          <h3 className={s.h}>Storage by class</h3>
          <table className={s.table}>
            <thead>
              <tr><th>Class</th><th className={s.num}>GB</th><th className={s.num}>/mo</th></tr>
            </thead>
            <tbody>
              {storage.map((st, i) => (
                <tr key={i}><td>{st.class}</td><td className={s.num}>{st.gb.toFixed(1)}</td><td className={s.num}>{money(st.monthly)}</td></tr>
              ))}
            </tbody>
          </table>
        </>
      )}

      {(data.lifecycle?.length ?? 0) > 0 && (
        <>
          <h3 className={s.h}>Lifecycle savings</h3>
          <table className={s.table}>
            <thead>
              <tr>
                <th>Move</th><th className={s.num}>GB</th><th className={s.num}>Now /mo</th>
                <th className={s.num}>After /mo</th><th className={s.num}>Save /mo</th>
              </tr>
            </thead>
            <tbody>
              {data.lifecycle!.map((r, i) => (
                <tr key={i}>
                  <td>{r.from_class} → {r.to_class}</td>
                  <td className={s.num}>{r.gb.toFixed(1)}</td>
                  <td className={s.num}>{money(r.current_monthly)}</td>
                  <td className={s.num}>{money(r.projected_monthly)}</td>
                  <td className={`${s.num} ${s.save}`}>{money(r.savings_monthly)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className={s.footnote}>Savings assume the data is cold enough for the target class — that judgement is yours.</p>
        </>
      )}

      {data.trend && data.trend.lines.length > 0 && (
        <>
          <h3 className={s.h}>Week-over-week</h3>
          <p>Total change: <strong>{signed(data.trend.total_delta)} {cur}/mo</strong></p>
          <table className={s.table}>
            <thead>
              <tr><th>Service</th><th>Signal</th><th className={s.num}>Was</th><th className={s.num}>Now</th><th className={s.num}>Δ</th></tr>
            </thead>
            <tbody>
              {data.trend.lines.map((l, i) => (
                <tr key={i}>
                  <td>{l.service}</td><td>{l.signal}</td>
                  <td className={s.num}>{money(l.previous)}</td><td className={s.num}>{money(l.current)}</td>
                  <td className={s.num}>{signed(l.delta)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </div>
  );
}

const money = (v: number) => v.toFixed(2);
const signed = (v: number) => (v >= 0 ? '+' : '') + v.toFixed(2);

const getStyles = (theme: GrafanaTheme2) => ({
  total: css`
    font-size: ${theme.typography.h1.fontSize};
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
  h: css`
    margin-top: ${theme.spacing(3)};
    margin-bottom: ${theme.spacing(1)};
  `,
  table: css`
    width: 100%;
    border-collapse: collapse;
    & th, & td {
      text-align: left;
      padding: ${theme.spacing(0.5, 1)};
      border-bottom: 1px solid ${theme.colors.border.weak};
    }
  `,
  num: css`
    text-align: right;
  `,
  save: css`
    color: ${theme.colors.success.text};
    font-weight: ${theme.typography.fontWeightBold};
  `,
  footnote: css`
    color: ${theme.colors.text.secondary};
    font-size: ${theme.typography.bodySmall.fontSize};
    margin-top: ${theme.spacing(1)};
  `,
});
