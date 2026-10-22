// node --experimental-strip-types --test src/lib/archive-after-stop.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { KEPT_OTHER_AGENT, KEPT_STOP_FAILED, KEPT_UNCHECKED, planArchive, type StopTarget } from "./archive-after-stop.ts";

const at = (session: string, wt: string, main = false): StopTarget => ({ key: `devl/${session}`, box: "devl", where: { location: "shop", worktree: { name: wt, path: `/w/${wt}`, main } } });
const names = (ws: { wt: { name: string } }[]) => ws.map((w) => w.wt.name);

test("each worktree is archived once, however many stopped agents worked there", () => {
  const targets = [at("a", "fix"), at("b", "fix"), at("c", "perf")];
  const plan = planArchive(targets, new Set(targets.map((t) => t.key)), []);
  assert.deepEqual(names(plan.archive), ["fix", "perf"]);
  assert.deepEqual(plan.archive[0].rows, ["devl/a", "devl/b"]);
  assert.deepEqual(plan.kept, []);
});

test("the main checkout and an agent with no worktree are never candidates", () => {
  const loose: StopTarget = { key: "devl/x", box: "devl" };
  const targets = [at("a", "shop", true), loose];
  const plan = planArchive(targets, new Set(targets.map((t) => t.key)), []);
  assert.deepEqual(plan, { archive: [], kept: [] });
});

test("another live agent outside the batch keeps the worktree; the batch's own don't", () => {
  const targets = [at("a", "fix"), at("b", "perf")];
  const live = [
    { box: "devl", dir: "/w/fix/app", name: "other" },
    { box: "devl", dir: "/w/perf-old", name: "beside" },
    { box: "devl", dir: "/w/perf", name: "b" },
    { box: "gpu", dir: "/w/perf", name: "elsewhere" },
  ];
  const plan = planArchive(targets, new Set(targets.map((t) => t.key)), live);
  assert.deepEqual(names(plan.archive), ["perf"]);
  assert.deepEqual(
    plan.kept.map((k) => [k.wt.name, k.why]),
    [["fix", KEPT_OTHER_AGENT]],
  );
});

test("a worktree whose stops all failed is left out; one where some failed is kept", () => {
  const targets = [at("a", "fix"), at("b", "perf"), at("c", "perf")];
  const plan = planArchive(targets, new Set(["devl/b"]), []);
  assert.deepEqual(plan.archive, []);
  assert.deepEqual(
    plan.kept.map((k) => [k.wt.name, k.why]),
    [["perf", KEPT_STOP_FAILED]],
  );
});

test("a box that didn't list its sessions keeps its worktrees", () => {
  const targets = [at("a", "fix"), { ...at("b", "perf"), key: "gpu/b", box: "gpu" }];
  const plan = planArchive(targets, new Set(targets.map((t) => t.key)), [], new Set(["devl"]));
  assert.deepEqual(names(plan.archive), ["perf"]);
  assert.deepEqual(
    plan.kept.map((k) => [k.wt.name, k.why]),
    [["fix", KEPT_UNCHECKED]],
  );
});
