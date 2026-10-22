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
import { finishJob, isCancelling, jobOf, outcomeOf, runPool, setItem, startJob } from "./jobs.ts";
import { inTurn } from "./removing.ts";

export interface StopRow extends StopTarget {
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
  // Git refusing, in its own words, to lose uncommitted work.
  keepsWork(err: unknown): boolean;
  errorText(err: unknown): string;
}

export type WorktreeOutcome = "archived" | "archiving" | "kept" | "failed" | "cancelled";

export interface StopResult {
  job: number;
  stopped: Set<string>;
  // By worktree (removalKey).
  worktrees: Record<string, WorktreeOutcome>;
  clean: boolean;
}

// A few at a time, so a long list is quick without every request landing on
// a box at once.
export const CONCURRENCY = 3;

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

export async function runStopJob(rows: StopRow[], archive: boolean, deps: StopDeps): Promise<StopResult> {
  const job = startJob({
    title: `Stopping ${plural(rows.length, "agent")}`,
    items: rows.map((r) => ({ key: r.key, group: r.box, label: r.label, aside: r.aside, message: "Waiting to stop" })),
    cancellable: true,
  });
  const stopped = new Set<string>();
  const willArchive = (r: StopRow) => archive && !!r.where && !r.where.worktree.main;
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
  if (rows.some(willArchive)) {
    const report = (w: WorktreeTarget, o: WorktreeOutcome, message: string) => {
      worktrees[w.key] = o;
      const state = o === "archived" || o === "archiving" ? "done" : o;
      for (const k of w.rows) if (stopped.has(k)) setItem(job, k, { state, message: `Stopped · ${message}` });
    };
    const candidates = rows.filter(willArchive);
    // The boxes' own listing, fresh, so an agent started a moment ago
    // counts. A box that doesn't answer keeps its worktrees.
    const boxes = [...new Set(candidates.map((r) => r.box))];
    const listed = await Promise.all(boxes.map(async (box) => ({ box, agents: await deps.liveAgents(box).catch(() => undefined) })));
    const live = listed.flatMap(({ box, agents }) => (agents ?? []).map((a) => ({ box, ...a })));
    const plan = planArchive(
      candidates,
      stopped,
      live,
      new Set(listed.filter((l) => !l.agents).map((l) => l.box)),
    );
    for (const k of plan.kept) report(k, "kept", `Kept: ${k.why}`);
    for (const w of plan.archive) for (const k of w.rows) if (stopped.has(k)) setItem(job, k, { state: "running", message: "Stopped · waiting to archive" });
    await Promise.all(
      plan.archive.map((w) =>
        inTurn(w.box, async () => {
          if (isCancelling(job)) return report(w, "cancelled", "Not archived");
          for (const k of w.rows) if (stopped.has(k)) setItem(job, k, { state: "running", message: "Archiving…" });
          try {
            const res = await deps.archive(w.box, w.location, w.wt);
            if (res.archive) report(w, "archiving", "Archiving: its archive script runs, then it goes");
            else report(w, "archived", "Archived; its branch is kept");
          } catch (err) {
            if (deps.keepsWork(err)) report(w, "kept", "Kept: uncommitted changes");
            else report(w, "failed", `Couldn't archive: ${deps.errorText(err)}`);
          }
        }),
      ),
    );
  }

  finishJob(job, stopSummary(rows.length, stopped.size, unsent.length, Object.values(worktrees)));
  const done = jobOf(job);
  return { job, stopped, worktrees, clean: !!done && outcomeOf(done) === "clean" };
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
