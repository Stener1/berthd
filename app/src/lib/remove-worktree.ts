import { toastError } from "@/components/error-note";
import { toastManager } from "@/components/ui/toast";
import type { Client } from "@/lib/api";
import { errorMessage } from "@/lib/format";
import { unpinWorktree } from "@/lib/pinned-worktrees";
import { clearRemoving, markRemoving, markScript, pruneRemovals, type RemovalKind, removalOf, removeResult, useRemovals } from "@/lib/removing";
import { scheduleRefresh, useStore } from "@/lib/store";
import { forgetWorktree } from "@/lib/workspaces";
import { worktreesApi } from "@/lib/worktrees";

// removeWorktreeOnBox asks the box to remove (or archive) a worktree and
// follows it through. A plain removal is done when the answer comes; with an
// archive script the box answers at once and runs it in the background, so
// the worktree is marked (lib/removing.ts) until worktree.removed or
// worktree.archive.failed arrives (lib/events.ts calls worktreeGone or
// archiveFailed). Every entry point goes through here: the sidebar's ⋯ and
// right-click, zen, the launcher and the Worktrees table.
export async function removeWorktreeOnBox(
  client: Client,
  box: string,
  location: string,
  wt: { name: string; path: string },
  kind: RemovalKind,
  opts: { force?: boolean; branch?: boolean } = {},
): Promise<{ archive?: string }> {
  markRemoving(box, wt.path, kind, wt.name);
  let res: { archive?: string };
  try {
    res = removeResult(await worktreesApi.remove(client, box, location, wt.name, opts));
  } catch (err) {
    clearRemoving(box, wt.path);
    throw err;
  }
  if (res.archive) markScript(box, wt.path);
  else {
    clearRemoving(box, wt.path);
    forgetWorktree(box, wt.path);
    unpinWorktree(box, wt.path);
  }
  scheduleRefresh(box, ["locations", "sessions", "services"]);
  return res;
}

// Removals asked for and not yet sent, per box. Each box takes one at a
// time, so a slow teardown never has others half done beside it.
const waiting = new Map<string, { location: string; wt: { name: string; path: string }; force?: boolean }[]>();

// queueRemoval removes a worktree without asking first: it is marked
// "Removing…" at once and goes when its box gets to it. Its branch stays,
// and git still refuses to lose uncommitted work: then a toast offers to
// remove it anyway.
export function queueRemoval(box: string, location: string, wt: { name: string; path: string }, opts: { force?: boolean } = {}) {
  if (removalOf(useRemovals.getState().byKey, box, wt.path)) return;
  markRemoving(box, wt.path, "remove", wt.name);
  const list = waiting.get(box);
  if (list) {
    list.push({ location, wt, force: opts.force });
    return;
  }
  waiting.set(box, [{ location, wt, force: opts.force }]);
  void drain(box);
}

async function drain(box: string) {
  const list = waiting.get(box)!;
  while (list.length) {
    const { location, wt, force } = list[0];
    try {
      const client = useStore.getState().client;
      if (!client) throw new Error("not connected");
      await removeWorktreeOnBox(client, box, location, wt, "remove", { force });
    } catch (err) {
      clearRemoving(box, wt.path);
      // git's own words for work it would lose.
      const msg = errorMessage(err);
      if (!force && /modified|untracked|uncommitted|contains/i.test(msg)) {
        toastManager.add({
          type: "warning",
          title: `Kept ${wt.name}`,
          description: /submodule commits/i.test(msg)
            ? "A submodule in it has commits no remote has, so it was left as it is."
            : "It has uncommitted changes, so git left it as it is.",
          actionProps: { children: "Remove anyway", onClick: () => queueRemoval(box, location, wt, { force: true }) },
        });
      } else toastError(err, { title: `Couldn't remove ${wt.name}`, box });
    }
    list.shift();
  }
  waiting.delete(box);
}

// worktreeGone is the box saying a worktree went: its workspace (tabs,
// panes) and its pin go too, and an archive the app was waiting on says it
// is done. A failed archive keeps both (archiveFailed).
export function worktreeGone(box: string, path: string) {
  const was = clearRemoving(box, path);
  forgetWorktree(box, path);
  unpinWorktree(box, path);
  if (was?.script) toastManager.add({ type: "success", title: `${was.kind === "archive" ? "Archived" : "Removed"} ${was.name ?? "the worktree"}`, description: box });
}

// archiveFailed puts the worktree back as it was; the failure itself is a
// notification of its own (lib/events.ts), with the script's output.
export function archiveFailed(box: string, path: string) {
  clearRemoving(box, path);
}

// A mark for a worktree a box no longer lists is dropped, in case its event
// was missed (the app was closed, the stream dropped).
useStore.subscribe((s, prev) => {
  if (s.boxes === prev.boxes || !Object.keys(useRemovals.getState().byKey).length) return;
  for (const [name, data] of Object.entries(s.boxes)) {
    if (!data?.locations || data.locations === prev.boxes[name]?.locations) continue;
    pruneRemovals(name, new Set(data.locations.flatMap((l) => (l.worktrees ?? []).map((w) => w.path))));
  }
});
