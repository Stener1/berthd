// node --experimental-strip-types --test src/lib/pinned-worktrees.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { isPinned, pinKey, pinnedFirst, pinWorktree, unpinWorktree, usePinned } from "./pinned-worktrees.ts";

const reset = () => usePinned.setState({ keys: [] });

test("pins keep the order they were made in, once each", () => {
  reset();
  pinWorktree("devl", "/w/shop-search");
  pinWorktree("devl", "/w/shop-cart");
  pinWorktree("devl", "/w/shop-search");
  assert.deepEqual(usePinned.getState().keys, [pinKey("devl", "/w/shop-search"), pinKey("devl", "/w/shop-cart")]);
  // Another box's worktree at the same path is a different one.
  assert.equal(isPinned("cal", "/w/shop-search"), false);
  unpinWorktree("devl", "/w/shop-search");
  assert.equal(isPinned("devl", "/w/shop-search"), false);
  assert.equal(isPinned("devl", "/w/shop-cart"), true);
});

test("pinnedFirst lists pinned rows in pin order and skips pins with no row", () => {
  const rows = [{ k: "devl:/w/a" }, { k: "devl:/w/b" }, { k: "devl:/w/c" }];
  assert.deepEqual(
    pinnedFirst(rows, (r) => r.k, ["devl:/w/c", "gone:/w/x", "devl:/w/a"]).map((r) => r.k),
    ["devl:/w/c", "devl:/w/a"],
  );
});
