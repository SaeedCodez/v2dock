import * as React from "react";
import { useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import {
  CircleAlert,
  Link2,
  Loader2,
  MoreHorizontal,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Server,
  Timer,
  Trash2,
  X,
  Zap,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Empty, Mono, Page, PageHeader, Segmented } from "@/components/page";
import { QuotaMeter } from "@/components/charts";
import { useConfirm } from "@/components/confirm";
import { Servers, Subs, errorOf } from "@/lib/api";
import { ago, bytes, latencyTone } from "@/lib/format";
import { useLive } from "@/lib/live";
import { usePoll } from "@/lib/use-api";
import { cn } from "@/lib/utils";

const HOURS = [
  { value: "0", label: "Never (manual)" },
  { value: "6", label: "Every 6 hours" },
  { value: "12", label: "Every 12 hours" },
  { value: "24", label: "Every day" },
  { value: "72", label: "Every 3 days" },
];

function SubscriptionDialog({ open, onOpenChange, sub, onSaved }) {
  const editing = !!sub;
  const [url, setUrl] = React.useState("");
  const [name, setName] = React.useState("");
  const [hours, setHours] = React.useState("12");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");
  React.useEffect(() => {
    if (open) {
      setUrl("");
      setName(sub?.name || "");
      setHours(String(sub?.autoUpdateHours ?? 12));
      setError("");
    }
  }, [open, sub]);

  const save = async (e) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    const body = { name, autoUpdateHours: Number(hours) };
    if (url) body.url = url;
    const res = editing ? await Subs.update(sub.id, body) : await Subs.create(body);
    setBusy(false);
    if (!res.ok) {
      setError(errorOf(res));
      return;
    }
    if (!editing) {
      if (res.body.warning) toast.warning(`Added, but the download failed: ${res.body.warning}`);
      else toast.success(`Added ${res.body.subscription.name}: ${res.body.servers} servers`);
    } else toast.success("Saved");
    onOpenChange(false);
    onSaved(res.body.subscription?.id);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[520px]">
        <form onSubmit={save} className="space-y-5">
          <DialogHeader>
            <DialogTitle>{editing ? "Edit subscription" : "Add subscription"}</DialogTitle>
            <DialogDescription>
              {editing ? "Leave the URL empty to keep the current one." : "Paste a V2Ray subscription link (the base64 list most panels give for v2rayN / v2rayNG)."}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="sub-url">Subscription URL</Label>
            <Input id="sub-url" autoFocus={!editing} value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://panel.example.com/sub/…" className="font-mono text-sm" />
            <p className="text-xs text-muted-foreground">The URL is a secret. It is stored on the server only and shown masked.</p>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="sub-name">Name</Label>
              <Input id="sub-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="optional" maxLength={60} />
            </div>
            <div className="space-y-2">
              <Label>Update automatically</Label>
              <Select value={hours} onValueChange={setHours}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {HOURS.map((h) => (
                    <SelectItem key={h.value} value={h.value}>
                      {h.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          {error ? <p className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || (!editing && !url)}>
              {busy ? <Loader2 className="animate-spin" /> : null}
              {editing ? "Save" : "Add and download"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function LinkDialog({ open, onOpenChange, onSaved }) {
  const [link, setLink] = React.useState("");
  const [name, setName] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");
  React.useEffect(() => {
    if (open) {
      setLink("");
      setName("");
      setError("");
    }
  }, [open]);
  const save = async (e) => {
    e.preventDefault();
    setBusy(true);
    const res = await Servers.add(link.trim(), name);
    setBusy(false);
    if (!res.ok) {
      setError(errorOf(res));
      return;
    }
    toast.success("Server added to the Manual list");
    onOpenChange(false);
    onSaved("manual");
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[560px]">
        <form onSubmit={save} className="space-y-5">
          <DialogHeader>
            <DialogTitle>Add a server link</DialogTitle>
            <DialogDescription>vless://, vmess://, trojan://, ss:// or hysteria2:// — one link.</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="link">Link</Label>
            <textarea
              id="link"
              autoFocus
              value={link}
              onChange={(e) => setLink(e.target.value)}
              rows={4}
              className="w-full rounded-[10px] border border-input bg-card px-3.5 py-2.5 font-mono text-sm focus-visible:border-ring focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-ring/15"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="link-name">Name</Label>
            <Input id="link-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="optional" />
          </div>
          {error ? <p className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !link.trim()}>
              {busy ? <Loader2 className="animate-spin" /> : null} Add
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Latency({ ms, error, at }) {
  if (ms !== null && ms !== undefined) {
    const tone = latencyTone(ms);
    return (
      <span className="inline-flex items-center gap-1.5 tabular">
        <span className={cn("size-1.5 rounded-full", { success: "bg-success", warning: "bg-warning", destructive: "bg-destructive" }[tone])} />
        {ms} ms
      </span>
    );
  }
  if (error) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="inline-flex cursor-help items-center gap-1.5 text-destructive">
            <X className="size-3.5" /> failed
          </span>
        </TooltipTrigger>
        <TooltipContent className="max-w-xs">{error}</TooltipContent>
      </Tooltip>
    );
  }
  return <span className="text-muted-foreground/60">–</span>;
}

function SubInfo({ sub, onRefresh, onEdit, onDelete, refreshing }) {
  const ui = sub.userInfo;
  const used = ui ? ui.upload + ui.download : 0;
  const expired = ui?.expire && ui.expire * 1000 < Date.now();
  return (
    <Card className="mb-5 flex flex-col gap-4 p-5 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0 space-y-1.5">
        <div className="flex flex-wrap items-center gap-2.5">
          <span className="display text-md font-semibold">{sub.name}</span>
          {sub.kind === "url" ? <Mono className="text-muted-foreground">{sub.url}</Mono> : <Badge variant="secondary">manual list</Badge>}
        </div>
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-sm text-muted-foreground">
          <span>{sub.serverCount} servers</span>
          {sub.kind === "url" ? <span>updated {ago(sub.lastSuccessAt)}{sub.fetchedViaVpn ? " (through the VPN)" : ""}</span> : null}
          {sub.kind === "url" ? <span>{sub.autoUpdateHours ? `auto every ${sub.autoUpdateHours}h` : "manual updates"}</span> : null}
          {ui?.expire ? (
            <span className={cn(expired && "text-destructive")}>
              {expired ? "expired" : "expires"} {new Date(ui.expire * 1000).toLocaleDateString()}
            </span>
          ) : null}
        </div>
        {sub.lastError ? (
          <p className="flex items-center gap-1.5 text-sm text-destructive">
            <CircleAlert className="size-4" /> Last update failed: {sub.lastError}
          </p>
        ) : null}
        {sub.skipped?.length ? <p className="text-xs text-muted-foreground">{sub.skipped.length} links skipped (unsupported): {sub.skipped.slice(0, 2).join("; ")}</p> : null}
      </div>
      <div className="flex shrink-0 items-center gap-4">
        {ui?.total ? (
          <div className="w-44">
            <div className="mb-1.5 flex justify-between text-xs text-muted-foreground">
              <span>Plan usage</span>
              <span className="tabular">
                {bytes(used)} / {bytes(ui.total)}
              </span>
            </div>
            <QuotaMeter used={used} limit={ui.total} />
          </div>
        ) : null}
        {sub.kind === "url" ? (
          <Button variant="outline" onClick={onRefresh} disabled={refreshing}>
            {refreshing ? <Loader2 className="animate-spin" /> : <RefreshCw />} Update
          </Button>
        ) : null}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="More">
              <MoreHorizontal />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {sub.kind === "url" ? (
              <DropdownMenuItem onSelect={onEdit}>
                <Pencil /> Edit
              </DropdownMenuItem>
            ) : null}
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={onDelete} className="text-destructive focus:text-destructive">
              <Trash2 /> Remove
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </Card>
  );
}

export default function ServersPage() {
  const [params, setParams] = useSearchParams();
  const subId = params.get("sub") || "";
  const { status, reload: reloadStatus } = useLive();
  const confirm = useConfirm();
  const job = status?.test;
  const subs = usePoll(() => Subs.list(), [], 0);
  const servers = usePoll(() => Servers.list(subId), [subId], job ? 1500 : 15000);
  const [q, setQ] = React.useState("");
  const [sort, setSort] = React.useState("default");
  const [dialog, setDialog] = React.useState(null);
  const [connecting, setConnecting] = React.useState("");
  const [refreshing, setRefreshing] = React.useState(false);

  // When a test finishes, fetch the final results once more.
  const wasRunning = React.useRef(false);
  React.useEffect(() => {
    if (wasRunning.current && !job) servers.reload();
    wasRunning.current = !!job;
  }, [job]); // eslint-disable-line react-hooks/exhaustive-deps

  const subList = subs.data?.subscriptions || [];
  const current = subList.find((s) => s.id === subId);
  const list = React.useMemo(() => {
    let l = servers.data?.servers || [];
    if (q) {
      const lq = q.toLowerCase();
      l = l.filter((s) => s.name.toLowerCase().includes(lq) || s.address.toLowerCase().includes(lq) || s.protocol.includes(lq));
    }
    const key = (v) => (v === null || v === undefined ? Infinity : v);
    if (sort === "delay") l = [...l].sort((a, b) => key(a.delayMs) - key(b.delayMs));
    if (sort === "tcp") l = [...l].sort((a, b) => key(a.tcpMs) - key(b.tcpMs));
    return l;
  }, [servers.data, q, sort]);

  const selectSub = (id) => {
    const p = new URLSearchParams(params);
    if (id) p.set("sub", id);
    else p.delete("sub");
    setParams(p, { replace: true });
  };

  const runTest = async (kind) => {
    const ids = q ? list.map((s) => s.id) : undefined;
    const res = await Servers.test(kind, subId, ids);
    if (!res.ok) toast.error(errorOf(res));
    reloadStatus();
    servers.reload();
  };
  const cancelTest = async () => {
    await Servers.cancelTest();
    reloadStatus();
  };
  const connect = async (s) => {
    setConnecting(s.id);
    const res = await Servers.connect(s.id);
    setConnecting("");
    if (res.ok) toast.success(`Connected to ${s.name}`);
    else toast.error(errorOf(res));
    reloadStatus();
    servers.reload();
  };
  const refreshSub = async () => {
    setRefreshing(true);
    const res = await Subs.refresh(current.id);
    setRefreshing(false);
    if (res.ok) toast.success(`Updated: ${res.body.servers} servers`);
    else toast.error(errorOf(res));
    subs.reload();
    servers.reload();
  };
  const deleteSub = async () => {
    const ok = await confirm({
      title: `Remove ${current.name}?`,
      description: "Its servers are removed too. The active connection keeps working until you change servers.",
      confirmLabel: "Remove",
      destructive: true,
    });
    if (!ok) return;
    const res = await Subs.remove(current.id);
    if (!res.ok) toast.error(errorOf(res));
    selectSub("");
    subs.reload();
    servers.reload();
  };
  const removeManual = async (s) => {
    const res = await Servers.remove(s.id);
    if (!res.ok) toast.error(errorOf(res));
    servers.reload();
    subs.reload();
  };

  const activeId = servers.data?.activeId;
  const connected = servers.data?.connected;
  const total = subList.reduce((n, s) => n + s.serverCount, 0);

  return (
    <Page>
      <PageHeader
        title="Servers"
        description="Your subscriptions and their servers. Test them, then connect the one that works best."
        actions={
          <>
            <Button variant="outline" onClick={() => setDialog("link")}>
              <Link2 /> Add link
            </Button>
            <Button onClick={() => setDialog("add")}>
              <Plus /> Add subscription
            </Button>
          </>
        }
      />

      {subList.length > 0 ? (
        <div className="mb-5 flex flex-wrap gap-2">
          {[{ id: "", name: "All", serverCount: total }, ...subList].map((s) => (
            <button
              key={s.id || "all"}
              type="button"
              onClick={() => selectSub(s.id)}
              className={cn(
                "flex h-10 items-center gap-2 rounded-full border px-4 text-sm font-medium transition-colors",
                subId === s.id ? "border-foreground/80 bg-foreground text-background" : "border-border bg-card text-muted-foreground hover:text-foreground",
              )}
            >
              {s.name}
              <span className={cn("tabular text-xs", subId === s.id ? "opacity-70" : "opacity-60")}>{s.serverCount}</span>
              {s.lastError ? <CircleAlert className="size-3.5 text-destructive" /> : null}
            </button>
          ))}
        </div>
      ) : null}

      {current ? (
        <SubInfo sub={current} refreshing={refreshing} onRefresh={refreshSub} onEdit={() => setDialog("edit")} onDelete={deleteSub} />
      ) : null}

      {subList.length === 0 && !subs.loading ? (
        <Card>
          <Empty
            icon={Server}
            title="No subscriptions yet"
            action={
              <Button size="lg" onClick={() => setDialog("add")}>
                <Plus /> Add subscription
              </Button>
            }
          >
            Add the subscription link from your V2Ray provider. v2dock downloads it, keeps it up to date, and falls back to downloading it through the VPN when the direct route is cut.
          </Empty>
        </Card>
      ) : (
        <Card className="overflow-hidden">
          <div className="flex flex-col gap-3 border-b border-border p-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="relative w-full sm:max-w-xs">
              <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Filter by name or address" className="pl-9" />
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Segmented
                size="sm"
                value={sort}
                onChange={setSort}
                options={[
                  { value: "default", label: "Order" },
                  { value: "delay", label: "Fastest" },
                  { value: "tcp", label: "TCP" },
                ]}
              />
              {job ? (
                <div className="flex items-center gap-2">
                  <span className="flex items-center gap-2 rounded-full bg-running/12 px-3 py-1.5 text-sm font-medium text-running">
                    <Loader2 className="size-4 animate-spin" />
                    <span className="tabular">
                      {job.kind === "tcp" ? "TCP ping" : "Real delay"} {job.done}/{job.total}
                    </span>
                  </span>
                  <Button variant="ghost" size="sm" onClick={cancelTest}>
                    Stop
                  </Button>
                </div>
              ) : (
                <>
                  <Button variant="outline" size="sm" onClick={() => runTest("tcp")} disabled={!list.length}>
                    <Timer /> TCP ping
                  </Button>
                  <Button variant="outline" size="sm" onClick={() => runTest("real")} disabled={!list.length}>
                    <Zap /> Real delay
                  </Button>
                </>
              )}
            </div>
          </div>

          <div className="hidden grid-cols-[minmax(0,1fr)_90px_110px_130px] gap-4 border-b border-border px-5 py-2.5 text-xs font-medium uppercase tracking-wide text-muted-foreground md:grid">
            <span>Server</span>
            <span>TCP</span>
            <span>Real delay</span>
            <span />
          </div>
          <div className="divide-y divide-border">
            {list.map((s) => {
              const active = s.id === activeId && connected;
              return (
                <div
                  key={`${s.subscriptionId}-${s.id}`}
                  className={cn(
                    "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-2 px-5 py-3.5 md:grid-cols-[minmax(0,1fr)_90px_110px_130px]",
                    active && "bg-success/6",
                  )}
                >
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="truncate font-medium">{s.name}</span>
                      {active ? <Badge variant="success">connected</Badge> : null}
                      {s.warning ? (
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <CircleAlert className="size-4 shrink-0 text-warning" />
                          </TooltipTrigger>
                          <TooltipContent className="max-w-xs">{s.warning}</TooltipContent>
                        </Tooltip>
                      ) : null}
                    </div>
                    <div className="mt-0.5 flex flex-wrap items-center gap-x-2.5 text-[0.8125rem] text-muted-foreground">
                      <span className="font-medium uppercase tracking-wide">{s.protocol}</span>
                      <span>
                        {s.transport} · {s.security}
                      </span>
                      <Mono className="truncate">
                        {s.address}:{s.port}
                      </Mono>
                    </div>
                  </div>
                  <div className="hidden text-sm md:block">
                    <Latency ms={s.tcpMs} error={s.tcpError} />
                  </div>
                  <div className="hidden text-sm md:block">
                    <Latency ms={s.delayMs} error={s.delayError} />
                  </div>
                  <div className="flex items-center justify-end gap-1">
                    <div className="mr-2 text-sm md:hidden">
                      <Latency ms={s.delayMs ?? s.tcpMs} error={s.delayError} />
                    </div>
                    {active ? (
                      <Button variant="ghost" size="sm" disabled>
                        In use
                      </Button>
                    ) : (
                      <Button variant={connected ? "outline" : "default"} size="sm" onClick={() => connect(s)} disabled={!!connecting}>
                        {connecting === s.id ? <Loader2 className="animate-spin" /> : null}
                        Connect
                      </Button>
                    )}
                    {s.subscriptionId === "manual" ? (
                      <Button variant="ghost" size="icon-sm" aria-label="Remove" onClick={() => removeManual(s)}>
                        <Trash2 />
                      </Button>
                    ) : null}
                  </div>
                </div>
              );
            })}
            {!list.length && !servers.loading ? (
              <Empty icon={Search} title={q ? "No server matches" : "No servers in this list"} className="py-10" />
            ) : null}
          </div>
        </Card>
      )}

      <SubscriptionDialog
        open={dialog === "add" || dialog === "edit"}
        sub={dialog === "edit" ? current : null}
        onOpenChange={(o) => !o && setDialog(null)}
        onSaved={(id) => {
          subs.reload();
          if (id) selectSub(id);
          servers.reload();
        }}
      />
      <LinkDialog
        open={dialog === "link"}
        onOpenChange={(o) => !o && setDialog(null)}
        onSaved={() => {
          subs.reload();
          selectSub("manual");
        }}
      />
    </Page>
  );
}
