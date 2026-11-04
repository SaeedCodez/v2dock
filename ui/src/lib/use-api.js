import * as React from "react";

/**
 * usePoll calls `fn` (an API helper returning {ok, body}) on mount, whenever
 * `deps` change, and every `interval` ms while the tab is visible. It keeps
 * the last good body while a refresh is in flight, so lists never flash empty.
 */
export function usePoll(fn, deps = [], interval = 0) {
  const [state, setState] = React.useState({ data: null, error: null, loading: true });
  const fnRef = React.useRef(fn);
  fnRef.current = fn;
  const seq = React.useRef(0);

  const reload = React.useCallback(async () => {
    const my = ++seq.current;
    const res = await fnRef.current();
    if (my !== seq.current) return res;
    if (res.ok) setState({ data: res.body, error: null, loading: false });
    else setState((s) => ({ data: s.data, error: res.body?.error || "Request failed", loading: false }));
    return res;
  }, []);

  React.useEffect(() => {
    setState((s) => ({ ...s, loading: true }));
    reload();
    if (!interval) return undefined;
    const t = setInterval(() => {
      if (document.visibilityState === "visible") reload();
    }, interval);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return { ...state, reload };
}
