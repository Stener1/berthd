import type { ReactNode } from "react";

import { ErrorBoundary, type Rescue } from "@/components/error-boundary";
import { closePane, dropTab, resetTab } from "@/lib/actions";
import { findLeaf } from "@/lib/layout";
import { setPaneContent, useWorkspaces } from "@/lib/workspaces";

// A pane that throws as it draws breaks only itself: it says so in its own
// place, with a way out (its terminal in place of its chat, or closing it),
// and the sidebar, the strip and every other pane and tab carry on (#137).
// PaneLayer draws every pane of every open workspace, the tabs not showing
// too, so without this one bad pane anywhere took the whole window.
export function PaneGuard({ wsKey, tab, pane, children }: { wsKey: string; tab: string; pane: string; children: ReactNode }) {
  const compare = useWorkspaces((s) => !!s.spaces[wsKey]?.tabs.find((t) => t.id === tab)?.compare);
  return (
    <ErrorBoundary scope={compare ? "this side of the Compare tab" : "this pane"} rescues={() => paneRescues(wsKey, tab, pane)}>
      {children}
    </ErrorBoundary>
  );
}

// paneRescues are a broken pane's ways out, from what it holds now.
function paneRescues(key: string, tab: string, pane: string): Rescue[] {
  const t = useWorkspaces.getState().spaces[key]?.tabs.find((x) => x.id === tab);
  const l = t && findLeaf(t.root, pane);
  if (!t || !l) return [];
  const c = l.content;
  const out: Rescue[] = [];
  if (t.compare) {
    if (t.compare.lane !== "terminal") out.push({ label: "Show both terminals", run: () => resetTab(key, tab) });
    out.push({ label: "Close tab", run: () => dropTab(key, tab) });
    return out;
  }
  if (c.kind === "terminal" && c.view !== "terminal") out.push({ label: "Show its terminal", run: () => setPaneContent(key, tab, pane, { ...c, view: "terminal" }) });
  out.push({ label: t.root.kind === "leaf" ? "Close tab" : "Close pane", run: () => closePane(key, tab, pane) });
  return out;
}

// showingTabRescues are the ways out of the tab showing, for what guards
// more than one pane (the workspace, the whole window): it was most likely
// what broke.
export function showingTabRescues(): Rescue[] {
  const s = useWorkspaces.getState();
  const key = s.current;
  const ws = key ? s.spaces[key] : undefined;
  const t = ws?.tabs.find((x) => x.id === ws.active);
  if (!key || !t) return [];
  return [
    { label: "Reset this tab", run: () => resetTab(key, t.id) },
    { label: "Close this tab", run: () => dropTab(key, t.id) },
  ];
}

// ?mock&crash=<session> makes that session's chat throw as it draws, so the
// browser tests can check that a pane that breaks stays in its pane
// (e2e/restore-tabs.spec.ts). Never outside the mock.
const params = typeof location === "undefined" ? undefined : new URLSearchParams(location.search);
const crashing = params?.has("mock") ? params.get("crash") : null;

export function CrashProbe({ session }: { session: string }) {
  if (crashing && crashing === session) throw new Error(`${session}'s chat could not draw (a test crash)`);
  return null;
}
