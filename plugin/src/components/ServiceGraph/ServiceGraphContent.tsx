import React, { useCallback, useRef, useState } from 'react';
import { css } from '@emotion/css';
import { getDefaultTimeRange, GrafanaTheme2, LoadingState, PanelData } from '@grafana/data';
import { PanelRenderer } from '@grafana/runtime';
import { Alert, useStyles2 } from '@grafana/ui';
import { fetchServiceGraph } from '../../api/client';
import { ServiceGraph } from '../../api/types';
import { useEngineData } from '../../api/useEngineData';
import { EngineGate } from '../EngineGate/EngineGate';
import { testIds } from '../testIds';
import { buildGraphFrames } from './graphFrames';

// Service graph answers: who calls whom, and how healthy is each hop's
// instrumentation? Nodes are scored services (arc = spec score), edges are
// resolved cross-service parent references from the completed window.
export function ServiceGraphContent() {
  const state = useEngineData(fetchServiceGraph);
  return (
    <EngineGate state={state} loading="Loading service graph…">
      {(graph) => <Graph graph={graph} />}
    </EngineGate>
  );
}

function Graph({ graph }: { graph: ServiceGraph }) {
  const s = useStyles2(getStyles);
  const [wrapRef, width] = useContainerWidth();

  if ((graph.edges ?? []).length === 0 && (graph.nodes ?? []).length === 0) {
    return (
      <Alert title="No cross-service traces observed" severity="info">
        Edges appear once a completed window contains traces whose parent spans resolve across services. If the fleet is
        quiet or the window just rotated, this fills in on the next refresh.
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

// fallbackWidth is used until the container has been measured (and in test
// environments, which lay nothing out).
const fallbackWidth = 800;

// useContainerWidth tracks the width of the element the returned ref is
// attached to; the nodeGraph panel needs explicit pixels. It is a callback
// ref, so it measures when the element mounts, which here is after the data
// has loaded, and a ResizeObserver follows sidebar and layout changes, which
// emit no window resize event. The observer is disconnected when the element
// goes away.
function useContainerWidth(): [(el: HTMLDivElement | null) => void, number] {
  const [width, setWidth] = useState(fallbackWidth);
  const observer = useRef<ResizeObserver | null>(null);
  const ref = useCallback((el: HTMLDivElement | null) => {
    observer.current?.disconnect();
    observer.current = null;
    if (!el) {
      return;
    }
    if (el.clientWidth) {
      setWidth(el.clientWidth);
    }
    if (typeof ResizeObserver === 'undefined') {
      return;
    }
    observer.current = new ResizeObserver(([entry]) => {
      const w = Math.round(entry.contentRect.width);
      if (w > 0) {
        setWidth(w);
      }
    });
    observer.current.observe(el);
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
