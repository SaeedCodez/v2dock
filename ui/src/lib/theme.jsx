import * as React from "react";

const ThemeContext = React.createContext({
  theme: "system",
  resolved: "dark",
  setTheme: () => {},
});

const STORAGE_KEY = "theme";

function systemPrefersDark() {
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

/**
 * ThemeProvider keeps the `dark` class on <html> in sync with the stored
 * preference. The same logic runs inline in index.html before first paint, so
 * the page never flashes the wrong theme on load.
 */
export function ThemeProvider({ children }) {
  const [theme, setThemeState] = React.useState(() => {
    try {
      // Dark is the default look (see the pre-paint script in index.html, which
      // must agree with this); "system" and "light" are explicit choices.
      return localStorage.getItem(STORAGE_KEY) || "dark";
    } catch {
      return "dark";
    }
  });
  const [systemDark, setSystemDark] = React.useState(systemPrefersDark);

  React.useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = (e) => setSystemDark(e.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);

  const resolved = theme === "system" ? (systemDark ? "dark" : "light") : theme;

  React.useEffect(() => {
    document.documentElement.classList.toggle("dark", resolved === "dark");
    document.documentElement.style.colorScheme = resolved;
  }, [resolved]);

  const setTheme = React.useCallback((next) => {
    setThemeState(next);
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch {
      /* private mode: the theme just won't persist */
    }
  }, []);

  const value = React.useMemo(
    () => ({ theme, resolved, setTheme }),
    [theme, resolved, setTheme],
  );
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme() {
  return React.useContext(ThemeContext);
}
