// Back and forward through the places you've been, as in a browser: the
// pure part (lib/nav-history.ts follows the app and goes there). A place is
// a view, and in the workspace the worktree in front (none is Home); its tab
// showing, or the section a page is on, goes with it. No imports: pnpm test
// runs this file as it is.

export interface Place {
  // What tells two places apart: the view, and in the workspace the
  // worktree in front.
  id: string;
  // The worktree it shows, by its workspace key (a box's home included).
  ws?: string;
  // What can change about a place without making it another: the tab
  // showing there, a page's section or what it was asked to open. Changing
  // it changes the place you are on rather than adding one, so Back never
  // walks through every tab.
  detail?: string;
}

export interface Trail<P extends Place = Place> {
  items: P[];
  // Where you are in items; -1 before the first place.
  at: number;
}

// How many places the trail keeps; the oldest go first.
export const CAP = 50;

export const EMPTY: Trail<never> = { items: [], at: -1 };

// visit notes p as where you are now. The same place as now only takes its
// detail; a new one goes after it, and what was ahead (after a Back) is
// dropped, as a browser does.
export function visit<P extends Place>(t: Trail<P>, p: P, cap = CAP): Trail<P> {
  const now = t.items[t.at];
  if (now?.id === p.id) return now.detail === p.detail ? t : { items: t.items.map((x, i) => (i === t.at ? p : x)), at: t.at };
  const items = [...t.items.slice(0, t.at + 1), p];
  const drop = Math.max(0, items.length - cap);
  return { items: drop ? items.slice(drop) : items, at: items.length - 1 - drop };
}

// step is the place back (-1) or forward (1) from where you are, and the
// trail moved there. It passes over places that are gone (exists says
// which: a worktree removed since) and over the place you are on already
// (it shows twice when what was between went). Undefined when there is
// nowhere to go.
export function step<P extends Place>(t: Trail<P>, dir: -1 | 1, exists: (p: P) => boolean): { trail: Trail<P>; place: P } | undefined {
  const here = t.items[t.at]?.id;
  for (let i = t.at + dir; i >= 0 && i < t.items.length; i += dir) {
    const p = t.items[i];
    if (p.id !== here && exists(p)) return { trail: { items: t.items, at: i }, place: p };
  }
  return undefined;
}
