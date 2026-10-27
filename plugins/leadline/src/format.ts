// How Leadline's numbers are written. Durations arrive in milliseconds,
// times as unix milliseconds, shares as 0 to 1.

// ms writes a duration: 412µs, 3.21ms, 12.3ms, 840ms, 1.24s, 2.5m.
export function ms(n: number): string {
  if (!Number.isFinite(n)) return "–";
  if (n < 1) return `${Math.round(n * 1000)}µs`;
  if (n < 10) return `${n.toFixed(2)}ms`;
  if (n < 100) return `${n.toFixed(1)}ms`;
  if (n < 1000) return `${Math.round(n)}ms`;
  if (n < 60_000) return `${(n / 1000).toFixed(2)}s`;
  return `${(n / 60_000).toFixed(1)}m`;
}

// count shortens a count: 950, 1.2k, 34k, 3.4M.
export function count(n: number): string {
  if (!Number.isFinite(n)) return "–";
  if (n < 1000) return String(Math.round(n));
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`;
  return `${(n / 1_000_000).toFixed(1)}M`;
}

// per writes an average count, such as queries per request: 0.04, 1, 13.7, 240.
export function per(n: number): string {
  if (!Number.isFinite(n)) return "–";
  if (n >= 100) return count(n);
  if (n >= 10) return n.toFixed(1).replace(/\.0$/, "");
  return String(Number(n.toFixed(n < 1 ? 2 : 1)));
}

// pct writes a share between 0 and 1: 0%, <0.1%, 4.2%, 48%.
export function pct(share: number): string {
  if (!Number.isFinite(share)) return "–";
  const p = share * 100;
  if (p === 0) return "0%";
  if (p < 0.1) return "<0.1%";
  return p < 10 ? `${p.toFixed(1)}%` : `${Math.round(p)}%`;
}

const two = (n: number, w = 2) => String(n).padStart(w, "0");

// clock writes a time as local HH:MM:SS.mmm, the way a log line starts.
export function clock(at: number): string {
  const d = new Date(at);
  return `${two(d.getHours())}:${two(d.getMinutes())}:${two(d.getSeconds())}.${two(d.getMilliseconds(), 3)}`;
}

// tick labels a bucket of a chart: HH:MM, with seconds when buckets are
// shorter than a minute.
export function tick(at: number, bucketMs: number): string {
  const d = new Date(at);
  return `${two(d.getHours())}:${two(d.getMinutes())}${bucketMs < 60_000 ? `:${two(d.getSeconds())}` : ""}`;
}

// ago writes how long ago something happened, short enough for a column:
// now, 8s ago, 12m ago, 3h ago, 2d ago.
export function ago(msAgo: number): string {
  if (!Number.isFinite(msAgo)) return "–";
  const s = Math.max(0, msAgo) / 1000;
  if (s < 2) return "now";
  if (s < 60) return `${Math.floor(s)}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86_400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86_400)}d ago`;
}

export const plural = (n: number, one: string, many = `${one}s`) => `${count(n)} ${n === 1 ? one : many}`;

// span writes a length of time in words, to its largest unit: 40 seconds,
// 15 minutes, 3 hours, 3 days.
export function span(n: number): string {
  if (!Number.isFinite(n)) return "–";
  const s = Math.max(0, n) / 1000;
  if (s < 60) return plural(Math.floor(s), "second");
  if (s < 3600) return plural(Math.floor(s / 60), "minute");
  if (s < 86_400) return plural(Math.floor(s / 3600), "hour");
  return plural(Math.floor(s / 86_400), "day");
}

// bytes writes a size: 0 B, 812 B, 1.6 MB, 2 GB.
export function bytes(n: number): string {
  if (!Number.isFinite(n)) return "–";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = Math.max(0, n);
  let u = 0;
  while (v >= 1000 && u < units.length - 1) {
    v /= 1024;
    u++;
  }
  return `${u === 0 || v >= 100 ? Math.round(v) : v.toFixed(1).replace(/\.0$/, "")} ${units[u]}`;
}

// serviceNames names worktrees' services in a sentence: "web in
// acme/fix-orders" (the project alone for its main checkout), "a, b and c".
export function serviceNames(list: { location: string; worktree: string; service: string }[]): string {
  const names = list.map((s) => `${s.service} in ${!s.worktree || s.worktree === s.location ? s.location : `${s.location}/${s.worktree}`}`);
  return names.length < 2 ? names.join("") : `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

// sentence makes what a program said read as one: a capital, and a full stop.
export function sentence(text: string): string {
  const t = text.trim();
  if (!t) return t;
  const first = t.charAt(0).toUpperCase() + t.slice(1);
  return /[.!?…]$/.test(first) ? first : `${first}.`;
}
