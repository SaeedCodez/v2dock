import * as React from "react";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { Boxes, ChartNoAxesColumn, Gauge, Loader2, MoreHorizontal, RotateCcw, Search, ShieldAlert, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Empty, Mono, Page, PageHeader, Segmented } from "@/components/page";
import { QuotaMeter } from "@/components/charts";
import { useConfirm } from "@/components/confirm";
import { Containers, errorOf } from "@/lib/api";
import { PERIOD_LABEL, bytes, fromBytes, rate, toBytes } from "@/lib/format";
import { useLive } from "@/lib/live";
import { usePoll } from "@/lib/use-api";
import { cn } from "@/lib/utils";

export function QuotaDialog({ open, onOpenChange, container, onSaved }) {
  const route = container?.route;
  const [limited, setLimited] = React.useState(false);
  const [value, setValue] = React.useState("");
  const [unit, setUnit] = React.useState("GB");
  const [period, setPeriod] = React.useState("monthly");
  const [action, setAction] = React.useState("block");
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (!open) return;
    const fb = fromBytes(route?.quotaBytes || 0);
    setLimited(!!route?.quotaBytes);
    setValue(fb.value || "10");
    setUnit(fb.unit);
    setPeriod(route?.quotaPeriod || "monthly");
    setAction(route?.quotaAction || "block");
  }, [open, route]);

  const save = async (e) => {
    e.preventDefault();
    setBusy(true);
    const res = await Containers.route(container.key, {
      quotaBytes: limited ? toBytes(value, unit) : 0,
      quotaPeriod: period,
      quotaAction: action,
    });
    setBusy(false);
    if (!res.ok) {
      toast.error(errorOf(res));
      return;
    }
    toast.success(limited ? "Quota saved" : "Quota removed");
    onOpenChange(false);
    onSaved();
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[500px]">
        <form onSubmit={save} className="space-y-5">
          <DialogHeader>
            <DialogTitle>VPN quota · {container?.label}</DialogTitle>
            <DialogDescription>Limit how much of this container's traffic may go through the VPN. Direct traffic is never limited.</DialogDescription>
          </DialogHeader>
          <Segmented
            value={limited ? "limit" : "none"}
            onChange={(v) => setLimited(v === "limit")}
            options={[
              { value: "none", label: "No limit" },
              { value: "limit", label: "Set a limit" },
            ]}
          />
          {limited ? (
            <>
              <div className="grid grid-cols-[1fr_110px_1fr] items-end gap-3">
                <div className="space-y-2">
                  <Label htmlFor="q-val">Limit</Label>
                  <Input id="q-val" inputMode="decimal" value={value} onChange={(e) => setValue(e.target.value.replace(/[^\d.]/g, ""))} />
                </div>
                <Select value={unit} onValueChange={setUnit}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="MB">MB</SelectItem>
                    <SelectItem value="GB">GB</SelectItem>
                    <SelectItem value="TB">TB</SelectItem>
                  </SelectContent>
                </Select>
                <Select value={period} onValueChange={setPeriod}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="daily">per day</SelectItem>
                    <SelectItem value="weekly">per week</SelectItem>
                    <SelectItem value="monthly">per month</SelectItem>
                    <SelectItem value="total">in total</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-2">
                <Label>When the limit is reached</Label>
                <div className="grid gap-2">
                  {[
                    { v: "block", t: "Block international traffic", d: "Iranian sites keep working; nothing else gets out. Safest for the VPN budget." },
                    { v: "direct", t: "Use the normal network", d: "The container stops using the VPN until the next period." },
                  ].map((o) => (
                    <button
                      type="button"
                      key={o.v}
                      onClick={() => setAction(o.v)}
                      className={cn(
                        "rounded-xl border p-3.5 text-left transition-colors",
                        action === o.v ? "border-primary bg-primary/6 ring-1 ring-primary" : "border-border hover:bg-accent",
                      )}
                    >
                      <div className="font-medium">{o.t}</div>
                      <div className="mt-0.5 text-sm text-muted-foreground">{o.d}</div>
                    </button>
                  ))}
                </div>
              </div>
            </>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || (limited && !(Number(value) > 0))}>
              {busy ? <Loader2 className="animate-spin" /> : null} Save
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function stateBadge(c) {
  if (c.state === "running") return <Badge variant="secondary">running</Badge>;
  return <Badge variant="outline">{c.state}</Badge>;
}

export function ContainerRow({ c, onToggle, onQuota, onReset, onRemove, busy }) {
  const r = c.route;
  const vpnToday = c.today?.vpn || 0;
  const directToday = c.today?.direct || 0;
  const toggle = (
    <Switch checked={c.routed} onCheckedChange={(v) => onToggle(c, v)} disabled={busy || (!c.routable && !c.routed)} aria-label={`Route ${c.label} through the VPN`} />
  );
  return (
    <div className={cn("grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-5 gap-y-3 px-5 py-4 lg:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1fr)_auto]", c.state !== "running" && "opacity-70")}>
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <Link to={`/container?key=${encodeURIComponent(c.key)}`} className="truncate font-medium hover:underline">
            {c.label}
          </Link>
          {stateBadge(c)}
          {r?.exceeded ? (
            <Badge variant="destructive">
              <ShieldAlert className="size-3" /> over quota
            </Badge>
          ) : null}
        </div>
        <div className="mt-0.5 flex flex-wrap gap-x-3 text-[0.8125rem] text-muted-foreground">
          {c.project ? <span>{c.project}</span> : null}
          <Mono className="truncate">{c.name}</Mono>
          {c.ips?.length ? <Mono>{c.ips.join(", ")}</Mono> : null}
        </div>
      </div>

      <div className="hidden text-sm lg:block">
        <div className="flex items-center gap-2">
          <span className="size-2 rounded-[2px]" style={{ background: "var(--series-vpn)" }} />
          <span className="tabular">{bytes(vpnToday)}</span>
          <span className="text-muted-foreground">VPN today</span>
        </div>
        <div className="mt-1 flex items-center gap-2">
          <span className="size-2 rounded-[2px]" style={{ background: "var(--series-direct)" }} />
          <span className="tabular">{bytes(directToday)}</span>
          <span className="text-muted-foreground">direct</span>
        </div>
      </div>

      <div className="hidden min-w-0 text-sm lg:block">
        {c.routed && r?.quotaBytes ? (
          <div className="max-w-[220px]">
            <div className="mb-1.5 flex justify-between gap-2 text-xs text-muted-foreground">
              <span>
                {bytes(r.quotaUsed)} of {bytes(r.quotaBytes)}
              </span>
              <span>{PERIOD_LABEL[r.quotaPeriod]}</span>
            </div>
            <QuotaMeter used={r.quotaUsed} limit={r.quotaBytes} exceeded={r.exceeded} />
          </div>
        ) : c.live ? (
          <div className="text-muted-foreground">
            <span className="tabular">{c.live.cpu.toFixed(1)}%</span> CPU · <span className="tabular">{bytes(c.live.mem)}</span>
            <div className="text-xs">
              ↓ {rate(c.live.rxRate)} ↑ {rate(c.live.txRate)}
            </div>
          </div>
        ) : null}
      </div>

      <div className="flex items-center justify-end gap-2">
        {c.routable || c.routed ? (
          <label className="flex cursor-pointer items-center gap-2.5 text-sm font-medium">
            <span className={cn("hidden sm:inline", c.routed ? "text-foreground" : "text-muted-foreground")}>VPN</span>
            {toggle}
          </label>
        ) : (
          <Tooltip>
            <TooltipTrigger asChild>
              <span className="flex items-center gap-2.5 text-sm text-muted-foreground">
                <span className="hidden sm:inline">VPN</span>
                {toggle}
              </span>
            </TooltipTrigger>
            <TooltipContent>Not possible: this container {c.reason}</TooltipContent>
          </Tooltip>
        )}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon-sm" aria-label="More">
              <MoreHorizontal />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem asChild>
              <Link to={`/container?key=${encodeURIComponent(c.key)}`}>
                <ChartNoAxesColumn /> Usage & charts
              </Link>
            </DropdownMenuItem>
            {c.routed ? (
              <>
                <DropdownMenuItem onSelect={() => onQuota(c)}>
                  <Gauge /> VPN quota…
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => onReset(c)}>
                  <RotateCcw /> Reset quota usage
                </DropdownMenuItem>
              </>
            ) : null}
            {onRemove ? (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => onRemove(c)} className="text-destructive focus:text-destructive">
                  <Trash2 /> Forget
                </DropdownMenuItem>
              </>
            ) : null}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </div>
  );
}

