import React from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { Button, useStyles2 } from '@grafana/ui';
import { testIds } from '../testIds';

interface Props {
  updatedAt: Date | null;
  // A failed refresh, while the previous data is still on screen.
  error: string | null;
  reload: () => void;
}

// Freshness says when the data on the page was fetched, offers a refresh, and
// says so when the last refresh failed, so stale data never passes for live.
export function Freshness({ updatedAt, error, reload }: Props) {
  const s = useStyles2(getStyles);
  return (
    <div className={s.line} data-testid={testIds.freshness}>
      {updatedAt && <span>Updated {updatedAt.toLocaleTimeString()}</span>}
      {error && <span className={s.error}>Refresh failed: {error}</span>}
      <Button size="sm" variant="secondary" fill="text" icon="sync" onClick={reload}>
        Refresh
      </Button>
    </div>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  line: css`
    display: flex;
    gap: ${theme.spacing(1)};
    align-items: center;
    color: ${theme.colors.text.secondary};
    font-size: ${theme.typography.bodySmall.fontSize};
    margin-bottom: ${theme.spacing(1)};
  `,
  error: css`
    color: ${theme.colors.warning.text};
  `,
});
