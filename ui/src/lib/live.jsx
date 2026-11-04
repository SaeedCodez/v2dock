import * as React from "react";
import { api } from "@/lib/api";

const LiveContext = React.createContext({ status: null, reload: async () => {} });

/**
 * LiveProvider polls /api/status for the whole app: the header pill, the
 * overview and the server test progress all read the same snapshot.
 */
export function LiveProvider({ children }) {
  const [status, setStatus] = React.useState(null);
  const reload = React.useCallback(async () => {
    const res = await api.get("/api/status");
    if (res.ok) setStatus(res.body);
    return res;
  }, []);

  React.useEffect(() => {
    reload();
    const t = setInterval(() => {
      if (document.visibilityState === "visible") reload();
    }, 4000);
    return () => clearInterval(t);
  }, [reload]);

  const value = React.useMemo(() => ({ status, reload }), [status, reload]);
  return <LiveContext.Provider value={value}>{children}</LiveContext.Provider>;
}

export function useLive() {
  return React.useContext(LiveContext);
}

/** connectionState folds status into one of: off, ok, degraded, down, error. */
export function connectionState(s) {
  if (!s) return "loading";
  if (!s.connected) return "off";
  if (s.error) return "error";
  if (!s.health?.at) return "checking";
  if (s.health.ok) return "ok";
  return s.health.failures >= 2 ? "down" : "degraded";
}