export function useContainerActions(reload) {
  const { status, reload: reloadStatus } = useLive();
  const confirm = useConfirm();
  const [busy, setBusy] = React.useState("");
  const [quotaFor, setQuotaFor] = React.useState(null);

  const toggle = async (c, on) => {
    setBusy(c.key);
    const res = on ? await Containers.route(c.key, {}) : await Containers.unroute(c.key);
    setBusy("");
    if (!res.ok) toast.error(errorOf(res));
    else if (on) {
      toast.success(`${c.label} now uses the VPN${status?.connected ? "" : " as soon as a server is connected"}`);
    } else toast.success(`${c.label} uses its normal network again`);
    reload();
    reloadStatus();
  };
  const reset = async (c) => {
    const ok = await confirm({ title: `Reset ${c.label}'s quota usage?`, description: "Usage starts from zero again; if it was blocked, it uses the VPN again.", confirmLabel: "Reset" });
    if (!ok) return;
    const res = await Containers.reset(c.key);
    if (!res.ok) toast.error(errorOf(res));
    reload();
    reloadStatus();
  };
  const forget = async (c) => {
    const res = await Containers.unroute(c.key);
    if (!res.ok) toast.error(errorOf(res));
    reload();
  };
  return { busy, toggle, reset, forget, quotaFor, setQuotaFor };
}

