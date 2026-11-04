import * as React from "react";
import { Activity, AlertTriangle, CircleX, Info, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Empty, Page, PageHeader, Segmented } from "@/components/page";
import { Events } from "@/lib/api";
import { ago, dateTime } from "@/lib/format";
import { cn } from "@/lib/utils";

const KINDS = [
  { value: "", label: "All" },
  { value: "connection", label: "Connection" },
  { value: "failover", label: "Failover" },
  { value: "health", label: "Health" },
  { value: "route", label: "Routing" },
  { value: "quota", label: "Quota" },
  { value: "subscription", label: "Subscriptions" },
  { value: "security", label: "Security" },
];

export function EventRow({ e }) {
  const Icon = e.level === "error" ? CircleX : e.level === "warn" ? AlertTriangle : Info;
  return (
    <div className="flex items-start gap-3.5 px-5 py-3.5">
      <Icon className={cn("mt-0.5 size-[18px] shrink-0", e.level === "error" ? "text-destructive" : e.level === "warn" ? "text-warning" : "text-muted-foreground")} />
      <div className="min-w-0 flex-1">
        <div className="break-words">{e.message}</div>
        <div className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-muted-foreground">
          <span title={dateTime(e.ts)}>{ago(e.ts)}</span>
          <span className="capitalize">{e.kind}</span>
          {e.actor ? <span>by {e.actor}</span> : null}
          {e.ip ? <span className="font-mono">{e.ip}</span> : null}
        </div>
      </div>
    </div>
  );
}

export default function ActivityPage() {
  const [kind, setKind] = React.useState("");
  const [events, setEvents] = React.useState([]);
  const [loading, setLoading] = React.useState(true);
  const [more, setMore] = React.useState(false);

  const load = React.useCallback(
    async (before) => {
      setLoading(true);
      const res = await Events.list(50, before, kind);
      setLoading(false);
      if (!res.ok) return;
      const got = res.body.events || [];
      setEvents((prev) => (before ? [...prev, ...got] : got));
      setMore(got.length === 50);
    },
    [kind],
  );

  React.useEffect(() => {
    load();
    const t = setInterval(() => document.visibilityState === "visible" && load(), 20000);
    return () => clearInterval(t);
  }, [load]);

  return (
    <Page>
      <PageHeader title="Activity" description="Everything v2dock did and every admin action, newest first. Kept for 90 days." />
      <div className="mb-5 overflow-x-auto">
        <Segmented size="sm" value={kind} onChange={setKind} options={KINDS} />
      </div>
      <Card className="overflow-hidden">
        {events.length ? (
          <div className="divide-y divide-border">
            {events.map((e) => (
              <EventRow key={e.id} e={e} />
            ))}
          </div>
        ) : loading ? (
          <div className="grid h-40 place-items-center">
            <Loader2 className="size-5 animate-spin text-muted-foreground" />
          </div>
        ) : (
          <Empty icon={Activity} title="No activity" />
        )}
      </Card>
      {more ? (
        <div className="mt-5 flex justify-center">
          <Button variant="outline" onClick={() => load(events[events.length - 1]?.id)} disabled={loading}>
            Load older
          </Button>
        </div>
      ) : null}
    </Page>
  );
}
