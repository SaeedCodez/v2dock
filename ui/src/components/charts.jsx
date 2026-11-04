import * as React from "react";
import { bytes, shortTime } from "@/lib/format";
import { cn } from "@/lib/utils";

/*
 * Small, dependency-free SVG charts. Conventions (see the dataviz notes in
 * docs/ui.md): VPN = categorical slot 1 (blue), Direct = slot 2 (orange),
 * columns <= 24px with a 4px rounded data-end and a 2px surface gap between
 * stacked segments, 1px recessive grid, text in text colours only, a legend
 * for two series, and a hover tooltip on every chart.
 */

const VPN = "var(--series-vpn)";
const DIRECT = "var(--series-direct)";

function useWidth(ref) {
  const [w, setW] = React.useState(0);
  React.useLayoutEffect(() => {
    if (!ref.current) return undefined;
    const ro = new ResizeObserver(([e]) => setW(Math.floor(e.contentRect.width)));
    ro.observe(ref.current);
    setW(Math.floor(ref.current.getBoundingClientRect().width));
    return () => ro.disconnect();
  }, [ref]);
  return w;
}

/** niceScale picks a round top and ticks for a max value. */
function niceScale(max, { binary = false, ticks = 4 } = {}) {
  if (!max || max <= 0) return { top: binary ? 1024 : 1, step: binary ? 256 : 0.25, unit: 1 };
  let unit = 1;
  if (binary) {
    while (max / unit >= 1024) unit *= 1024;
  }
  const raw = max / unit / ticks;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const norm = raw / mag;
  const nice = norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 2.5 ? 2.5 : norm <= 5 ? 5 : 10;
  const step = nice * mag * unit;
  const top = Math.ceil(max / step) * step;
  return { top, step, unit };
}

