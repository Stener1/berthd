import type { BerthEvent } from "@/lib/api";
import { agentOf } from "@/lib/derive";
import { type Leaf, leaves, placeAgentBrowser } from "@/lib/layout";
import { usePrefs } from "@/lib/prefs";
import { useStore } from "@/lib/store";
import { focusPane, setPaneContent, splitPane, useWorkspaces, wsKey } from "@/lib/workspaces";

// An agent on a box opens its own browser (berthd browser open) to check its
// work. When the person is looking at that agent, the browser's live view
// opens beside it, so they see the chat and the page side by side: in the
// worktree's browser pane in that tab if there is one, else split to the
// right of the agent's pane. The keyboard stays where it was. Elsewhere,
// nothing opens: an agent never pulls the person away, and the pane's
// "Agent's view · live" chip is there when they come back.

export function handleBrowserOpened(e: BerthEvent) {
  const box = e.box;
  const path = e.data?.path as string | undefined;
  if (!box || !path || !usePrefs.getState().agentBrowserBeside) return;
  if (useStore.getState().view.kind !== "workspace") return;
  const { current, spaces } = useWorkspaces.getState();
  const ws = current ? spaces[current] : undefined;
  const tab = ws?.tabs.find((t) => t.id === ws.active);
  // A Compare tab's sides are its own.
  if (!current || !tab || tab.compare) return;
  const sessions = useStore.getState().boxes[box]?.sessions ?? [];
  const isAgent = (l: Leaf) => {
    const c = l.content;
    if (c.kind !== "terminal" || c.box !== box) return false;
    const s = sessions.find((x) => x.name === c.session);
    return s ? !s.exited && s.dir === path && !!agentOf(s) : false;
  };
  const place = placeAgentBrowser(tab.root, tab.focus, current, wsKey(box, path), isAgent);
  if (!place) return;
  const watch = Date.now();
  if (place.kind === "watch") {
    const pane = leaves(tab.root).find((l) => l.id === place.pane);
    if (pane?.content.kind === "browser") setPaneContent(current, tab.id, pane.id, { ...pane.content, watch });
    return;
  }
  splitPane(current, tab.id, place.beside, "row", { kind: "browser", url: "", watch }, wsKey(box, path));
  // splitPane focuses the new pane; the person may be typing to the agent.
  focusPane(current, tab.id, tab.focus);
}
