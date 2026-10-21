// After the board's bulk Stop: which of the stopped agents' worktrees to
// archive, and which to keep as they are. Each worktree is archived once,
// however many of the stopped agents worked in it. The main checkout never
// is, nor one where an agent of the batch didn't stop, nor one where an agent
// outside the batch still runs, there or in a folder under it (the box ends
// those too). Plain shells don't count: they close with the archive, as when
// archiving by hand.

import { removalKey } from "./removing.ts";

export interface StopTarget {
  // box/session, as the board keys its agents.
  key: string;
  box: string;
  where?: { location: string; worktree: { name: string; path: string; main?: boolean } };
}

export interface WorktreeTarget {
  // removalKey(box, path).
  key: string;
  box: string;
  location: string;
  wt: { name: string; path: string };
  // The batch's rows that worked in it.
  rows: string[];
}

export interface Plan {
  archive: WorktreeTarget[];
  kept: (WorktreeTarget & { why: string })[];
}

const within = (dir: string, root: string) => dir === root || dir.startsWith(root.endsWith("/") ? root : `${root}/`);

export const KEPT_STOP_FAILED = "an agent there didn't stop";
export const KEPT_OTHER_AGENT = "another agent is still running there";
export const KEPT_UNCHECKED = "couldn't check for other agents on its box";

// planArchive groups the stopped rows by worktree. stopped holds the keys
// whose stop went through; liveAgents are the agent sessions still live on
// the boxes (stopped ones may not have left the listing yet); unchecked are
// boxes that didn't list them, whose worktrees are kept.
export function planArchive(targets: StopTarget[], stopped: Set<string>, liveAgents: { box: string; dir: string; name: string }[], unchecked: ReadonlySet<string> = new Set()): Plan {
  const byWt = new Map<string, WorktreeTarget>();
  for (const t of targets) {
    if (!t.where || t.where.worktree.main) continue;
    const key = removalKey(t.box, t.where.worktree.path);
    const w = byWt.get(key) ?? { key, box: t.box, location: t.where.location, wt: { name: t.where.worktree.name, path: t.where.worktree.path }, rows: [] };
    w.rows.push(t.key);
    byWt.set(key, w);
  }
  const batch = new Set(targets.map((t) => t.key));
  const plan: Plan = { archive: [], kept: [] };
  for (const w of byWt.values()) {
    const ok = w.rows.filter((r) => stopped.has(r)).length;
    // Every stop failed: those rows say so, and nothing here changed.
    if (ok === 0) continue;
    if (ok < w.rows.length) plan.kept.push({ ...w, why: KEPT_STOP_FAILED });
    else if (unchecked.has(w.box)) plan.kept.push({ ...w, why: KEPT_UNCHECKED });
    else if (liveAgents.some((s) => s.box === w.box && within(s.dir, w.wt.path) && !batch.has(`${s.box}/${s.name}`))) plan.kept.push({ ...w, why: KEPT_OTHER_AGENT });
    else plan.archive.push(w);
  }
  return plan;
}
