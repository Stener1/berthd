import { create } from "zustand";

import { EMPTY, type Place, step, type Trail, visit } from "@/lib/nav-history-core";
import { useStore, type View } from "@/lib/store";
import { activateTab, goHome, homeBox, selectWorktree, showHome, useWorkspaces, type Workspace } from "@/lib/workspaces";

// Back and forward through the places you've been (⌘[ and ⌘], the window
// strip's arrows, a mouse's back and forward buttons), as in a browser. The
// trail follows the two stores that say where you are, the view and the
// worktree in front, so nothing that moves you has to tell it; the pure part
// is lib/nav-history-core.ts. It lasts until the window closes.

export interface Spot extends Place {
  view: View;
  tab?: string;
}

export const useNavHistory = create<Trail<Spot>>()(() => EMPTY);

// spotOf is where you are: the view, and in the workspace the worktree in
// front and its tab showing. A page is one place whatever its section, or
// what it was asked to open (Automations a flow, Review a run, which it
// takes and then forgets); a project's settings, a plugin's screen and a
// team's setup are one place each.
function spotOf(view: View, current: string | undefined, spaces: Record<string, Workspace>): Spot {
  if (view.kind === "workspace") {
    const tab = current ? spaces[current]?.active : undefined;
    return { id: `workspace:${current ?? ""}`, view, ws: current, tab, detail: tab };
  }
  const which = view.kind === "project" ? `${view.box}:${view.location}` : view.kind === "plugin" ? view.screen : view.kind === "team" ? (view.org ?? "") : "";
  return { id: `${view.kind}:${which}`, view, detail: JSON.stringify(view) };
}

// A place is gone when its worktree is: a box's home never is.
const existsIn = (spaces: Record<string, Workspace>) => (p: Spot) => !p.ws || !!homeBox(p.ws) || !!spaces[p.ws];

// Set while going back or forward, so the trail doesn't note each step of
// the way as a place of its own.
let restoring = false;

function note() {
  if (restoring) return;
  const t = useNavHistory.getState();
  const next = visit(t, spotOf(useStore.getState().view, useWorkspaces.getState().current, useWorkspaces.getState().spaces));
  if (next !== t) useNavHistory.setState(next, true);
}

// goTo puts you on a place from the trail, and the trail there.
function goTo(trail: Trail<Spot>, p: Spot) {
  useNavHistory.setState(trail, true);
  restoring = true;
  try {
    if (p.view.kind !== "workspace") useStore.getState().setView(p.view);
    else if (!p.ws) goHome();
    else {
      const ws = useWorkspaces.getState().spaces[p.ws];
      if (homeBox(p.ws)) showHome(p.ws);
      else if (ws) selectWorktree(ws.ref);
      if (ws && p.tab && ws.tabs.some((t) => t.id === p.tab)) activateTab(p.ws, p.tab);
    }
  } finally {
    restoring = false;
  }
  // What it landed on (a tab closed since shows another).
  note();
}

// go goes back (-1) or forward (1), and says whether there was anywhere to go.
export function go(dir: -1 | 1): boolean {
  const to = step(useNavHistory.getState(), dir, existsIn(useWorkspaces.getState().spaces));
  if (to) goTo(to.trail, to.place);
  return !!to;
}

// useCanGo says whether Back (-1) or Forward (1) has somewhere to go.
export function useCanGo(dir: -1 | 1): boolean {
  const trail = useNavHistory();
  // The answer, not the spaces: those change with every pane resized.
  return useWorkspaces((s) => !!step(trail, dir, existsIn(s.spaces)));
}

useStore.subscribe((s, prev) => {
  if (s.view !== prev.view) note();
});

useWorkspaces.subscribe((s, prev) => {
  // Only the worktree in front and its tab showing are where you are.
  if (s.current === prev.current && s.spaces[s.current ?? ""]?.active === prev.spaces[prev.current ?? ""]?.active) return;
  // The worktree in front went (archived or removed: only forgetWorktree
  // drops a workspace) and left you on Home: back to the last place that is
  // still there instead. After the store's other listeners, which still see
  // it go.
  const gone = prev.current && !s.current && !s.spaces[prev.current];
  if (gone && useStore.getState().view.kind === "workspace") {
    queueMicrotask(() => {
      const to = step(useNavHistory.getState(), -1, existsIn(useWorkspaces.getState().spaces));
      // Unless something else moved you meanwhile.
      if (to && !useWorkspaces.getState().current && useStore.getState().view.kind === "workspace") goTo(to.trail, to.place);
      else note();
    });
    return;
  }
  note();
});

// Where the window opens is the first place.
note();
