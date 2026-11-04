import * as React from "react";
import { Link, NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { Activity, BarChart3, Boxes, LayoutGrid, LogOut, Menu, Server, Settings, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { BrandMark, ThemeSwitch } from "@/components/brand";
import { Dot } from "@/components/page";
import { useAuth } from "@/lib/auth";
import { connectionState, useLive } from "@/lib/live";
import { cn } from "@/lib/utils";

const NAV = [
  { to: "/overview", label: "Overview", icon: LayoutGrid },
  { to: "/servers", label: "Servers", icon: Server },
  { to: "/containers", label: "Containers", icon: Boxes },
  { to: "/usage", label: "Usage", icon: BarChart3 },
  { to: "/activity", label: "Activity", icon: Activity },
];

function NavItem({ to, label, icon: Icon, onClick, block }) {
  return (
    <NavLink
      to={to}
      onClick={onClick}
      className={({ isActive }) =>
        cn(
          "flex items-center gap-2 rounded-[10px] font-medium transition-colors",
          block ? "h-12 px-4 text-md" : "h-10 px-3.5 text-[0.95rem]",
          isActive
            ? "bg-card text-foreground shadow-[0_1px_2px_rgb(0_0_0/0.06)] ring-1 ring-border"
            : "text-muted-foreground hover:bg-accent/70 hover:text-foreground",
        )
      }
    >
      <Icon className="size-[17px]" />
      {label}
    </NavLink>
  );
}

/** ConnectionPill is the always-visible answer to "is the VPN working?" */
function ConnectionPill() {
  const { status } = useLive();
  const st = connectionState(status);
  const map = {
    loading: { tone: "neutral", text: "…" },
    off: { tone: "neutral", text: "Disconnected" },
    checking: { tone: "running", text: "Checking…", pulse: true },
    ok: { tone: "success", text: status?.health?.delayMs ? `${status.health.delayMs} ms` : "Connected" },
    degraded: { tone: "warning", text: "Unstable" },
    down: { tone: "destructive", text: "Server down" },
    error: { tone: "destructive", text: "Error" },
  }[st];
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Link
          to="/overview"
          className="flex h-10 max-w-[260px] items-center gap-2.5 rounded-full border border-border bg-card px-3.5 text-sm font-medium transition-colors hover:bg-accent"
        >
          <Dot tone={map.tone} pulse={map.pulse} />
          <span className="truncate">
            {status?.connected && status?.server ? <span className="hidden text-muted-foreground lg:inline">{status.server.name} · </span> : null}
            <span className="tabular">{map.text}</span>
          </span>
        </Link>
      </TooltipTrigger>
      <TooltipContent>
        {status?.connected ? `VPN: ${status.server?.name || "server"}` : "VPN is off; containers use their normal network"}
      </TooltipContent>
    </Tooltip>
  );
}

export function Shell() {
  const { logout } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [open, setOpen] = React.useState(false);
  React.useEffect(() => setOpen(false), [location.pathname]);

  const doLogout = async () => {
    await logout();
    navigate("/login", { replace: true });
  };

  return (
    <div className="min-h-dvh bg-background">
      <header className="sticky top-0 z-40 border-b border-border bg-background/85 backdrop-blur-md">
        <div className="mx-auto flex h-[72px] w-full max-w-[1240px] items-center gap-4 px-5 sm:px-8">
          <Link to="/overview" className="flex shrink-0 items-center gap-3 rounded-xl transition-opacity hover:opacity-85">
            <BrandMark />
            <span className="display hidden text-lg font-semibold sm:inline">v2dock</span>
          </Link>

          <nav aria-label="Main" className="ml-2 hidden items-center gap-1 rounded-[14px] border border-border bg-secondary/60 p-1 md:flex">
            {NAV.map((n) => (
              <NavItem key={n.to} {...n} />
            ))}
          </nav>

          <div className="ml-auto flex items-center gap-2">
            <ConnectionPill />
            <ThemeSwitch className="hidden lg:flex" />
            <Tooltip>
              <TooltipTrigger asChild>
                <Button asChild variant="ghost" size="icon" aria-label="Settings" className="hidden md:inline-flex">
                  <NavLink to="/settings" className={({ isActive }) => cn(isActive && "bg-accent text-foreground")}>
                    <Settings />
                  </NavLink>
                </Button>
              </TooltipTrigger>
              <TooltipContent>Settings</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon" aria-label="Log out" onClick={doLogout} className="hidden md:inline-flex">
                  <LogOut />
                </Button>
              </TooltipTrigger>
              <TooltipContent>Log out</TooltipContent>
            </Tooltip>
            <Button variant="ghost" size="icon" className="md:hidden" aria-label="Menu" onClick={() => setOpen((o) => !o)}>
              {open ? <X /> : <Menu />}
            </Button>
          </div>
        </div>
        {open ? (
          <div className="border-t border-border px-5 pb-5 pt-3 md:hidden">
            <nav className="grid gap-1">
              {[...NAV, { to: "/settings", label: "Settings", icon: Settings }].map((n) => (
                <NavItem key={n.to} {...n} block />
              ))}
            </nav>
            <div className="mt-4 flex items-center gap-2">
              <ThemeSwitch className="flex-1" />
              <Button variant="outline" onClick={doLogout}>
                <LogOut /> Log out
              </Button>
            </div>
          </div>
        ) : null}
      </header>
      <main>
        <Outlet />
      </main>
    </div>
  );
}
