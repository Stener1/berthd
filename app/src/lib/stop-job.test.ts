// node --experimental-strip-types --test src/lib/stop-job.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { KEPT_OTHER_AGENT } from "./archive-after-stop.ts";
import { cancelJob, jobOf, useJobs } from "./jobs.ts";
import { runStopJob, type StopDeps, type StopItem, stopSummary } from "./stop-job.ts";

const row = (session: string, wt?: string, box = "devl"): StopItem => ({
  key: `${box}/${session}`,
  box,
  session,
  label: wt ?? "shop",
  where: { location: "shop", worktree: wt ? { name: wt, path: `/w/${wt}` } : { name: "shop", path: "/w/shop", main: true } },
});

function fakeBoxes(over: Partial<StopDeps> = {}) {
  const calls: string[] = [];
  const deps: StopDeps = {
    stop: async (box, s) => void calls.push(`stop ${box}/${s}`),
    liveAgents: async () => [],
    archive: async (box, _loc, wt) => {
      calls.push(`archive ${box}:${wt.name}`);
      return {};
    },
    errorText: (err) => (err instanceof Error ? err.message : String(err)),
    ...over,
  };
  return { deps, calls };
}

const itemsOf = (id: number) => Object.fromEntries(jobOf(id)!.items.map((i) => [i.key, `${i.state}: ${i.message}`]));

test("a clean run stops them all, archives their worktrees, and sums it up", async () => {
  useJobs.setState({ jobs: [] });
  const { deps, calls } = fakeBoxes();
  const res = await runStopJob([row("a", "fix"), row("b", "perf"), row("c")], true, deps);
  assert.equal(res.clean, true);
  assert.equal(jobOf(res.job)!.title, "Stopping 3 agents");
  assert.equal(jobOf(res.job)!.summary, "3 stopped, 2 archived");
  assert.deepEqual(itemsOf(res.job), {
    "devl/a": "done: Stopped · Archived; its branch is kept",
    "devl/b": "done: Stopped · Archived; its branch is kept",
    "devl/c": "done: Stopped",
  });
  assert.deepEqual(calls.filter((c) => c.startsWith("archive")), ["archive devl:fix", "archive devl:perf"]);
});

test("without archiving, only the agents stop", async () => {
  const { deps, calls } = fakeBoxes();
  const res = await runStopJob([row("a", "fix")], false, deps);
  assert.equal(res.clean, true);
  assert.equal(jobOf(res.job)!.summary, "1 stopped");
  assert.deepEqual(calls, ["stop devl/a"]);
});

test("kept and failed rows make a report, each saying why", async () => {
  const { deps } = fakeBoxes({
    stop: async (_box, s) => {
      if (s === "d") throw new Error("box went away");
    },
    liveAgents: async () => [{ dir: "/w/busy", name: "someone-else" }],
    archive: async (_box, _loc, wt) => {
      if (wt.name === "dirty") throw new Error("contains uncommitted changes");
      if (wt.name === "broken") throw new Error("permission denied");
      return wt.name === "scripted" ? { archive: "pnpm archive" } : {};
    },
  });
  const res = await runStopJob([row("a", "dirty"), row("b", "busy"), row("c", "broken"), row("d", "gone"), row("e", "scripted")], true, deps);
  assert.equal(res.clean, false);
  assert.deepEqual(itemsOf(res.job), {
    "devl/a": "kept: Stopped · Kept: uncommitted changes",
    "devl/b": `kept: Stopped · Kept: ${KEPT_OTHER_AGENT}`,
    "devl/c": "failed: Stopped · Couldn't archive: permission denied",
    "devl/d": "failed: Failed: box went away",
    "devl/e": "done: Stopped · Archiving: its archive script runs, then it goes",
  });
  assert.equal(jobOf(res.job)!.summary, "4 stopped, 1 archiving, 2 kept, 2 failed");
});

test("Stop the rest sends nothing more; what was sent finishes", async () => {
  let release!: () => void;
  const hold = new Promise<void>((r) => (release = r));
  const sent: string[] = [];
  const { deps } = fakeBoxes({
    stop: async (_box, s) => {
      sent.push(s);
      await hold;
    },
  });
  const rows = ["a", "b", "c", "d", "e", "f"].map((s) => row(s, `wt-${s}`));
  const running = runStopJob(rows, true, deps);
  await new Promise((r) => setTimeout(r, 0));
  const id = useJobs.getState().jobs[0].id;
  assert.deepEqual(sent, ["a", "b", "c"], "three at a time");
  cancelJob(id);
  release();
  const res = await running;
  assert.deepEqual(sent, ["a", "b", "c"]);
  const items = itemsOf(res.job);
  for (const s of ["a", "b", "c"]) assert.equal(items[`devl/${s}`], "cancelled: Stopped · Not archived");
  for (const s of ["d", "e", "f"]) assert.equal(items[`devl/${s}`], "cancelled: Not stopped");
  assert.equal(jobOf(res.job)!.summary, "3 stopped, 3 not stopped, 3 not archived");
  assert.equal(res.clean, false);
});

test("the job carries on in the store with nothing holding it", async () => {
  useJobs.setState({ jobs: [] });
  const { deps } = fakeBoxes();
  // As the dialog does: start it and let go, with no view to keep it alive.
  void runStopJob([row("a", "fix")], true, deps);
  const id = useJobs.getState().jobs[0].id;
  for (let i = 0; i < 20 && !jobOf(id)!.finished; i++) await new Promise((r) => setTimeout(r, 0));
  assert.equal(jobOf(id)!.summary, "1 stopped, 1 archived");
});

test("the summary names what was kept, failed and never sent", () => {
  assert.equal(stopSummary(17, 17, 0, Array(15).fill("archived")), "17 stopped, 15 archived");
  assert.equal(stopSummary(5, 3, 1, ["archived", "kept", "failed"]), "3 stopped, 1 archived, 1 kept, 2 failed, 1 not stopped");
});
