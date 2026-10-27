import assert from "node:assert/strict";
import { test } from "node:test";

import { share } from "./share.ts";

const box = () => ({
  locations: [
    { name: "acme-web", worktrees: [{ name: "acme-web", main: true }, { name: "checkout-fix", title: "Fix checkout" }] },
    { name: "acme-api", worktrees: [{ name: "acme-api", main: true }] },
  ],
  sessions: [{ name: "a", agent_state: "running" }],
});

test("what didn't change is the old object, and all of it when nothing did", () => {
  const prev = box();
  assert.equal(share(prev, box()), prev);
  assert.equal(share(prev.sessions, box().sessions), prev.sessions);
});

test("a change makes new objects only on its way up", () => {
  const prev = box();
  const next = box();
  next.locations[0].worktrees[1].title = "Fix the acme checkout";
  const out = share(prev, next);
  assert.notEqual(out, prev);
  assert.notEqual(out.locations[0], prev.locations[0]);
  assert.notEqual(out.locations[0].worktrees[1], prev.locations[0].worktrees[1]);
  assert.equal(out.locations[0].worktrees[1].title, "Fix the acme checkout");
  // Beside it, the same objects.
  assert.equal(out.locations[0].worktrees[0], prev.locations[0].worktrees[0]);
  assert.equal(out.locations[1], prev.locations[1]);
  assert.equal(out.sessions, prev.sessions);
});

test("keys come and go, lists grow and shrink", () => {
  const prev = box();
  const next = box() as ReturnType<typeof box> & { error?: string };
  delete (next.locations[0].worktrees[1] as { title?: string }).title;
  next.sessions.push({ name: "b", agent_state: "waiting" });
  const out = share(prev, next);
  assert.equal("title" in out.locations[0].worktrees[1], false);
  assert.equal(out.sessions.length, 2);
  assert.equal(out.sessions[0], prev.sessions[0]);
  assert.deepEqual(share({ a: 1 }, { a: 1, b: undefined }), { a: 1, b: undefined });
  assert.deepEqual(share({ a: 1, b: 2 }, { a: 1 }), { a: 1 });
});

test("anything but plain data is next's", () => {
  const d = new Date(0);
  assert.equal(share(new Date(0), d), d);
  assert.equal(share([1], { 0: 1 }).constructor, Object);
  assert.equal(share({ a: [1] }, { a: 1 }).a, 1);
  assert.equal(share(NaN, NaN), NaN);
});

test("a list read again in another order keeps the items nothing changed", async () => {
  const { shareByKey } = await import("./share.ts");
  const item = (key: string, head: string, files = [{ path: "a.ts", added: 1 }]) => ({ key, head, files });
  const prev = [item("devl|/srv/shop/a", "111"), item("devl|/srv/shop/b", "222")];

  // Nothing changed: the list itself is the one it was.
  assert.equal(shareByKey(prev, [item("devl|/srv/shop/a", "111"), item("devl|/srv/shop/b", "222")]), prev);

  // Sorted differently, and one item moved on: the other is still the same
  // object, and so is what the changed one kept.
  const next = shareByKey(prev, [item("devl|/srv/shop/b", "333"), item("devl|/srv/shop/a", "111")]);
  assert.notEqual(next, prev);
  assert.equal(next[1], prev[0]);
  assert.notEqual(next[0], prev[1]);
  assert.equal(next[0].head, "333");
  assert.equal(next[0].files, prev[1].files);

  // A new item, and one gone.
  const added = shareByKey(prev, [item("devl|/srv/shop/a", "111"), item("devl|/srv/shop/c", "444")]);
  assert.equal(added[0], prev[0]);
  assert.equal(added[1].key, "devl|/srv/shop/c");
  assert.equal(added.length, 2);
});
