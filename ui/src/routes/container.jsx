import * as React from "react";
import { Link, useSearchParams } from "react-router-dom";
import { ChevronLeft, Gauge } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, Mono, Page, Segmented, StatTile } from "@/components/page";
import { MetricChart, QuotaMeter, TrafficChart } from "@/components/charts";
import { Containers, Usage } from "@/lib/api";
import { PERIOD_LABEL, bytes } from "@/lib/format";
import { usePoll } from "@/lib/use-api";
import { ContainerRow, QuotaDialog, useContainerActions } from "@/routes/containers";

export const RANGES = [
  { value: "1h", label: "1 hour" },
  { value: "24h", label: "24 hours" },
  { value: "7d", label: "7 days" },
  { value: "30d", label: "30 days" },
];

export default function ContainerPage() {
  const [params] = useSearchParams();
  const key = params.get("key") || "";
  const [range, setRange] = React.useState("24h");
  const list = usePoll(() => Containers.list(), [], 10000);
  const usage = usePoll(() => Usage.get(range, key), [range, key], 30000);
  const acts = useContainerActions(list.reload);
  const c = (list.data?.containers || []).find((x) => x.key === key);
  const missing = (list.data?.missingRoutes || []).find((r) => r.key === key);
  const total = usage.data?.containers?.[0];
  const label = c?.label || missing?.label || key.replace(/^(compose|name):/, "");

  return (
    <Page>
      <Button variant="ghost" asChild className="-ml-3 mb-4">
        <Link to="/containers">
          <ChevronLeft /> Containers
        </Link>
      </Button>
      <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0">
          <h1 className="display text-2xl font-semibold sm:text-3xl">{label}</h1>
          <Mono className="mt-2 block text-muted-foreground">{key}</Mono>
        </div>
        <Segmented value={range} onChange={setRange} options={RANGES} />
      </div>

      {c ? (
        <Card className="mb-6 overflow-hidden">
          <ContainerRow c={c} busy={acts.busy === c.key} onToggle={acts.toggle} onQuota={acts.setQuotaFor} onReset={acts.reset} />
          {c.routed && c.route?.quotaBytes ? (
            <div className="border-t border-border bg-surface px-5 py-4">
              <div className="mb-2 flex flex-wrap justify-between gap-2 text-sm">
                <span className="flex items-center gap-2 font-medium">
                  <Gauge className="size-4 text-muted-foreground" /> VPN quota
                </span>
                <span className="tabular text-muted-foreground">
                  {bytes(c.route.quotaUsed)} of {bytes(c.route.quotaBytes)} {PERIOD_LABEL[c.route.quotaPeriod]} ·{" "}
                  {c.route.quotaAction === "block" ? "blocks international traffic when reached" : "falls back to the normal network when reached"}
                </span>
              </div>
              <QuotaMeter used={c.route.quotaUsed} limit={c.route.quotaBytes} exceeded={c.route.exceeded} />
            </div>
          ) : null}
        </Card>
      ) : null}

      <div className="mb-6 grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatTile label="Through VPN" value={bytes(total?.vpn || 0)} swatch="var(--series-vpn)" hint={total ? `↑ ${bytes(total.vpnUp)} ↓ ${bytes(total.vpnDown)}` : ""} />
        <StatTile label="Direct" value={bytes(total?.direct || 0)} swatch="var(--series-direct)" />
        <StatTile label="Average CPU" value={total ? `${total.cpu.toFixed(1)}%` : "–"} />
        <StatTile label="Peak memory" value={total ? bytes(total.mem) : "–"} />
      </div>

      {usage.data && usage.data.series.length === 0 ? (
        <Card>
          <Empty title="No data for this period yet">Samples are taken every 10–30 seconds while the container runs.</Empty>
        </Card>
      ) : usage.data ? (
        <div className="space-y-6">
          <Card>
            <CardHeader>
              <div>
                <CardTitle>Network traffic</CardTitle>
                <CardDescription>Bytes in both directions. VPN is what went through the server; direct is everything else.</CardDescription>
              </div>
            </CardHeader>
            <CardContent>
              <TrafficChart points={usage.data.series} since={usage.data.since} bucket={usage.data.bucket} />
            </CardContent>
          </Card>
          <div className="grid gap-6 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle>CPU</CardTitle>
              </CardHeader>
              <CardContent>
                <MetricChart label="CPU usage" points={usage.data.series} since={usage.data.since} bucket={usage.data.bucket} field="cpu" format={(v) => `${+v.toFixed(1)}%`} />
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle>Memory</CardTitle>
              </CardHeader>
              <CardContent>
                <MetricChart label="Memory usage" points={usage.data.series} since={usage.data.since} bucket={usage.data.bucket} field="mem" binary format={(v) => (v ? bytes(v, 0) : "0")} />
              </CardContent>
            </Card>
          </div>
        </div>
      ) : null}

      <QuotaDialog open={!!acts.quotaFor} container={acts.quotaFor} onOpenChange={(o) => !o && acts.setQuotaFor(null)} onSaved={list.reload} />
    </Page>
  );
}
