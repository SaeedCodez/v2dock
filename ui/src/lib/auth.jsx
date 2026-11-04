import * as React from "react";
import { Auth } from "@/lib/api";

const AuthContext = React.createContext(null);

export function AuthProvider({ children }) {
  const [state, setState] = React.useState({ loading: true, authenticated: false, setupRequired: false, totpEnabled: false });

  const refresh = React.useCallback(async () => {
    const res = await Auth.status();
    if (res.ok) {
      setState({ loading: false, ...res.body });
    } else {
      setState((s) => ({ ...s, loading: false, unreachable: res.body?.error || "v2dock is not reachable" }));
    }
    return res;
  }, []);

  React.useEffect(() => {
    refresh();
    const onUnauthorized = () => setState((s) => ({ ...s, authenticated: false }));
    window.addEventListener("v2dock:unauthorized", onUnauthorized);
    return () => window.removeEventListener("v2dock:unauthorized", onUnauthorized);
  }, [refresh]);

  const logout = React.useCallback(async () => {
    await Auth.logout();
    setState((s) => ({ ...s, authenticated: false }));
  }, []);

  const value = React.useMemo(() => ({ ...state, refresh, logout }), [state, refresh, logout]);
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  return React.useContext(AuthContext);
}
