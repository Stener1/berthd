// Lists ordered by time (traces, logs) are read a page at a time and read
// again while they are on screen. Kept apart from the hooks so it can be
// tested.

// merge puts a newly read first page in front of the rows already loaded:
// rows that arrived since go on top, older pages stay, and rows that have
// left the window go. A first page that shares nothing with what was loaded
// (more arrived than a page holds) starts the list again.
export function merge<R>(loaded: R[], fresh: R[], id: (r: R) => string, time: (r: R) => number, sinceMs: number): R[] {
  if (!loaded.length) return fresh;
  const known = new Set(loaded.map(id));
  const at = fresh.findIndex((r) => known.has(id(r)));
  if (at < 0) return fresh.length ? fresh : loaded.filter((r) => time(r) >= sinceMs);
  const seen = new Set(fresh.map(id));
  return [...fresh, ...loaded.filter((r) => !seen.has(id(r)) && time(r) >= sinceMs && time(r) <= time(fresh[fresh.length - 1]))];
}
