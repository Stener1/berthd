import { toastError } from "@/components/error-note";
import { toastManager } from "@/components/ui/toast";
import type { Client } from "@/lib/api";
import { plainError } from "@/lib/errors";
import { errorMessage } from "@/lib/format";
import { finishJob, setItem, startJob } from "@/lib/jobs";
import { keptReason } from "@/lib/kept-reason";
import { unpinWorktree } from "@/lib/pinned-worktrees";
import { archivedMessage, clearRemoving, inTurn, KEPT_DIRTY, markRemoving, markScript, pruneRemovals, type RemovalKind, removalOf, removeResult, useRemovals } from "@/lib/removing";
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

// queueRemoval removes or archives a worktree in the background: it is
// marked "Removing…" or "Archiving…" at once, so whatever asked (archive's
// dialog) can close, and goes when its box gets to it, one at a time per
// box (inTurn). Its branch stays, and git still refuses to lose uncommitted
// work: then a toast says so, and a removal's offers to remove it anyway.
// An archive is a one-row job in the background-jobs tray (lib/jobs.ts)
// too, so a slow one can be watched.
export function queueRemoval(box: string, location: string, wt: Leaving, opts: { kind?: RemovalKind; force?: boolean } = {}) {
  const kind = opts.kind ?? "remove";
  if (removalOf(useRemovals.getState().byKey, box, wt.path)) return;
  markRemoving(box, wt.path, kind, wt.name);
  const job = kind === "archive" ? archiveJob(box, wt) : undefined;
  void inTurn(box, () => send(box, location, wt, kind, opts.force, job));
}

const KEY = "worktree";

function archiveJob(box: string, wt: Leaving) {
  const id = startJob({ title: `Archiving ${wt.name}`, items: [{ key: KEY, group: box, label: wt.name, aside: wt.branch, message: `Waiting for ${box}` }] });
  const end = (item: Parameters<typeof setItem>[2], summary: string) => {
    setItem(id, KEY, item);
    finishJob(id, summary);
  };
  return {
    sent: () => setItem(id, KEY, { state: "running", message: "Archiving…" }),
    done: (res: { archive?: string }) => end({ state: "done", message: archivedMessage(res) }, `${res.archive ? "Archiving" : "Archived"} ${wt.name}`),
    kept: () => end({ state: "kept", message: KEPT_DIRTY }, `Kept ${wt.name}`),
    failed: (err: unknown) => end({ state: "failed", message: `Couldn't archive: ${plainError(err)}` }, `Couldn't archive ${wt.name}`),
  };
}

type Leaving = { name: string; path: string; branch?: string };

async function send(box: string, location: string, wt: Leaving, kind: RemovalKind, force?: boolean, job?: ReturnType<typeof archiveJob>) {
  try {
    const client = useStore.getState().client;
    if (!client) throw new Error("not connected");
    job?.sent();
    const res = await removeWorktreeOnBox(client, box, location, wt, kind, { force });
    job?.done(res);
    if (kind === "remove") return;
    if (res.archive) toastManager.add({ title: `Archiving ${wt.name}`, description: "Its archive script is running on the box; the worktree goes when it finishes.", type: "info" });
    else toastManager.add({ title: `Archived ${wt.name}`, description: wt.branch ? `Branch ${wt.branch} is kept.` : box, type: "success" });
  } catch (err) {
    clearRemoving(box, wt.path);
    const why = force ? null : keptReason(errorMessage(err));
    if (why) {
      job?.kept();
      if (kind === "archive") {
        toastManager.add({
          type: "warning",
          title: `Couldn't archive ${wt.name}`,
          description: `${wt.name} has uncommitted changes, so it was left as it is. Commit them first, or use Remove worktree to discard them.`,
        });
      } else {
        toastManager.add({
          type: "warning",
          title: `Kept ${wt.name}`,
          description: why,
          actionProps: { children: "Remove anyway", onClick: () => queueRemoval(box, location, wt, { force: true }) },
        });
      }
    } else {
      job?.failed(err);
      toastError(err, { title: `Couldn't ${kind} ${wt.name}`, box });
    }
  }
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
