import { useCallback, useEffect, useState } from 'react';

// What a page knows about one engine endpoint.
export interface EngineData<T> {
  data: T | null;
  // The last request's error. When data is set too, it is the previous
  // answer, still shown, and the error is about the refresh.
  error: string | null;
  // The endpoint answered 404: the engine runs without this feature.
  notConfigured: boolean;
  // The engine answered 503: the report is still being computed (a backtest
  // replay can take minutes). The hook asks again shortly.
  computing: boolean;
  updatedAt: Date | null;
  reload: () => void;
}

// How often a page asks for fresh data, and how soon it asks again while the
// engine is still computing.
export const refreshMs = 60_000;
const computingRetryMs = 10_000;

// useEngineData loads one engine endpoint, and keeps it current: it reloads
// every refreshMs, asks again sooner while the engine answers 503, and
// ignores an answer that arrives after a newer request was made or after the
// page has gone. fetcher must keep its identity across renders (a
// module-level function): a new one starts a new request.
export function useEngineData<T>(fetcher: () => Promise<T>, everyMs = refreshMs): EngineData<T> {
  const [state, setState] = useState<Omit<EngineData<T>, 'reload'>>({
    data: null,
    error: null,
    notConfigured: false,
    computing: false,
    updatedAt: null,
  });
  const [generation, setGeneration] = useState(0);
  const reload = useCallback(() => setGeneration((g) => g + 1), []);

  useEffect(() => {
    let current = true; // false once this request is superseded or the page unmounts
    let timer: ReturnType<typeof setTimeout> | undefined;
    const again = (ms: number) => {
      timer = setTimeout(reload, ms);
    };
    fetcher().then(
      (data) => {
        if (!current) {
          return;
        }
        setState({ data, error: null, notConfigured: false, computing: false, updatedAt: new Date() });
        again(everyMs);
      },
      (e) => {
        if (!current) {
          return;
        }
        if (e?.status === 404) {
          setState((s) => ({ ...s, notConfigured: true, computing: false }));
          return; // not configured stays not configured until the engine restarts
        }
        if (e?.status === 503) {
          setState((s) => ({ ...s, computing: true, error: null }));
          again(computingRetryMs);
          return;
        }
        setState((s) => ({ ...s, computing: false, error: errorText(e) }));
        again(everyMs);
      }
    );
    return () => {
      current = false;
      clearTimeout(timer);
    };
  }, [fetcher, generation, everyMs, reload]);

  return { ...state, reload };
}

// errorText is the message to show for a failed request: the engine's own
// text when it sent one.
export function errorText(e: unknown): string {
  const err = e as { data?: { message?: string }; message?: string } | undefined;
  return err?.data?.message ?? err?.message ?? String(e);
}
