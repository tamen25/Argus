import React, { useEffect, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { Alert, Combobox, LoadingPlaceholder, useStyles2 } from '@grafana/ui';
import { fetchBench } from '../../api/client';
import { BenchAgentComparison, BenchBoard, BenchCell, BenchComparison, BenchResponse } from '../../api/types';
import { testIds } from '../testIds';

// The flagship experiment's two telemetry conditions. When both are present
// the page opens on their comparison.
const DEFAULT_BASELINE = 'degraded';
const DEFAULT_TREATMENT = 'remediated';

// Bench answers: can an AI agent diagnose an incident from this telemetry, and
// does fixing the telemetry change that? The leaderboard is agents ×
// scenarios per telemetry condition; the comparison is the change between two
// conditions. Every number comes from `argus bench run` reports by plain
// arithmetic, and the caveats ride on every view (architecture rule 7).
export function BenchContent() {
  const s = useStyles2(getStyles);
  const [data, setData] = useState<BenchResponse | null>(null);
  const [comparison, setComparison] = useState<BenchComparison | null>(null);
  const [baseline, setBaseline] = useState<string | null>(null);
  const [treatment, setTreatment] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notConfigured, setNotConfigured] = useState(false);

  useEffect(() => {
    fetchBench().then(
      (d) => {
        setData(d);
        const conditions = d.conditions ?? [];
        if (conditions.includes(DEFAULT_BASELINE) && conditions.includes(DEFAULT_TREATMENT)) {
          setBaseline(DEFAULT_BASELINE);
          setTreatment(DEFAULT_TREATMENT);
        }
      },
      (e) => {
        if (e?.status === 404) {
          setNotConfigured(true);
        } else {
          setError(e?.data?.message ?? e?.message ?? String(e));
        }
      }
    );
  }, []);

  useEffect(() => {
    if (!baseline || !treatment || baseline === treatment) {
      return;
    }
    fetchBench(`${baseline},${treatment}`).then(
      (d) => setComparison(d.comparison ?? null),
      (e) => setError(e?.data?.message ?? e?.message ?? String(e))
    );
  }, [baseline, treatment]);

  if (notConfigured) {
    return (
      <Alert title="Bench is not configured" severity="info">
        Start the engine with <code>--bench-reports &lt;dir&gt;</code>, a directory of reports written by{' '}
        <code>argus bench run --format json --out &lt;dir&gt;/&lt;name&gt;.json</code>, to see the leaderboard here. See
        the Bench docs.
      </Alert>
    );
  }
  if (error) {
    return (
      <Alert title="Could not load the bench results" severity="error">
        {error}
      </Alert>
    );
  }
  if (!data) {
    return <LoadingPlaceholder text="Reading bench runs…" />;
  }
  if (data.reports === 0 || !data.leaderboard) {
    return (
      <Alert title="No bench runs yet" severity="info">
        Run a scenario with <code>argus bench run --format json --out &lt;dir&gt;/&lt;name&gt;.json</code> into the
        engine&apos;s <code>--bench-reports</code> directory; it shows here on the next load.
      </Alert>
    );
  }

  const conditions = data.conditions ?? [];
  const options = conditions.map((c) => ({ label: conditionLabel(c), value: c }));
  // Only the comparison for the pair currently selected is shown: a response
  // for an earlier selection must not linger under the new one.
  const shown =
    comparison && comparison.baseline === baseline && comparison.treatment === treatment && baseline !== treatment
      ? comparison
      : null;
  const caveats = (shown?.caveats ?? data.leaderboard.caveats) || [];

  return (
    <div data-testid={testIds.bench.container}>
      <div className={s.total}>
        {data.reports} run report{data.reports === 1 ? '' : 's'}
        <span className={s.totalSub}>
          {' '}
          · {conditions.length} telemetry condition{conditions.length === 1 ? '' : 's'}
        </span>
      </div>

      {conditions.length >= 2 && (
        <div className={s.picker}>
          <span>Compare</span>
          <Combobox
            width={24}
            options={options}
            value={baseline}
            placeholder="baseline"
            onChange={(o) => setBaseline(o?.value ?? null)}
          />
          <span>with</span>
          <Combobox
            width={24}
            options={options}
            value={treatment}
            placeholder="treatment"
            onChange={(o) => setTreatment(o?.value ?? null)}
          />
        </div>
      )}

      {shown && <ComparisonView c={shown} />}

      {(data.leaderboard.boards ?? []).map((b) => (
        <BoardView key={b.condition} board={b} />
      ))}

      {/* How to read the numbers. Never hidden. */}
      <Alert title="How to read these numbers" severity="warning" className={s.note}>
        <ul className={s.caveatList}>
          {caveats.map((c, i) => (
            <li key={i}>{c}</li>
          ))}
        </ul>
      </Alert>
    </div>
  );
}