function roundedTop(x, y, w, h, r) {
  r = Math.max(0, Math.min(r, w / 2, h));
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`;
}

export function Legend({ items, className }) {
  return (
    <div className={cn("flex flex-wrap items-center gap-x-5 gap-y-1 text-sm text-muted-foreground", className)}>
      {items.map((i) => (
        <span key={i.label} className="flex items-center gap-2">
          <span className="size-2.5 rounded-[3px]" style={{ background: i.color }} />
          {i.label}
        </span>
      ))}
    </div>
  );
}

function Tooltip({ x, y, width, children }) {
  const left = Math.min(Math.max(x + 14, 0), Math.max(0, width - 200));
  return (
    <div
      className="pointer-events-none absolute z-10 min-w-[170px] rounded-xl border border-border bg-popover px-3.5 py-2.5 text-sm text-popover-foreground shadow-lg"
      style={{ left, top: Math.max(0, y) }}
    >
      {children}
    </div>
  );
}

/** Keep x-axis labels inside the plot: anchor the edge ones inward. */
function anchorFor(x, width, padL) {
  if (x > width - 34) return "end";
  if (x < padL + 20) return "start";
  return "middle";
}

function fillBuckets(points, since, bucket) {
  const map = new Map(points.map((p) => [p.ts, p]));
  const out = [];
  const end = Math.floor(Date.now() / 1000 / bucket) * bucket;
  for (let t = since; t <= end; t += bucket) {
    const p = map.get(t);
    out.push({ ts: t, vpn: p?.vpn || 0, direct: p?.direct || 0, cpu: p?.cpu || 0, mem: p?.mem || 0, has: !!p });
  }
  return out;
}

/** TrafficChart: stacked columns of VPN and direct bytes per time bucket. */
export function TrafficChart({ points = [], since, bucket, height = 240, className }) {
  const ref = React.useRef(null);
  const width = useWidth(ref);
  const [hover, setHover] = React.useState(null);
  const data = React.useMemo(() => fillBuckets(points, since, bucket), [points, since, bucket]);
  const pad = { l: 64, r: 6, t: 10, b: 28 };
  const innerW = Math.max(0, width - pad.l - pad.r);
  const innerH = height - pad.t - pad.b;
  const max = Math.max(0, ...data.map((d) => d.vpn + d.direct));
  const { top, step } = niceScale(max, { binary: true });
  const y = (v) => pad.t + innerH - (v / top) * innerH;
  const band = data.length ? innerW / data.length : 0;
  const barW = Math.max(1, Math.min(24, band - 2));
  const withDay = bucket >= 3600 * 6 || data.length * bucket > 2 * 86400;
  const labelEvery = Math.max(1, Math.ceil(data.length / Math.max(2, Math.floor(innerW / 90))));
  const ticks = [];
  for (let v = 0; v <= top + 1e-9; v += step) ticks.push(v);

  const onMove = (e) => {
    const rect = e.currentTarget.getBoundingClientRect();
    const px = e.clientX - rect.left - pad.l;
    const i = Math.floor(px / band);
    if (i >= 0 && i < data.length) setHover({ i, x: e.clientX - rect.left, y: e.clientY - rect.top - 70 });
    else setHover(null);
  };

  return (
    <div className={cn("space-y-3", className)}>
      <Legend items={[{ label: "Through VPN", color: VPN }, { label: "Direct", color: DIRECT }]} />
      <div ref={ref} className="relative" style={{ height }}>
        {width > 0 && (
          <svg width={width} height={height} onMouseMove={onMove} onMouseLeave={() => setHover(null)} role="img" aria-label="Traffic over time, through VPN and direct">
            {ticks.map((v) => (
              <g key={v}>
                <line x1={pad.l} x2={width - pad.r} y1={y(v)} y2={y(v)} stroke="var(--chart-grid)" strokeWidth="1" />
                <text x={pad.l - 10} y={y(v)} dy="0.32em" textAnchor="end" className="fill-muted-foreground text-[11px] tabular">
                  {v === 0 ? "0" : bytes(v, v % 1 ? 1 : 0)}
                </text>
              </g>
            ))}
            {hover ? <rect x={pad.l + hover.i * band} y={pad.t} width={band} height={innerH} fill="var(--accent)" opacity="0.6" /> : null}
            {data.map((d, i) => {
              const x = pad.l + i * band + (band - barW) / 2;
              const vh = (d.vpn / top) * innerH;
              const dh = (d.direct / top) * innerH;
              const gap = vh > 0 && dh > 0 ? 2 : 0;
              const base = pad.t + innerH;
              return (
                <g key={d.ts}>
                  {vh > 0 && (dh > 0 ? (
                    <rect x={x} y={base - vh} width={barW} height={vh} fill={VPN} />
                  ) : (
                    <path d={roundedTop(x, base - vh, barW, vh, 4)} fill={VPN} />
                  ))}
                  {dh > 0 && <path d={roundedTop(x, base - vh - gap - dh, barW, dh, 4)} fill={DIRECT} />}
                </g>
              );
            })}
            <line x1={pad.l} x2={width - pad.r} y1={pad.t + innerH} y2={pad.t + innerH} stroke="var(--border)" strokeWidth="1" />
            {data.map((d, i) =>
              i % labelEvery === 0 ? (
                <text key={d.ts} x={pad.l + i * band + band / 2} y={height - 8} textAnchor={anchorFor(pad.l + i * band + band / 2, width, pad.l)} className="fill-muted-foreground text-[11px]">
                  {shortTime(d.ts, withDay)}
                </text>
              ) : null,
            )}
          </svg>
        )}
        {hover && data[hover.i] ? (
          <Tooltip x={hover.x} y={hover.y} width={width}>
            <div className="mb-1.5 text-xs text-muted-foreground">
              {new Date(data[hover.i].ts * 1000).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}
            </div>
            <Row color={VPN} label="Through VPN" value={bytes(data[hover.i].vpn)} />
            <Row color={DIRECT} label="Direct" value={bytes(data[hover.i].direct)} />
          </Tooltip>
        ) : null}
      </div>
    </div>
  );
}

function Row({ color, label, value }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span className="flex items-center gap-2">
        <span className="size-2.5 rounded-[3px]" style={{ background: color }} />
        {label}
      </span>
      <span className="font-medium tabular">{value}</span>
    </div>
  );
}

/** MetricChart: one series over time (CPU % or memory), line + light wash. */
export function MetricChart({ points = [], since, bucket, field, format, height = 180, binary, label }) {
  const ref = React.useRef(null);
  const width = useWidth(ref);
  const [hover, setHover] = React.useState(null);
  const data = React.useMemo(() => fillBuckets(points, since, bucket), [points, since, bucket]);
  const pad = { l: 64, r: 10, t: 10, b: 28 };
  const innerW = Math.max(0, width - pad.l - pad.r);
  const innerH = height - pad.t - pad.b;
  const max = Math.max(0, ...data.map((d) => d[field]));
  const { top, step } = niceScale(max, { binary });
  const x = (i) => pad.l + (data.length <= 1 ? innerW / 2 : (i / (data.length - 1)) * innerW);
  const y = (v) => pad.t + innerH - (v / top) * innerH;
  const pts = data.map((d, i) => (d.has ? [x(i), y(d[field])] : null));
  let line = "";
  let area = "";
  let segStart = null;
  pts.forEach((p, i) => {
    if (!p) {
      if (segStart !== null) area += `L${pts[i - 1][0]},${y(0)}Z`;
      segStart = null;
      return;
    }
    if (segStart === null) {
      line += `M${p[0]},${p[1]}`;
      area += `M${p[0]},${y(0)}L${p[0]},${p[1]}`;
      segStart = i;
    } else {
      line += `L${p[0]},${p[1]}`;
      area += `L${p[0]},${p[1]}`;
    }
    if (i === pts.length - 1) area += `L${p[0]},${y(0)}Z`;
  });
  const ticks = [];
  for (let v = 0; v <= top + 1e-9; v += step) ticks.push(v);
  const withDay = bucket >= 3600 * 6 || data.length * bucket > 2 * 86400;
  const labelEvery = Math.max(1, Math.ceil(data.length / Math.max(2, Math.floor(innerW / 90))));

  const onMove = (e) => {
    const rect = e.currentTarget.getBoundingClientRect();
    const px = e.clientX - rect.left - pad.l;
    const i = Math.round((px / Math.max(1, innerW)) * (data.length - 1));
    if (i >= 0 && i < data.length) setHover({ i, x: e.clientX - rect.left, y: e.clientY - rect.top - 60 });
    else setHover(null);
  };

  return (
    <div ref={ref} className="relative" style={{ height }}>
      {width > 0 && (
        <svg width={width} height={height} onMouseMove={onMove} onMouseLeave={() => setHover(null)} role="img" aria-label={label}>
          {ticks.map((v) => (
            <g key={v}>
              <line x1={pad.l} x2={width - pad.r} y1={y(v)} y2={y(v)} stroke="var(--chart-grid)" strokeWidth="1" />
              <text x={pad.l - 10} y={y(v)} dy="0.32em" textAnchor="end" className="fill-muted-foreground text-[11px] tabular">
                {format(v)}
              </text>
            </g>
          ))}
          <path d={area} fill={VPN} opacity="0.1" />
          <path d={line} fill="none" stroke={VPN} strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />
          {hover && pts[hover.i] ? (
            <>
              <line x1={pts[hover.i][0]} x2={pts[hover.i][0]} y1={pad.t} y2={pad.t + innerH} stroke="var(--border)" />
              <circle cx={pts[hover.i][0]} cy={pts[hover.i][1]} r="4.5" fill={VPN} stroke="var(--card)" strokeWidth="2" />
            </>
          ) : null}
          {data.map((d, i) =>
            i % labelEvery === 0 ? (
              <text key={d.ts} x={x(i)} y={height - 8} textAnchor={anchorFor(x(i), width, pad.l)} className="fill-muted-foreground text-[11px]">
                {shortTime(d.ts, withDay)}
              </text>
            ) : null,
          )}
        </svg>
      )}
      {hover && data[hover.i] ? (
        <Tooltip x={hover.x} y={hover.y} width={width}>
          <div className="mb-1 text-xs text-muted-foreground">{new Date(data[hover.i].ts * 1000).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}</div>
          <div className="font-medium tabular">{data[hover.i].has ? format(data[hover.i][field]) : "no data"}</div>
        </Tooltip>
      ) : null}
    </div>
  );
}

/** ShareBar: one whole (a container's traffic) split into VPN and direct. */
export function ShareBar({ vpn, direct, className }) {
  const total = vpn + direct;
  if (!total) return <div className={cn("h-2.5 rounded-full bg-secondary", className)} />;
  const v = (vpn / total) * 100;
  return (
    <div className={cn("flex h-2.5 w-full gap-[2px] overflow-hidden rounded-full", className)} title={`VPN ${bytes(vpn)} · Direct ${bytes(direct)}`}>
      {vpn > 0 && <div style={{ width: `${v}%`, background: VPN }} className="h-full min-w-[3px]" />}
      {direct > 0 && <div style={{ width: `${100 - v}%`, background: DIRECT }} className="h-full min-w-[3px]" />}
    </div>
  );
}

/** QuotaMeter: used / limit; the fill carries severity, the track the same hue lighter. */
export function QuotaMeter({ used, limit, exceeded, className }) {
  const pct = limit > 0 ? Math.min(100, (used / limit) * 100) : 0;
  const tone = exceeded || pct >= 100 ? "destructive" : pct >= 80 ? "warning" : "primary";
  const fill = { primary: "bg-primary", warning: "bg-warning", destructive: "bg-destructive" }[tone];
  const track = { primary: "bg-primary/15", warning: "bg-warning/18", destructive: "bg-destructive/15" }[tone];
  return (
    <div className={cn("h-2 w-full overflow-hidden rounded-full", track, className)} role="meter" aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100}>
      <div className={cn("h-full rounded-full transition-[width]", fill)} style={{ width: `${Math.max(pct, used > 0 ? 2 : 0)}%` }} />
    </div>
  );
}
