import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { css } from '@emotion/css';
import { getDefaultTimeRange, GrafanaTheme2, LoadingState, PanelData } from '@grafana/data';
import { PanelRenderer } from '@grafana/runtime';
import { Alert, LoadingPlaceholder, useStyles2 } from '@grafana/ui';
import { fetchServiceGraph } from '../../api/client';
import { ServiceGraph } from '../../api/types';
import { testIds } from '../testIds';
import { buildGraphFrames } from './graphFrames';

// Service graph answers: who calls whom, and how healthy is each hop's
// instrumentation? Nodes are scored services (arc = spec score), edges are
// resolved cross-service parent references from the completed window.
export function ServiceGraphContent() {
  const s = useStyles2(getStyles);
  const [graph, setGraph] = useState<ServiceGraph | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [wrapRef, width] = useContainerWidth();

  useEffect(() => {
    fetchServiceGraph().then(setGraph, (e) => setError(e?.message ?? String(e)));
  }, []);

  if (error) {
    return (
      <Alert title="Engine not reachable" severity="error">
        {error}
      </Alert>
    );
  }
  if (!graph) {
    return <LoadingPlaceholder text="Loading service graph…" />;
  }
  if (graph.edges.length === 0 && graph.nodes.length === 0) {
    return (
      <Alert title="No cross-service traces observed" severity="info">
        Edges appear once a completed window contains traces whose parent spans resolve across services. If the fleet
        is quiet or the window just rotated, this fills in on the next refresh.
      </Alert>
    );
  }

  const { nodes, edges } = buildGraphFrames(graph);
  const data: PanelData = {
    state: LoadingState.Done,
    series: [nodes, edges],
    timeRange: getDefaultTimeRange(),
  };

  return (
    <div ref={wrapRef} className={s.wrap} data-testid={testIds.serviceGraph.container}>
      <PanelRenderer title="Service graph" pluginId="nodeGraph" width={width} height={600} data={data} />
      <p className={s.footnote}>
        Edges come from the sampled mirror: parent references resolved across services in the last completed window
        (window {graph.window}). A missing edge can simply mean its traces were not sampled — absence here is not
        evidence of absence.
      </p>
    </div>
  );
}

// The nodeGraph panel needs explicit pixel dimensions; measure the container
// instead of pulling in a sizing dependency.
function useContainerWidth(): [React.RefObject<HTMLDivElement>, number] {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(800);
  useLayoutEffect(() => {
    const update = () => {
      if (ref.current?.clientWidth) {
        setWidth(ref.current.clientWidth);
      }
    };
    update();
    window.addEventListener('resize', update);
    return () => window.removeEventListener('resize', update);
  }, []);
  return [ref, width];
}

const getStyles = (theme: GrafanaTheme2) => ({
  wrap: css`
    width: 100%;
  `,
  footnote: css`
    color: ${theme.colors.text.secondary};
    font-size: ${theme.typography.bodySmall.fontSize};
    margin-top: ${theme.spacing(1)};
  `,
});
