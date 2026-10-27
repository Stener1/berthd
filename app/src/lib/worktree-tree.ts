// A worktree an agent handed off nests under the worktree that agent worked
// in (Worktree.parent, from the box), so a chain of handoffs reads as a tree
// in the sidebar. These are the pure parts: building the tree from a flat
// list and reading it.

export interface TreeNode<T> {
  row: T;
  key: string;
  children: TreeNode<T>[];
}

// Past this depth children line up with their parent instead of stepping
// in again, so a long chain keeps its names readable in a narrow sidebar.
export const MAX_INDENT = 3;

// nest builds the tree from rows in the order given; a row whose parent is
// not among them (gone, hidden, on another box) is a root, and so is one
// whose parents lead back to it.
export function nest<T>(rows: T[], key: (r: T) => string, parent: (r: T) => string | undefined): TreeNode<T>[] {
  const nodes = new Map<string, TreeNode<T>>();
  const up = new Map<string, string>();
  for (const row of rows) nodes.set(key(row), { row, key: key(row), children: [] });
  for (const row of rows) {
    const p = parent(row);
    if (p && p !== key(row) && nodes.has(p)) up.set(key(row), p);
  }
  const loops = (k: string) => {
    const seen = new Set([k]);
    for (let p = up.get(k); p; p = up.get(p)) {
      if (seen.has(p)) return true;
      seen.add(p);
    }
    return false;
  };
  const roots: TreeNode<T>[] = [];
  for (const node of nodes.values()) {
    const p = up.get(node.key);
    if (p && !loops(node.key)) nodes.get(p)!.children.push(node);
    else roots.push(node);
  }
  return roots;
}

// below is every row under a node, at any depth.
export function below<T>(node: TreeNode<T>): T[] {
  return node.children.flatMap((c) => [c.row, ...below(c)]);
}
