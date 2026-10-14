import React from 'react';
import { Alert, LoadingPlaceholder } from '@grafana/ui';
import { EngineData } from '../../api/useEngineData';
import { Freshness } from '../Freshness/Freshness';

interface Props<T> {
  state: EngineData<T>;
  // What the placeholder says while the first answer is on its way.
  loading: string;
  // Shown when the engine runs without this page's feature (HTTP 404).
  notConfigured?: React.ReactNode;
  children: (data: T) => React.ReactNode;
}

// EngineGate renders the states every page shares (not configured, not
// reachable, still computing, loading) and the page itself once there is
// data, under a line saying how fresh it is.
export function EngineGate<T>({ state, loading, notConfigured, children }: Props<T>) {
  if (state.notConfigured && notConfigured) {
    return <>{notConfigured}</>;
  }
  if (state.data === null) {
    if (state.error) {
      return (
        <Alert title="Engine not reachable" severity="error">
          {state.error}
        </Alert>
      );
    }
    if (state.computing) {
      return <LoadingPlaceholder text={`${loading} The engine is still computing it; checking again shortly.`} />;
    }
    return <LoadingPlaceholder text={loading} />;
  }
  // One element, not a fragment: Grafana lays a page's content out in a flex
  // row, where two siblings would sit side by side.
  return (
    <div>
      <Freshness updatedAt={state.updatedAt} error={state.error} reload={state.reload} />
      {children(state.data)}
    </div>
  );
}