export default function ContainersPage() {
  const { data, loading, reload } = usePoll(() => Containers.list(), [], 10000);
  const [q, setQ] = React.useState("");
  const [show, setShow] = React.useState("running");
  const acts = useContainerActions(reload);
  const { status } = useLive();

  const list = (data?.containers || []).filter((c) => {
    // v2dock's own containers can never be routed; listing them is noise.
    if (c.reason === "v2dock itself") return false;
    if (show === "running" && c.state !== "running" && !c.routed) return false;
    if (show === "vpn" && !c.routed) return false;
    if (!q) return true;
    const lq = q.toLowerCase();
    return c.label.toLowerCase().includes(lq) || c.name.toLowerCase().includes(lq) || (c.project || "").toLowerCase().includes(lq);
  });
  const missing = data?.missingRoutes || [];

  return (
    <Page>
      <PageHeader
        title="Containers"
        description={
          <>
            Turn on <span className="font-medium text-foreground">VPN</span> for the containers that must keep reaching the outside world.
            {status && !status.connected ? " No server is connected yet: selections take effect once one is." : ""}
          </>
        }
      />
      {data?.dockerError ? (
        <Card className="mb-5 border-destructive/30 p-4 text-sm text-destructive">Docker is not reachable: {data.dockerError}</Card>
      ) : null}
      <Card className="overflow-hidden">
        <div className="flex flex-col gap-3 border-b border-border p-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="relative w-full sm:max-w-xs">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Filter containers" className="pl-9" />
          </div>
          <Segmented
            size="sm"
            value={show}
            onChange={setShow}
            options={[
              { value: "running", label: "Running" },
              { value: "vpn", label: "On VPN" },
              { value: "all", label: "All" },
            ]}
          />
        </div>
        <div className="divide-y divide-border">
          {list.map((c) => (
            <ContainerRow key={c.id} c={c} busy={acts.busy === c.key} onToggle={acts.toggle} onQuota={acts.setQuotaFor} onReset={acts.reset} />
          ))}
          {!list.length && !loading ? (
            <Empty icon={Boxes} title={q ? "No container matches" : show === "vpn" ? "No container uses the VPN yet" : "No containers"} className="py-12">
              {show === "vpn" ? "Switch on VPN for a container in the Running list." : null}
            </Empty>
          ) : null}
        </div>
      </Card>

      {missing.length ? (
        <div className="mt-8">
          <h2 className="display mb-1 text-lg font-semibold">Selected but not found</h2>
          <p className="mb-4 text-sm text-muted-foreground">These were set to use the VPN, but no container with that identity exists right now (removed, or being redeployed). They resume automatically when it is back.</p>
          <Card className="divide-y divide-border">
            {missing.map((r) => (
              <div key={r.key} className="flex items-center justify-between gap-4 px-5 py-3.5">
                <div className="min-w-0">
                  <div className="font-medium">{r.label}</div>
                  <Mono className="text-muted-foreground">{r.key}</Mono>
                </div>
                <Button variant="ghost" size="sm" onClick={() => acts.forget(r)}>
                  <Trash2 /> Forget
                </Button>
              </div>
            ))}
          </Card>
        </div>
      ) : null}

      <QuotaDialog open={!!acts.quotaFor} container={acts.quotaFor} onOpenChange={(o) => !o && acts.setQuotaFor(null)} onSaved={reload} />
    </Page>
  );
}
