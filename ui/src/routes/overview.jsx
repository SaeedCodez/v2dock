import * as React from "react";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { ArrowRight, Boxes, Gauge, Globe2, Loader2, PlugZap, ShieldAlert, Unplug, Zap } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import { Dot, Empty, Mono, Page, PageHeader, StatTile } from "@/components/page";
import { TrafficChart } from "@/components/charts";
import { useConfirm } from "@/components/confirm";
import { Events, Servers, Settings, Usage, errorOf } from "@/lib/api";
import { bytes, since } from "@/lib/format";
import { connectionState, useLive } from "@/lib/live";
import { usePoll } from "@/lib/use-api";
import { EventRow } from "@/routes/activity";

function ConnectionCard() {
  const { status, reload } = useLive();
  const confirm = useConfirm();
  const [busy, setBusy] = React.useState("");
  const st = connectionState(status);

  const connectBest = async () => {
    setBusy("best");
    const id = toast.loading("Testing servers to find the fastest…");
    const res = await Servers.connectBest();
    toast.dismiss(id);
    setBusy("");
    if (res.ok) toast.success(`Connected to ${res.body.server.name}`);
    else toast.error(errorOf(res));
    reload();
  };
  const disconnect = async () => {
    const ok = await confirm({
      title: "Disconnect the VPN?",
      description: "Routed containers go back to their normal network. During an international shutdown they will lose access to the outside world.",
      confirmLabel: "Disconnect",
      destructive: true,
    });
    if (!ok) return;
    setBusy("off");
    const res = await Servers.disconnect();
    setBusy("");
    if (!res.ok) toast.error(errorOf(res));
    reload();
  };
  const toggleBypass = async (v) => {
    const res = await Settings.put({ bypassIran: v });
    if (!res.ok) toast.error(errorOf(res));
    else toast.success(v ? "Iranian sites now go direct" : "All traffic of routed containers now goes through the VPN");
    reload();
  };

  const headline = {
    loading: "Loading…",
    off: "Disconnected",
    checking: "Connected · checking",
    ok: "Connected",
    degraded: "Connected · unstable",
    down: "Server not responding",
    error: "Not working",
  }[st];
  const tone = { ok: "success", degraded: "warning", down: "destructive", error: "destructive", checking: "running" }[st] || "neutral";

  return (
    <Card className="overflow-hidden">
      <div className="flex flex-col gap-6 p-6 sm:p-7 lg:flex-row lg:items-center lg:justify-between">
        <div className="flex min-w-0 items-start gap-5">
          <span className={`grid size-14 shrink-0 place-items-center rounded-2xl ${st === "ok" ? "bg-success/12 text-success" : st === "off" ? "bg-secondary text-muted-foreground" : tone === "destructive" ? "bg-destructive/12 text-destructive" : "bg-primary/12 text-primary"}`}>
            {st === "off" ? <Unplug className="size-7" /> : <PlugZap className="size-7" />}
          </span>
          <div className="min-w-0">
            <h2 className="display flex items-center gap-2.5 text-xl font-semibold sm:text-2xl">
              <Dot tone={tone} pulse={st === "checking"} className="shrink-0" />
              <span>{headline}</span>
            </h2>
            {status?.connected && status.server ? (
              <div className="mt-2 space-y-1">
                <div className="text-md font-medium">{status.server.name}</div>
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-muted-foreground">
                  <Mono>{status.server.address}</Mono>
                  <span>{status.server.protocol}</span>
                  {status.connectedAt ? <span>for {since(status.connectedAt)}</span> : null}
                  {status.health?.ok ? <span className="tabular">{status.health.delayMs} ms real delay</span> : null}
                </div>
                {status.health?.error && !status.health.ok ? (
                  <p className="text-sm text-destructive">
                    Health check failed {status.health.failures}× ({status.health.error}).{status.autoFailover ? " Auto failover will switch servers." : ""}
                  </p>
                ) : null}
                {status.error ? <p className="text-sm text-destructive">{status.error}</p> : null}
                {status.firewallError ? <p className="text-sm text-destructive">Firewall: {status.firewallError}</p> : null}
              </div>
            ) : (
              <p className="mt-2 max-w-xl text-base text-muted-foreground">
                Containers use their normal network. Connect a server to send the chosen containers' international traffic through it.
              </p>
            )}
          </div>
        </div>
        <div className="flex shrink-0 flex-wrap gap-2.5">
          {status?.connected ? (
            <>
              <Button variant="outline" size="lg" asChild>
                <Link to="/servers">Change server</Link>
              </Button>
              <Button variant="outline" size="lg" onClick={disconnect} disabled={!!busy}>
                {busy === "off" ? <Loader2 className="animate-spin" /> : <Unplug />} Disconnect
              </Button>
            </>
          ) : (
            <>
              <Button variant="outline" size="lg" asChild>
                <Link to="/servers">Choose server</Link>
              </Button>
              <Button size="lg" onClick={connectBest} disabled={!!busy}>
                {busy === "best" ? <Loader2 className="animate-spin" /> : <Zap />} Connect fastest
              </Button>
            </>
          )}
        </div>
      </div>
      <div className="flex flex-col gap-4 border-t border-border bg-surface px-6 py-4 sm:flex-row sm:items-center sm:justify-between sm:px-7">
        <label className="flex cursor-pointer items-start gap-3.5">
          <Switch checked={!!status?.bypassIran} onCheckedChange={toggleBypass} disabled={!status} className="mt-0.5" />
          <span>
            <span className="block font-medium">Iranian sites go direct</span>
            <span className="block text-sm text-muted-foreground">.ir domains and Iranian IP ranges skip the VPN (saves VPN traffic, keeps local services fast).</span>
          </span>
        </label>
        <Badge variant={status?.autoFailover ? "success" : "secondary"} className="self-start sm:self-center">
          {status?.autoFailover ? "Auto failover on" : "Auto failover off"}
        </Badge>
      </div>
    </Card>
  );
}

