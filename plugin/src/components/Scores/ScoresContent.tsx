import React, { useEffect, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { Alert, Badge, Button, ClipboardButton, Combobox, LoadingPlaceholder, useStyles2 } from '@grafana/ui';
import { fetchRemediation, fetchReport } from '../../api/client';
import { Finding, Remediation, Report, ServiceReport } from '../../api/types';
import { testIds } from '../testIds';

// Scores answers: what exactly is wrong per service, how sure is Argus,
// and what patch fixes it? Confidence is always shown — sampled findings
// never masquerade as verified ones.
export function ScoresContent() {
  const s = useStyles2(getStyles);
  const [report, setReport] = useState<Report | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);

  useEffect(() => {
    fetchReport().then(setReport, (e) => setError(e?.message ?? String(e)));
  }, []);

  if (error) {
    return (
      <Alert title="Engine not reachable" severity="error">
        {error}
      </Alert>
    );
  }
  if (!report) {
    return <LoadingPlaceholder text="Loading findings…" />;
  }

  const withFindings = report.snapshot.services.filter((svc) => (svc.findings?.length ?? 0) > 0);
  if (withFindings.length === 0) {
    return <Alert title="No findings" severity="success">Every observed service passes every evaluated rule in this window.</Alert>;
  }
  const current =
    withFindings.find((svc) => svc.service === selected) ?? withFindings[0];

  return (
    <div data-testid={testIds.scores.container}>
      <div className={s.picker}>
        <Combobox
          options={withFindings.map((svc) => ({
            label: `${svc.service} — ${svc.spec_score.toFixed(1)} (${svc.findings?.length} findings)`,
            value: svc.service,
          }))}
          value={current.service}
          onChange={(v) => setSelected(v?.value ?? null)}
          width={60}
        />
      </div>
      <ServiceFindings key={current.service} svc={current} />
    </div>
  );
}

function ServiceFindings({ svc }: { svc: ServiceReport }) {
  const s = useStyles2(getStyles);
  return (
    <div>
      {(svc.findings ?? []).map((f) => (
        <FindingCard key={f.rule_id} finding={f} />
      ))}
      <p className={s.footnote}>
        Extension-rule findings never affect the spec score; confidence marks whether the backend poller verified the
        sampled observation.
      </p>
    </div>
  );
}

function FindingCard({ finding }: { finding: Finding }) {
  const s = useStyles2(getStyles);
  const [rem, setRem] = useState<Remediation | null>(null);
  const [remErr, setRemErr] = useState<string | null>(null);
  const [open, setOpen] = useState(false);

  const confidenceColor = finding.confidence === 'verified' ? 'green' : 'orange';

  return (
    <div className={s.card} data-testid={testIds.scores.finding}>
      <div className={s.cardHeader}>
        <strong>{finding.rule_name}</strong>
        <code>{finding.rule_id}</code>
        <Badge text={finding.confidence} color={confidenceColor} />
        <Badge text={finding.impact} color="blue" />
        <span className={s.source}>{finding.source}</span>
      </div>
      <p>{finding.description}</p>
      <p className={s.stats}>
        observed {finding.stats.observed} · violations {finding.stats.violations} ·{' '}
        {(finding.stats.ratio * 100).toFixed(0)}%
      </p>
      {(finding.evidence ?? []).map((e, i) => (
        <pre key={i} className={s.evidence}>
          {e.summary ?? JSON.stringify(e.attrs)}
        </pre>
      ))}

      {!open ? (
        <Button
          size="sm"
          variant="secondary"
          onClick={() => {
            setOpen(true);
            fetchRemediation(finding.rule_id, finding.service).then(setRem, (e) =>
              setRemErr(e?.data?.message ?? e?.message ?? String(e))
            );
          }}
        >
          View remediation
        </Button>
      ) : remErr ? (
        <Alert title="No remediation available" severity="info">
          {remErr}
        </Alert>
      ) : !rem ? (
        <LoadingPlaceholder text="Rendering patch…" />
      ) : (
        <RemediationPanel rem={rem} />
      )}
    </div>
  );
}

function RemediationPanel({ rem }: { rem: Remediation }) {
  const s = useStyles2(getStyles);
  const formats = Object.keys(rem.formats).sort();
  return (
    <div data-testid={testIds.scores.remediation}>
      {formats.map((format) => (
        <div key={format} className={s.patch}>
          <div className={s.patchHeader}>
            <code>{format}</code>
            <ClipboardButton size="sm" variant="secondary" getText={() => rem.formats[format]}>
              Copy
            </ClipboardButton>
          </div>
          <pre className={s.patchBody}>{rem.formats[format]}</pre>
        </div>
      ))}
      <p className={s.footnote}>
        Generated file — review before applying. Argus never modifies your systems.
      </p>
    </div>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  picker: css`
    margin-bottom: ${theme.spacing(2)};
  `,
  card: css`
    border: 1px solid ${theme.colors.border.weak};
    border-radius: ${theme.shape.radius.default};
    padding: ${theme.spacing(2)};
    margin-bottom: ${theme.spacing(2)};
    background: ${theme.colors.background.secondary};
  `,
  cardHeader: css`
    display: flex;
    gap: ${theme.spacing(1)};
    align-items: center;
    margin-bottom: ${theme.spacing(1)};
  `,
  source: css`
    color: ${theme.colors.text.secondary};
    font-size: ${theme.typography.bodySmall.fontSize};
  `,
  stats: css`
    color: ${theme.colors.text.secondary};
  `,
  evidence: css`
    font-size: ${theme.typography.bodySmall.fontSize};
    padding: ${theme.spacing(1)};
    background: ${theme.colors.background.primary};
  `,
  patch: css`
    margin-top: ${theme.spacing(1)};
  `,
  patchHeader: css`
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: ${theme.spacing(0.5)};
  `,
  patchBody: css`
    max-height: 320px;
    overflow: auto;
    font-size: ${theme.typography.bodySmall.fontSize};
  `,
  footnote: css`
    color: ${theme.colors.text.secondary};
    font-size: ${theme.typography.bodySmall.fontSize};
    margin-top: ${theme.spacing(1)};
  `,
});
