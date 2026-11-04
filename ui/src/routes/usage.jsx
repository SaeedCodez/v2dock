import * as React from "react";
import { Link } from "react-router-dom";
import { BarChart3 } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Empty, Page, PageHeader, Segmented, StatTile } from "@/components/page";
import { Legend, ShareBar, TrafficChart } from "@/components/charts";
import { Usage } from "@/lib/api";
import { bytes } from "@/lib/format";
import { usePoll } from "@/lib/use-api";
import { RANGES } from "@/routes/container";

export default function UsagePage() {
  const [range, setRange] = React.useState("24h");
  const [onlyVpn, setOnlyVpn] = React.useState("all");
  const usage = usePoll(() => Usage.get(range), [range], 30000);
  const rows = (usage.data?.containers || []).filter((r) => onlyVpn === "all" || r.routed);
  const tot = rows.reduce((a, r) => ({ vpn: a.vpn + r.vpn, direct: a.direct + r.direct }), { vpn: 0, direct: 0 });
  const share = tot.vpn + tot.direct ? Math.round((tot.vpn / (tot.vpn + tot.direct)) * 100) : 0;

  return (
    <Page>
      <PageHeader
        title="Usage"
        description="How much traffic each container sent through the VPN and how much went direct, plus CPU and memory."
        actions={<Segmented value={range} onChange={setRange} options={RANGES} />}
      />
      <div className="space-y-6">
        <div className="grid grid-cols-2 gap-4 lg:grid-cols-3">
          <StatTile label="Through VPN" value={bytes(tot.vpn)} swatch="var(--series-vpn)" />
          <StatTile label="Direct" value={bytes(tot.direct)} swatch="var(--series-direct)" />
          <StatTile label="Share through VPN" value={`${share}%`} className="col-span-2 lg:col-span-1" />
        </div>

        <Card>
          <CardHeader>
            <div>
              <CardTitle>Traffic over time</CardTitle>
              <CardDescription>All containers on this server.</CardDescription>
            </div>
          </CardHeader>
          <CardContent>{usage.data ? <TrafficChart points={usage.data.series} since={usage.data.since} bucket={usage.data.bucket} /> : <div className="h-[270px]" />}</CardContent>
        </Card>

        <Card className="overflow-hidden">
          <CardHeader className="border-b border-border">
            <div>
              <CardTitle>By container</CardTitle>
              <CardDescription>Sorted by VPN traffic. Open a container for its charts.</CardDescription>
            </div>
            <div className="flex flex-wrap items-center gap-4">
              <Legend items={[{ label: "VPN", color: "var(--series-vpn)" }, { label: "Direct", color: "var(--series-direct)" }]} />
              <Segmented
                size="sm"
                value={onlyVpn}
                onChange={setOnlyVpn}
                options={[
                  { value: "all", label: "All" },
                  { value: "vpn", label: "On VPN" },
                ]}
              />
            </div>
          </CardHeader>
          {rows.length ? (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[760px] text-sm">
                <thead>
                  <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                    <th className="px-5 py-2.5 font-medium">Container</th>
                    <th className="px-3 py-2.5 text-right font-medium">VPN</th>
                    <th className="px-3 py-2.5 text-right font-medium">Direct</th>
                    <th className="w-[22%] px-3 py-2.5 font-medium">Split</th>
                    <th className="px-3 py-2.5 text-right font-medium">Avg CPU</th>
                    <th className="px-5 py-2.5 text-right font-medium">Peak memory</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {rows.map((r) => (
                    <tr key={r.key} className="hover:bg-accent/40">
                      <td className="px-5 py-3">
                        <Link to={`/container?key=${encodeURIComponent(r.key)}`} className="font-medium hover:underline">
                          {r.label}
                        </Link>
                        {r.routed ? (
                          <Badge variant="secondary" className="ml-2">
                            VPN
                          </Badge>
                        ) : null}
                      </td>
                      <td className="px-3 py-3 text-right tabular">{bytes(r.vpn)}</td>
                      <td className="px-3 py-3 text-right tabular">{bytes(r.direct)}</td>
                      <td className="px-3 py-3">
                        <ShareBar vpn={r.vpn} direct={r.direct} />
                      </td>
                      <td className="px-3 py-3 text-right tabular">{r.cpu.toFixed(1)}%</td>
                      <td className="px-5 py-3 text-right tabular">{bytes(r.mem)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <Empty icon={BarChart3} title="No usage recorded in this period" className="py-12">
              Samples are collected every 10–30 seconds while containers run.
            </Empty>
          )}
        </Card>
      </div>
    </Page>
  );
}
