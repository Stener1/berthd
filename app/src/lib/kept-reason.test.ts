// node --experimental-strip-types --test src/lib/kept-reason.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { keptReason } from "./kept-reason.ts";

const box = (why: string) => `git worktree remove: '/home/acme/code/shop-fix' ${why}, use --force to delete it`;

test("work git or the box would lose keeps the worktree, and says what it is", () => {
  assert.match(keptReason(box("contains modified or untracked files"))!, /uncommitted changes/);
  assert.match(keptReason(box("contains modified or untracked files in a submodule"))!, /uncommitted changes/);
  assert.match(keptReason(box("contains submodule commits or stashes that no remote has"))!, /commits or stashes no remote has/);
});

test("a check that couldn't run still offers Remove anyway, without calling it uncommitted changes", () => {
  for (const why of ["could not be checked for changes", "has submodules that could not be checked for unpushed work"]) {
    const said = keptReason(box(why));
    assert.ok(said, why);
    assert.doesNotMatch(said!, /uncommitted/);
  }
});

test("other failures aren't a reason to force", () => {
  assert.equal(keptReason("git worktree remove: fatal: 'shop-fix' is not a working tree"), null);
  assert.equal(keptReason("not connected"), null);
});