function ComparisonView({ c }: { c: BenchComparison }) {
  const s = useStyles2(getStyles);
  return (
    <div className={s.card}>
      <h3 className={s.h}>
        {conditionLabel(c.baseline)} → {conditionLabel(c.treatment)}
      </h3>
      <table className={s.table}>
        <thead>
          <tr>
            <th>Agent</th>
            <th className={s.num}>Scenarios compared</th>
            <th className={s.num}>{conditionLabel(c.baseline)}</th>
            <th className={s.num}>{conditionLabel(c.treatment)}</th>
            <th className={s.num}>Δ</th>
            <th className={s.num}>Runs answered ({conditionLabel(c.baseline)})</th>
            <th className={s.num}>Runs answered ({conditionLabel(c.treatment)})</th>
          </tr>
        </thead>
        <tbody>
          {(c.agents ?? []).map((a) => (
            <tr key={a.agent}>
              <td>
                <code>{a.agent}</code>
              </td>
              <td className={s.num}>
                {a.paired} of {(a.scenarios ?? []).length}
              </td>
              <td className={s.num}>{a.paired > 0 ? a.baseline_mean.toFixed(2) : '—'}</td>
              <td className={s.num}>{a.paired > 0 ? a.treatment_mean.toFixed(2) : '—'}</td>
              <td className={`${s.num} ${deltaClass(s, a)}`}>{a.paired > 0 ? signed(a.delta) : '—'}</td>
              <td className={s.num}>{answered(a.baseline_diagnoses, a.baseline_attempts)}</td>
              <td className={s.num}>{answered(a.treatment_diagnoses, a.treatment_attempts)}</td>
            </tr>
          ))}
        </tbody>
      </table>

      {(c.agents ?? []).map((a) => (
        <div key={a.agent} className={s.agent}>
          <h4 className={s.h}>
            <code>{a.agent}</code>
          </h4>
          <table className={s.table}>
            <thead>
              <tr>
                <th>Scenario</th>
                <th className={s.num}>{conditionLabel(c.baseline)}</th>
                <th className={s.num}>{conditionLabel(c.treatment)}</th>
                <th className={s.num}>Δ</th>
              </tr>
            </thead>
            <tbody>
              {(a.scenarios ?? []).map((sc) => (
                <tr key={sc.scenario}>
                  <td>
                    <code>{sc.scenario}</code>
                  </td>
                  <td className={s.num}>{cellText(sc.baseline)}</td>
                  <td className={s.num}>{cellText(sc.treatment)}</td>
                  <td className={s.num}>{sc.delta === undefined || sc.delta === null ? '—' : signed(sc.delta)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {(a.excluded ?? []).map((e, i) => (
            <div key={i} className={s.muted}>
              excluded: {e}
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}

function BoardView({ board }: { board: BenchBoard }) {
  const s = useStyles2(getStyles);
  return (
    <div className={s.card}>
      <h3 className={s.h}>Leaderboard · {conditionLabel(board.condition)}</h3>
      <table className={s.table}>
        <thead>
          <tr>
            <th className={s.num}>#</th>
            <th>Agent</th>
            <th className={s.num}>Mean score</th>
            <th className={s.num}>Scenarios answered</th>
            <th className={s.num}>Runs answered</th>
            {(board.scenarios ?? []).map((sc) => (
              <th key={sc} className={s.num}>
                <code>{sc}</code>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {(board.rows ?? []).map((r, i) => (
            <tr key={r.agent}>
              <td className={s.num}>{i + 1}</td>
              <td>
                <code>{r.agent}</code>
              </td>
              <td className={s.num}>
                <strong>{r.scenarios_answered > 0 ? r.mean_score.toFixed(2) : '—'}</strong>
              </td>
              <td className={s.num}>
                {r.scenarios_answered}/{r.scenarios_run}
              </td>
              <td className={s.num}>{answered(r.diagnoses, r.attempts)}</td>
              {(r.cells ?? []).map((cell) => (
                <td key={cell.scenario} className={s.num}>
                  {cellText(cell)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      <p className={s.footnote}>
        A cell is mean score ± spread (answered runs/attempts). “no diagnosis” means every attempt failed or ran out of
        budget; “·” means not run.
      </p>
    </div>
  );
}

function conditionLabel(c: string): string {
  return c === '' ? '(unlabeled)' : c;
}

function cellText(c?: BenchCell): string {
  if (!c || c.attempts === 0) {
    return '·';
  }
  if (c.diagnoses === 0) {
    return `no diagnosis (0/${c.attempts})`;
  }
  return `${c.mean_score.toFixed(2)} ± ${c.stddev_score.toFixed(2)} (${c.diagnoses}/${c.attempts})`;
}

function answered(diagnoses: number, attempts: number): string {
  if (attempts === 0) {
    return '·';
  }
  return `${diagnoses}/${attempts} (${Math.round((diagnoses / attempts) * 100)}%)`;
}

function signed(x: number): string {
  return `${x >= 0 ? '+' : ''}${x.toFixed(2)}`;
}

function deltaClass(s: ReturnType<typeof getStyles>, a: BenchAgentComparison): string {
  if (a.paired === 0 || a.delta === 0) {
    return '';
  }
  return a.delta > 0 ? s.good : s.bad;
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
  picker: css`
    display: flex;
    align-items: center;
    gap: ${theme.spacing(1)};
    margin-bottom: ${theme.spacing(2)};
  `,
  note: css`
    margin-top: ${theme.spacing(2)};
  `,
  caveatList: css`
    margin: 0 0 0 ${theme.spacing(2)};
  `,
  card: css`
    margin-bottom: ${theme.spacing(3)};
    overflow-x: auto;
  `,
  agent: css`
    margin-top: ${theme.spacing(2)};
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
      white-space: nowrap;
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
