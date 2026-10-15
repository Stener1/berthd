// What the Switchboard lists, and how its keys move through it: pure, so
// switchboard-list.test.ts can check it without a page.

export interface ListItem {
  id: string;
  title: string;
  place: string;
  area: string;
}

// matches is whether an item shows in an area for a query: every word of the
// query is somewhere in its title, place or area, in any order.
export function matches(item: ListItem, area: string, query: string): boolean {
  if (area !== "All" && item.area !== area) return false;
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  const text = `${item.title} ${item.place} ${item.area}`.toLowerCase();
  return words.every((w) => text.includes(w));
}

// boardLists splits what matches into Focus, in the order it was starred,
// and the most recent of the rest.
export function boardLists<T extends ListItem>(items: T[], focus: string[], area: string, query: string, limit = 24): { focused: T[]; recent: T[] } {
  const shown = items.filter((item) => matches(item, area, query));
  const byId = new Map(shown.map((item) => [item.id, item]));
  const focused = focus.map((id) => byId.get(id)).filter((item): item is T => !!item);
  const starred = new Set(focus);
  return { focused, recent: shown.filter((item) => !starred.has(item.id)).slice(0, limit) };
}

// stepId is the id ↑ or ↓ moves the highlight to, wrapping at the ends. A
// highlight that is gone (filtered away) starts again from the top.
export function stepId(ids: string[], current: string | undefined, delta: number): string | undefined {
  if (!ids.length) return undefined;
  const at = current === undefined ? -1 : ids.indexOf(current);
  if (at < 0) return delta < 0 ? ids[ids.length - 1] : ids[0];
  return ids[(((at + delta) % ids.length) + ids.length) % ids.length];
}

// cycle is the next (or previous) of a list, wrapping: Tab through areas.
export function cycle<T>(list: T[], current: T, delta: number): T {
  const at = list.indexOf(current);
  if (at < 0) return list[0];
  return list[(((at + delta) % list.length) + list.length) % list.length];
}

// shortAgo is how long ago, in a column's few characters: now, 4m, 2h, 3d.
export function shortAgo(at: number, now: number): string {
  if (!at) return "";
  const s = Math.max(0, (now - at) / 1000);
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  if (s < 86400 * 7) return `${Math.floor(s / 86400)}d`;
  return `${Math.floor(s / (86400 * 7))}w`;
}
