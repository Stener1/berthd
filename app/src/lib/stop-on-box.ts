import { boxOffline } from "@/lib/queue";
import { boxHasRuns, runs, scheduleRuns } from "@/lib/runs";
import { followStopRun, type StopRead, type StopRun, type StopRunParams, type StopRuns } from "@/lib/stop-job";

// The boxes' stop runs (lib/stop-job.ts): what the board's bulk Stop, the
// Worktrees table's Stop sessions and Archive, and the sidebar's Stop
// everything ask a box with runs to do, so it carries on while the laptop
// sleeps.
export const stopRuns: StopRuns = {
  onBox: (box) => boxHasRuns(box) && !boxOffline(box),
  start: async (box, req) => {
    const run = await runs.start(box, { template: "stop", title: req.title, group: req.group, params: { ...req.params } });
    scheduleRuns(box, 0);
    return run;
  },
  get: (box, id) => runs.get(box, id) as Promise<StopRun>,
  cancel: (box, id) => runs.cancel(box, id),
};

// A run's map takes at most 64 items (internal/box/runs/exec.go).
const fits = (p: StopRunParams) => p.sessions.length <= 64 && p.worktrees.length <= 64;

// startStopOnBox asks box to stop sessions and archive worktrees as one
// run, and answers with its id, or undefined when the box can't run it (no
// runs, offline, or from before stop runs): the caller does it itself.
export async function startStopOnBox(box: string, title: string, params: StopRunParams): Promise<string | undefined> {
  if (!stopRuns.onBox(box) || !fits(params)) return undefined;
  try {
    return (await stopRuns.start(box, { title, group: `stop-${Date.now().toString(36)}`, params })).id;
  } catch {
    return undefined;
  }
}

// stopOnBox is startStopOnBox, then the run followed to its end: its last
// read, or undefined when the box didn't take it.
export async function stopOnBox(box: string, title: string, params: StopRunParams, o: { onRead?(read: StopRead): void; cancelled?(): boolean } = {}): Promise<StopRead | undefined> {
  const id = await startStopOnBox(box, title, params);
  return id ? followStopRun(stopRuns, box, id, { onRead: (r) => o.onRead?.(r), cancelled: o.cancelled }) : undefined;
}
