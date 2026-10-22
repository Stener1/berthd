// The board's bulk Stop as a background job (lib/jobs.ts): it stops the
// agents a few at a time, then, when asked, archives their worktrees where
// it's safe (lib/archive-after-stop.ts), one at a time on each box in the
// same line as every other removal there (inTurn), the boxes side by side.
// "Stop the rest" sends nothing more: rows not yet stopped say "Not
// stopped", worktrees not yet archived are left as they are.
//
// No imports from "@/": pnpm test runs this file in plain node. The calls to
// the boxes come in as deps (views/dashboard/stop.tsx).

import { planArchive, type StopTarget, type WorktreeTarget } from "./archive-after-stop.ts";
import { plural } from "./format.ts";
import { finishJob, type JobItem, isCancelling, jobOf, outcomeOf, runPool, setItem, startJob } from "./jobs.ts";
import { archivedMessage, clearRemoving, inTurn, KEPT_DIRTY, keepsWork, markRemoving, removalOf, useRemovals } from "./removing.ts";

export interface StopItem extends StopTarget {
  session: string;
  // What the tray calls it: its place, and its name when the place is shared.
  label: string;
  aside?: string;
}

export interface StopDeps {
  stop(box: string, session: string): Promise<void>;
  // The agent sessions still live on a box, fresh, or undefined when it
  // doesn't answer.
  liveAgents(box: string): Promise<{ dir: string; name: string }[] | undefined>;
  archive(box: string, location: string, wt: { name: string; path: string }): Promise<{ archive?: string }>;
  errorText(err: unknown): string;
}

export type WorktreeOutcome = "archived" | "archiving" | "kept" | "failed" | "cancelled";

export interface StopResult {
  job: number;
  // By worktree (removalKey).
  worktrees: Record<string, WorktreeOutcome>;
  clean: boolean;
}

// A few at a time, so a long list is quick without every request landing on
// a box at once.
const CONCURRENCY = 3;

export async function runStopJob(rows: StopItem[], archive: boolean, deps: StopDeps): Promise<StopResult> {
  const job = startJob({
    title: `Stopping ${plural(rows.length, "agent")}`,
    items: rows.map((r) => ({ key: r.key, group: r.box, label: r.label, aside: r.aside, message: "Waiting to stop" })),
    cancellable: true,
  });
  const stopped = new Set<string>();
  const willArchive = (r: StopItem) => archive && !!r.where && !r.where.worktree.main;
  const unsent = await runPool(
    rows,
    CONCURRENCY,
    async (r) => {
      setItem(job, r.key, { state: "running", message: "Stopping…" });
      try {
        await deps.stop(r.box, r.session);
        stopped.add(r.key);
        setItem(job, r.key, willArchive(r) ? { state: "running", message: "Stopped" } : { state: "done", message: "Stopped" });
      } catch (err) {
        setItem(job, r.key, { state: "failed", message: `Failed: ${deps.errorText(err)}` });
      }
    },
    () => isCancelling(job),
  );
  for (const r of unsent) setItem(job, r.key, { state: "cancelled", message: "Not stopped" });

  const worktrees: Record<string, WorktreeOutcome> = {};
  const candidates = rows.filter(willArchive);
  if (candidates.length) {
    // Only a worktree's stopped rows: one kept because a stop failed there
    // also has rows that say Failed.
    const setRows = (w: WorktreeTarget, item: Pick<JobItem, "state" | "message">) => {
      for (const k of w.rows) if (stopped.has(k)) setItem(job, k, item);
    };
    const report = (w: WorktreeTarget, o: WorktreeOutcome, message: string) => {
      worktrees[w.key] = o;
      setRows(w, { state: o === "archived" || o === "archiving" ? "done" : o, message: `Stopped · ${message}` });
    };
    // planArchive over the box's own listing, fresh, so an agent started a
    // moment ago counts. A box that doesn't answer keeps its worktrees.
    const check = async (targets: StopItem[], box?: string) => {
      const boxes = box ? [box] : [...new Set(targets.map((r) => r.box))];
      const listed = await Promise.all(boxes.map(async (b) => ({ box: b, agents: await deps.liveAgents(b).catch(() => undefined) })));
      const live = listed.flatMap(({ box: b, agents }) => (agents ?? []).map((a) => ({ box: b, ...a })));
      return planArchive(targets, stopped, live, new Set(listed.filter((l) => !l.agents).map((l) => l.box)));
    };
    const plan = await check(candidates);
    for (const k of plan.kept) report(k, "kept", `Kept: ${k.why}`);
    const queued = plan.archive.filter((w) => {
      // Already on its way out (Archive… from the sidebar): its box needn't
      // hear it twice.
      if (removalOf(useRemovals.getState().byKey, w.box, w.wt.path)) {
        report(w, "archiving", "Already being archived");
        return false;
      }
      // Marked from now, so nothing else asks for it while it waits its turn.
      markRemoving(w.box, w.wt.path, "archive", w.wt.name);
      // Queued: not sent, so Stop the rest can still hold it back.
      setRows(w, { state: "queued", message: "Stopped · waiting to archive" });
      return true;
    });
    await Promise.all(
      queued.map((w) =>
        inTurn(w.box, async () => {
          const leave = (o: WorktreeOutcome, message: string) => {
            clearRemoving(w.box, w.wt.path);
            report(w, o, message);
          };
          if (isCancelling(job)) return leave("cancelled", "Not archived");
          setRows(w, { state: "running", message: "Archiving…" });
          // Its turn may come minutes later: look again for an agent
          // started there meanwhile.
          const again = await check(
            candidates.filter((r) => w.rows.includes(r.key)),
            w.box,
          );
          if (again.kept.length) return leave("kept", `Kept: ${again.kept[0].why}`);
          try {
            const res = await deps.archive(w.box, w.location, w.wt);
            report(w, res.archive ? "archiving" : "archived", archivedMessage(res));
          } catch (err) {
            if (keepsWork(err)) leave("kept", KEPT_DIRTY);
            else leave("failed", `Couldn't archive: ${deps.errorText(err)}`);
          }
        }),
      ),
    );
  }

  finishJob(job, stopSummary(rows.length, stopped.size, unsent.length, Object.values(worktrees)));
  const done = jobOf(job);
  return { job, worktrees, clean: !!done && outcomeOf(done) === "clean" };
}

// stopSummary is what a finished job says: "17 stopped, 15 archived", and
// what was kept, failed or never sent.
export function stopSummary(total: number, stopped: number, unsent: number, worktrees: WorktreeOutcome[]) {
  const count = (o: WorktreeOutcome) => worktrees.filter((w) => w === o).length;
  const failed = total - stopped - unsent + count("failed");
  return [
    `${stopped} stopped`,
    count("archived") && `${count("archived")} archived`,
    count("archiving") && `${count("archiving")} archiving`,
    count("kept") && `${count("kept")} kept`,
    failed && `${failed} failed`,
    unsent && `${unsent} not stopped`,
    count("cancelled") && `${count("cancelled")} not archived`,
  ]
    .filter(Boolean)
    .join(", ");
}
