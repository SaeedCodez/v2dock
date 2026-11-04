/** bytes renders a byte count in binary units: 0 B, 812 KB, 4.2 GB. */
export function bytes(n, digits = 1) {
  if (n === null || n === undefined || Number.isNaN(n)) return "–";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let v = Math.abs(n);
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const s = i === 0 ? String(Math.round(v)) : v >= 100 ? v.toFixed(0) : v.toFixed(digits);
  return `${n < 0 ? "-" : ""}${s} ${units[i]}`;
}

export function rate(bps) {
  if (!bps) return "0 B/s";
  return bytes(bps) + "/s";
}

/** ago renders a unix timestamp as "just now", "5m ago", "3h ago", "2d ago". */
export function ago(ts) {
  if (!ts) return "never";
  const s = Math.max(0, Date.now() / 1000 - ts);
  if (s < 45) return "just now";
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 48 * 3600) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

/** since renders how long ago ts was, as a duration: "3h 12m". */
export function since(ts) {
  if (!ts) return "";
  const s = Math.max(0, Math.floor(Date.now() / 1000 - ts));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  return `${m}m`;
}

export function dateTime(ts) {
  if (!ts) return "";
  return new Date(ts * 1000).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function shortTime(ts, withDay) {
  const d = new Date(ts * 1000);
  if (withDay) return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

/** Latency tone: thresholds tuned for proxy real-delay numbers. */
export function latencyTone(ms) {
  if (ms === null || ms === undefined) return "neutral";
  if (ms < 400) return "success";
  if (ms < 1000) return "warning";
  return "destructive";
}

/** parseSize turns "10 GB" style input into bytes. */
export function toBytes(value, unit) {
  const n = Number(value);
  if (!Number.isFinite(n) || n <= 0) return 0;
  const mult = { MB: 1024 ** 2, GB: 1024 ** 3, TB: 1024 ** 4 }[unit] || 1;
  return Math.round(n * mult);
}

export function fromBytes(b) {
  if (!b) return { value: "", unit: "GB" };
  if (b >= 1024 ** 4 && b % 1024 ** 4 === 0) return { value: String(b / 1024 ** 4), unit: "TB" };
  if (b >= 1024 ** 3) return { value: String(+(b / 1024 ** 3).toFixed(2)), unit: "GB" };
  return { value: String(+(b / 1024 ** 2).toFixed(1)), unit: "MB" };
}

export const PERIOD_LABEL = { daily: "per day", weekly: "per week", monthly: "per month", total: "in total" };