export default function Overview() {
  const { status } = useLive();
  const usage = usePoll(() => Usage.get("24h"), [], 30000);
  const events = usePoll(() => Events.list(6), [], 15000);
  const today = status?.today || { vpn: 0, direct: 0 };

  return (
    <Page>
      <PageHeader title="Overview" description="The VPN connection, the containers that use it, and today's traffic." />
      <div className="space-y-6">
        <ConnectionCard />

        <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <StatTile label="Through VPN today" value={bytes(today.vpn)} swatch="var(--series-vpn)" />
          <StatTile label="Direct today" value={bytes(today.direct)} swatch="var(--series-direct)" />
          <StatTile
            label="Containers on VPN"
            icon={Boxes}
            value={status ? `${status.routedRunning}` : "–"}
            hint={status ? `${status.routes} selected · ${status.docker?.containers ?? 0} on this server` : ""}
          />
          <StatTile
            label="Over quota"
            icon={status?.quotaExceeded ? ShieldAlert : Gauge}
            value={status ? String(status.quotaExceeded) : "–"}
            hint={status?.quotaExceeded ? "see Containers" : "all within limits"}
          />
        </div>

        {status && !status.docker?.ok ? (
          <Card className="border-destructive/30">
            <CardContent className="flex items-start gap-3 pt-5 text-sm">
              <ShieldAlert className="mt-0.5 size-4 shrink-0 text-destructive" />
              <span>Docker is not reachable: {status.docker?.error}. Check that the guard service is running.</span>
            </CardContent>
          </Card>
        ) : null}

        <Card>
          <CardHeader>
            <div>
              <CardTitle>Traffic, last 24 hours</CardTitle>
              <CardDescription>All containers. Direct includes traffic of containers that do not use the VPN.</CardDescription>
            </div>
            <Button variant="ghost" asChild>
              <Link to="/usage">
                Usage details <ArrowRight />
              </Link>
            </Button>
          </CardHeader>
          <CardContent>
            {usage.data ? (
              <TrafficChart points={usage.data.series} since={usage.data.since} bucket={usage.data.bucket} />
            ) : (
              <div className="h-[260px]" />
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <div>
              <CardTitle>Recent activity</CardTitle>
              <CardDescription>Connections, failovers, quota events and logins.</CardDescription>
            </div>
            <Button variant="ghost" asChild>
              <Link to="/activity">
                All activity <ArrowRight />
              </Link>
            </Button>
          </CardHeader>
          <CardContent className="px-0 pb-2">
            {events.data?.events?.length ? (
              <div className="divide-y divide-border">
                {events.data.events.map((e) => (
                  <EventRow key={e.id} e={e} />
                ))}
              </div>
            ) : (
              <Empty icon={Globe2} title="Nothing yet" className="py-8">
                Add a subscription on the <Link to="/servers" className="text-primary underline-offset-2 hover:underline">Servers</Link> page to get started.
              </Empty>
            )}
          </CardContent>
        </Card>
      </div>
    </Page>
  );
}
