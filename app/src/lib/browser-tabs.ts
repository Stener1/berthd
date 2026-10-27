// A worktree's agent browser has named tabs on a box that says
// "browser.tabs": each agent passes `berthd browser --tab NAME` and gets a
// page of its own, so a dozen teammates of one lead don't drive one page.
// The pane watches one tab at a time, and which one is the person's to
// choose: what agents do in the others never moves it. This file is the
// pure part of that, kept free of the app's store so tests run it in Node.

import type { Viewport } from "@/lib/agent-browser";

export interface AgentBrowserTab {
  // "" is the main tab, which agents that pass no --tab share.
  name: string;
  url?: string;
  title?: string;
  // RFC3339, by the box's clock.
  last_used: string;
  // How long since it was used, as of the box's answer: unlike last_used,
  // this doesn't need the laptop's clock to agree with the box's.
  idle_ms?: number;
  watchers: number;
  viewport: Viewport;
  // As the box says it: "1920×1080", "390×844 @3x".
  size: string;
  // An agent's command is running on it right now.
  busy?: boolean;
}

// How long after its last use a tab still shows as active.
export const ACTIVE_MS = 3000;

export const tabLabel = (name: string) => (name === "" ? "Main" : name);

// tabQuery is the query a per-worktree browser route takes for a tab: none
// for the main tab, so a box without tabs gets the same request as before.
export const tabQuery = (tab?: string) => (tab ? `?tab=${encodeURIComponent(tab)}` : "");

// showTabStrip is whether there is anything to choose between: more than
// one tab, or a named one (an agent on its own tab while main is unused).
export const showTabStrip = (tabs: AgentBrowserTab[] | undefined) => !!tabs && (tabs.length > 1 || tabs.some((t) => t.name !== ""));

// usedAt orders tabs by use: by idle_ms where the box says it, else by
// last_used.
const usedAt = (t: AgentBrowserTab) => {
  if (t.idle_ms !== undefined) return -t.idle_ms;
  const at = Date.parse(t.last_used);
  return Number.isNaN(at) ? Number.NEGATIVE_INFINITY : at;
};

// mostRecentTab is the tab used last; on a tie, the earlier in the box's
// order (main first, then by when they opened).
export function mostRecentTab(tabs: AgentBrowserTab[]): AgentBrowserTab | undefined {
  let best: AgentBrowserTab | undefined;
  for (const t of tabs) if (!best || usedAt(t) > usedAt(best)) best = t;
  return best;
}

export interface TabChoice {
  name: string;
  // Set when the watched tab is gone and this is the one the view moved to.
  movedFrom?: string;
}

// chooseTab is the tab to watch, given the box's list and the one watched
// now (undefined when the view has just opened). It only ever changes an
// existing choice when that tab is gone: then to the most recently used of
// the rest. No list (an old box, or not known yet) keeps it as it is.
export function chooseTab(tabs: AgentBrowserTab[] | undefined, watched: string | undefined): TabChoice | undefined {
  if (!tabs?.length) return watched === undefined ? undefined : { name: watched };
  if (watched !== undefined && tabs.some((t) => t.name === watched)) return { name: watched };
  const next = mostRecentTab(tabs)!;
  return watched === undefined ? { name: next.name } : { name: next.name, movedFrom: watched };
}

// tabMovedNote is the line the view shows after it moved off a gone tab.
// The main tab isn't closed when it drops out of the list, only unused
// while agents work on named ones, so it isn't said to have closed.
export function tabMovedNote(from: string, to: string): string {
  return from === "" ? `Watching ${tabLabel(to)}, the tab in use.` : `${tabLabel(from)} closed. Watching ${tabLabel(to)}.`;
}

// TabSeen is what the pane remembers of each tab between status polls, to
// see a tab being used without trusting the box's clock to match this one.
export type TabSeen = Record<string, { last_used: string; changedAt: number }>;

// noteTabs records, as of now, which tabs' last_used moved since the last
// poll.
export function noteTabs(seen: TabSeen, tabs: AgentBrowserTab[], now: number): TabSeen {
  const next: TabSeen = {};
  for (const t of tabs) {
    const was = seen[t.name];
    next[t.name] = was && was.last_used === t.last_used ? was : { last_used: t.last_used, changedAt: was ? now : 0 };
  }
  return next;
}

// tabActive is whether a tab shows activity: an agent's command runs on it,
// or it was used in the last few seconds, by its own time when the clocks
// agree and by when this app saw it change when they don't.
export function tabActive(t: AgentBrowserTab, seen: TabSeen, now: number): boolean {
  if (t.busy) return true;
  const changed = seen[t.name]?.changedAt ?? 0;
  if (changed && now - changed < ACTIVE_MS) return true;
  if (t.idle_ms !== undefined) return t.idle_ms < ACTIVE_MS;
  const at = Date.parse(t.last_used);
  const age = now - at;
  return !Number.isNaN(at) && age >= 0 && age < ACTIVE_MS;
}
