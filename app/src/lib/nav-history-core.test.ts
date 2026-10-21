// node --experimental-strip-types --test src/lib/nav-history-core.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { EMPTY, type Place, step, type Trail, visit } from "./nav-history-core.ts";

const dash: Place = { id: "dashboard" };
const review: Place = { id: "review" };
const home: Place = { id: "workspace:" };
const wt = (path: string, tab?: string): Place => ({ id: `workspace:devl:${path}`, ws: `devl:${path}`, detail: tab });
const all = () => true;
const walk = (...places: Place[]) => places.reduce<Trail>((t, p) => visit(t, p), EMPTY);

test("each new place goes on the end, and you are on it", () => {
  const t = walk(home, dash, wt("/w/shop"));
  assert.deepEqual(t.items.map((p) => p.id), ["workspace:", "dashboard", "workspace:devl:/w/shop"]);
  assert.equal(t.at, 2);
});

test("the same place again is no new place; a new tab there changes it in place", () => {
  const t = walk(dash, wt("/w/shop", "a"));
  assert.equal(visit(t, wt("/w/shop", "a")), t);
  const moved = visit(t, wt("/w/shop", "b"));
  assert.equal(moved.items.length, 2);
  assert.equal(moved.items[1].detail, "b");
  assert.equal(moved.at, 1);
});

test("back and forward walk the trail, and stop at its ends", () => {
  const t = walk(home, dash, wt("/w/shop"));
  const b = step(t, -1, all);
  assert.equal(b?.place, dash);
  const bb = step(b!.trail, -1, all);
  assert.equal(bb?.place, home);
  assert.equal(step(bb!.trail, -1, all), undefined);
  const f = step(bb!.trail, 1, all);
  assert.equal(f?.place, dash);
  assert.equal(step(t, 1, all), undefined);
});

test("a new place after going back drops what was ahead, as a browser does", () => {
  const t = walk(home, dash, wt("/w/shop"));
  const back = step(t, -1, all)!.trail;
  const t2 = visit(back, review);
  assert.deepEqual(t2.items.map((p) => p.id), ["workspace:", "dashboard", "review"]);
  assert.equal(step(t2, 1, all), undefined);
});

test("the trail keeps the newest places", () => {
  let t: Trail = EMPTY;
  for (let i = 0; i < 60; i++) t = visit(t, { id: `p${i}` }, 50);
  assert.equal(t.items.length, 50);
  assert.equal(t.items[0].id, "p10");
  assert.equal(t.at, 49);
});

test("back and forward pass over a worktree that is gone, and over where you are already", () => {
  const gone = "devl:/w/old";
  const exists = (p: Place) => p.ws !== gone;
  // Dashboard, the old worktree, Dashboard again: back from there skips both.
  const t = walk(review, dash, wt("/w/old"), dash);
  assert.equal(step(t, -1, exists)?.place, review);
  // And forward from the start skips it too.
  const start = { ...t, at: 0 };
  assert.equal(step(start, 1, exists)?.place, dash);
  assert.equal(step(step(start, 1, exists)!.trail, 1, exists), undefined);
});

// When the worktree in front goes, lib/nav-history.ts steps back with it
// gone, as here.
const without = (...gone: string[]) => (p: Place) => !gone.includes(p.ws ?? "");

test("when the worktree in front goes, you land on the place before it, not Home", () => {
  const t = walk(home, dash, wt("/w/shop"), wt("/w/shop", "t2"));
  const to = step(t, -1, without("devl:/w/shop"));
  assert.equal(to?.place, dash);
  assert.equal(to?.trail.at, 1);
  // Forward from there no longer offers it.
  assert.equal(step(to!.trail, 1, without("devl:/w/shop")), undefined);
});

test("when the worktree in front goes, the place before it is passed over if it went too", () => {
  const t = walk(dash, wt("/w/a"), wt("/w/b"));
  assert.equal(step(t, -1, without("devl:/w/a", "devl:/w/b"))?.place, dash);
  // Nothing before it: undefined, and the app stays on Home.
  assert.equal(step(walk(wt("/w/b")), -1, without("devl:/w/b")), undefined);
});
