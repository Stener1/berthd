// node --experimental-strip-types --test src/lib/switchboard-list.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { boardLists, cycle, matches, shortAgo, stepId } from "./switchboard-list.ts";

const item = (id: string, title: string, area = "Work", place = "shop / main") => ({ id, title, place, area });
const items = [item("a", "Fix checkout webhook retries"), item("b", "Tune the judge prompt", "Personal", "evals / judge-v2"), item("c", "Plan the checkout release"), item("d", "Speed up product search")];

test("a query matches every word, in any order, across title, place and area", () => {
  assert.equal(matches(items[0], "All", "retries checkout"), true);
  assert.equal(matches(items[1], "All", "evals tune"), true);
  assert.equal(matches(items[1], "All", "personal"), true);
  assert.equal(matches(items[0], "All", "checkout refund"), false);
  assert.equal(matches(items[0], "All", "  "), true);
});

test("an area shows only its own work; All shows everything", () => {
  assert.equal(matches(items[1], "Work", ""), false);
  assert.equal(matches(items[1], "Personal", ""), true);
  assert.equal(matches(items[1], "All", ""), true);
});

test("Focus keeps the order work was starred in and leaves Recent", () => {
  const { focused, recent } = boardLists(items, ["c", "a"], "All", "");
  assert.deepEqual(focused.map((i) => i.id), ["c", "a"]);
  assert.deepEqual(recent.map((i) => i.id), ["b", "d"]);
});

test("Focus and Recent both follow the area and the query", () => {
  const { focused, recent } = boardLists(items, ["a", "b"], "Work", "checkout");
  assert.deepEqual(focused.map((i) => i.id), ["a"]);
  assert.deepEqual(recent.map((i) => i.id), ["c"]);
});

test("Recent is capped", () => {
  assert.equal(boardLists(items, [], "All", "", 2).recent.length, 2);
});

test("the highlight steps and wraps, and starts over when its row is gone", () => {
  const ids = ["a", "b", "c"];
  assert.equal(stepId(ids, "a", 1), "b");
  assert.equal(stepId(ids, "c", 1), "a");
  assert.equal(stepId(ids, "a", -1), "c");
  assert.equal(stepId(ids, "gone", 1), "a");
  assert.equal(stepId(ids, undefined, -1), "c");
  assert.equal(stepId([], "a", 1), undefined);
});

test("Tab cycles through areas both ways", () => {
  const areas = ["All", "Work", "Personal", "Unsorted"];
  assert.equal(cycle(areas, "All", 1), "Work");
  assert.equal(cycle(areas, "Unsorted", 1), "All");
  assert.equal(cycle(areas, "All", -1), "Unsorted");
  assert.equal(cycle(areas, "Gone", 1), "All");
});

test("times fit a narrow column", () => {
  const now = 10_000_000_000;
  assert.equal(shortAgo(now - 5_000, now), "now");
  assert.equal(shortAgo(now - 4 * 60_000, now), "4m");
  assert.equal(shortAgo(now - 3 * 3_600_000, now), "3h");
  assert.equal(shortAgo(now - 2 * 86_400_000, now), "2d");
  assert.equal(shortAgo(now - 15 * 86_400_000, now), "2w");
  assert.equal(shortAgo(0, now), "");
});
