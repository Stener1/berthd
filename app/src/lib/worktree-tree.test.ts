// node --experimental-strip-types --test src/lib/worktree-tree.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { below, nest, type TreeNode } from "./worktree-tree.ts";

interface Row {
  path: string;
  parent?: string;
}

const tree = (rows: Row[]) => nest(rows, (r) => r.path, (r) => r.parent);
const shape = (nodes: TreeNode<Row>[]): unknown[] => nodes.map((n) => (n.children.length ? [n.key, shape(n.children)] : n.key));

test("handed-off worktrees nest under their parent, in the order given", () => {
  const rows = [{ path: "/a" }, { path: "/c", parent: "/b" }, { path: "/b", parent: "/a" }, { path: "/d", parent: "/a" }, { path: "/e" }];
  assert.deepEqual(shape(tree(rows)), [["/a", [["/b", ["/c"]], "/d"]], "/e"]);
  assert.deepEqual(
    below(tree(rows)[0]).map((r) => r.path),
    ["/b", "/c", "/d"],
  );
});

test("a parent that is not listed, or a loop, leaves its rows at the top", () => {
  assert.deepEqual(shape(tree([{ path: "/b", parent: "/gone" }, { path: "/c", parent: "/c" }])), ["/b", "/c"]);
  // Every row whose parents run into the loop is at the top, none lost.
  assert.deepEqual(shape(tree([{ path: "/x", parent: "/y" }, { path: "/y", parent: "/x" }, { path: "/z", parent: "/y" }])), ["/x", "/y", "/z"]);
});
