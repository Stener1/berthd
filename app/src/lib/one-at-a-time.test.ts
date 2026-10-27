// node --experimental-strip-types --test src/lib/one-at-a-time.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { oneAtATime } from "./one-at-a-time.ts";

const tick = (ms: number) => new Promise((r) => setTimeout(r, ms));

// A stored document that is read, changed and written back whole, slowly,
// as the projects document is.
function store() {
  let doc: string[] = [];
  return {
    add: async (item: string) => {
      const base = [...doc];
      await tick(20);
      doc = [...base, item];
    },
    get doc() {
      return doc;
    },
  };
}

test("two saves started together both land", async () => {
  const s = store();
  const run = oneAtATime();
  await Promise.all([run(() => s.add("evals")), run(() => s.add("shop"))]);
  assert.deepEqual(s.doc, ["evals", "shop"]);
});

test("without it, the second write undoes the first", async () => {
  const s = store();
  await Promise.all([s.add("evals"), s.add("shop")]);
  assert.deepEqual(s.doc, ["shop"]);
});

test("a save that fails doesn't stop the next one, and says it failed", async () => {
  const s = store();
  const run = oneAtATime();
  const failed = run(async () => {
    await tick(5);
    throw new Error("laptop agent unreachable");
  });
  const next = run(() => s.add("shop"));
  await assert.rejects(failed, /unreachable/);
  await next;
  assert.deepEqual(s.doc, ["shop"]);
});
