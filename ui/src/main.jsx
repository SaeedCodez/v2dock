import React from "react";
import { createRoot } from "react-dom/client";
import { Navigate, RouterProvider, createHashRouter } from "react-router-dom";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Toaster } from "@/components/ui/sonner";
import { ConfirmProvider } from "@/components/confirm";
import { ErrorBoundary } from "@/components/error-boundary";
import { Shell } from "@/components/shell";
import { AuthProvider, useAuth } from "@/lib/auth";
import { LiveProvider } from "@/lib/live";
import { ThemeProvider } from "@/lib/theme";
import { Login, Setup } from "@/routes/auth-pages";
import Overview from "@/routes/overview";
import ServersPage from "@/routes/servers";
import ContainersPage from "@/routes/containers";
import ContainerPage from "@/routes/container";
import UsagePage from "@/routes/usage";
import ActivityPage from "@/routes/activity";
import SettingsPage from "@/routes/settings";
import "./index.css";

function RequireAuth({ children }) {
  const { loading, setupRequired, authenticated, unreachable } = useAuth();
  if (loading) return <div className="min-h-dvh bg-background" />;
  if (unreachable && !authenticated) {
    return (
      <div className="grid min-h-dvh place-items-center bg-background p-6 text-center">
        <div>
          <p className="display text-xl font-semibold">v2dock is starting…</p>
          <p className="mt-2 text-muted-foreground">{unreachable}</p>
        </div>
      </div>
    );
  }
  if (setupRequired) return <Navigate to="/setup" replace />;
  if (!authenticated) return <Navigate to="/login" replace />;
  return <LiveProvider>{children}</LiveProvider>;
}

const router = createHashRouter([
  { path: "/setup", element: <Setup /> },
  { path: "/login", element: <Login /> },
  {
    path: "/",
    element: (
      <RequireAuth>
        <Shell />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <Navigate to="/overview" replace /> },
      { path: "overview", element: <Overview /> },
      { path: "servers", element: <ServersPage /> },
      { path: "containers", element: <ContainersPage /> },
      { path: "container", element: <ContainerPage /> },
      { path: "usage", element: <UsagePage /> },
      { path: "activity", element: <ActivityPage /> },
      { path: "settings", element: <SettingsPage /> },
      { path: "*", element: <Navigate to="/overview" replace /> },
    ],
  },
]);

createRoot(document.getElementById("root")).render(
  <React.StrictMode>
    <ThemeProvider>
      <AuthProvider>
        <TooltipProvider delayDuration={250}>
          <ConfirmProvider>
            <ErrorBoundary>
              <RouterProvider router={router} />
            </ErrorBoundary>
            <Toaster />
          </ConfirmProvider>
        </TooltipProvider>
      </AuthProvider>
    </ThemeProvider>
  </React.StrictMode>,
);
