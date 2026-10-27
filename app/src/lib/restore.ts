// Restoring the saved workspaces (berth.workspaces) at launch. What was
// saved may come from an older Shipyard, a write cut short or a hand edit,
// and it is read before anything draws: a throw here leaves the window
// empty on every launch after (#137). So nothing here throws. A workspace,
// tab or pane the app can't draw is dropped, a Compare tab whose shape is
// off becomes a plain tab of its panes, and a field of the wrong type takes
// its default. No imports but layout and compare: pnpm test runs this.

import { type Compare, isLane, type Kind } from "./compare.ts";
import { type Leaf, leaves, type PaneContent, type PaneNode, remove } from "./layout.ts";

export interface SavedTab {
  id: string;
  root: PaneNode;
  focus: string;
  compare?: Compare;
}

export interface SavedWorkspace {
  ref: { box: string; location: string; worktree: string; path: string; main?: boolean };
  tabs: SavedTab[];
  active?: string;
  hidden: string[];
  known?: string[];
  visitedAt?: number;
}

export interface SavedState {
  current?: string;
  shown?: string[];
  folded: string[];
  tones: Record<string, string>;
  spaces: Record<string, SavedWorkspace>;
  recentUrls: string[];
}

type Obj = Record<string, unknown>;
const isObj = (v: unknown): v is Obj => typeof v === "object" && v !== null && !Array.isArray(v);
const isStr = (v: unknown): v is string => typeof v === "string";
const named = (v: unknown): v is string => typeof v === "string" && v !== "";
const strs = (v: unknown): string[] => (Array.isArray(v) ? v.filter(isStr) : []);
// Only the optional fields that are strings, so none is set to undefined.
function opt<K extends string>(v: Obj, ...keys: K[]): Partial<Record<K, string>> {
  const out: Partial<Record<K, string>> = {};
  for (const k of keys) if (isStr(v[k])) out[k] = v[k];
  return out;
}

const KINDS: readonly Kind[] = ["agent", "diff", "preview", "artifacts"];
// Deeper than any split a person makes; past it the save is not one.
const DEPTH = 32;

// Every kind of pane, so a kind added to PaneContent fails the typecheck
// here until contentOf restores it, rather than being dropped from saves.
export const PANE_KINDS = Object.keys({ terminal: 1, browser: 1, preview: 1, log: 1, file: 1, helper: 1, panel: 1, artifact: 1, starting: 1, empty: 1, error: 1 } satisfies Record<PaneContent["kind"], 1>);

// contentOf is what a saved pane shows, when it is something a pane can.
export function contentOf(v: unknown): PaneContent | undefined {
  if (!isObj(v)) return undefined;
  switch (v.kind) {
    case "terminal":
      if (!named(v.box) || !named(v.session)) return undefined;
      return { kind: "terminal", box: v.box, session: v.session, ...opt(v, "agent", "command", "title"), ...(v.view === "terminal" || v.view === "conversation" ? { view: v.view } : {}) };
    case "browser":
      return isStr(v.url) ? { kind: "browser", url: v.url, ...(typeof v.watch === "number" && Number.isFinite(v.watch) ? { watch: v.watch } : {}) } : undefined;
    case "preview":
      return isStr(v.url) ? { kind: "preview", url: v.url } : undefined;
    case "log":
      return named(v.box) && isStr(v.location) && isStr(v.worktree) && named(v.service) ? { kind: "log", box: v.box, location: v.location, worktree: v.worktree, service: v.service } : undefined;
    case "file":
      return named(v.path) ? { kind: "file", path: v.path } : undefined;
    case "helper":
      return named(v.box) && named(v.session) && named(v.helper) ? { kind: "helper", box: v.box, session: v.session, helper: v.helper, ...opt(v, "title") } : undefined;
    case "panel":
      return named(v.plugin) && named(v.panel) ? { kind: "panel", plugin: v.plugin, panel: v.panel, title: isStr(v.title) ? v.title : "" } : undefined;
    case "artifact":
      return { kind: "artifact", ...opt(v, "id", "title", "focus", "art") };
    case "starting":
      return { kind: "starting", label: isStr(v.label) ? v.label : "" };
    case "empty":
      return { kind: "empty", label: isStr(v.label) ? v.label : "Agent" };
    case "error":
      return { kind: "error", message: isStr(v.message) ? v.message : "" };
    default:
      return undefined;
  }
}

// leafOf is a saved pane. ids are the panes' ids across every workspace so
// far: PaneLayer lists them all in one keyed list, so a second pane with an
// id already taken is dropped.
function leafOf(v: unknown, ids: Set<string>): Leaf | undefined {
  if (!isObj(v) || v.kind !== "leaf" || !named(v.id) || ids.has(v.id)) return undefined;
  const content = contentOf(v.content);
  if (!content) return undefined;
  ids.add(v.id);
  return named(v.wt) ? { kind: "leaf", id: v.id, content, wt: v.wt } : { kind: "leaf", id: v.id, content };
}

// nodeOf is a saved split tree, with what can't be drawn taken out: a split
// with one good side is that side.
function nodeOf(v: unknown, ids: Set<string>, depth = 0): PaneNode | undefined {
  if (!isObj(v) || depth > DEPTH) return undefined;
  if (v.kind === "leaf") return leafOf(v, ids);
  if (v.kind !== "split" || !named(v.id)) return undefined;
  const a = nodeOf(v.a, ids, depth + 1);
  const b = nodeOf(v.b, ids, depth + 1);
  if (!a || !b) return a ?? b;
  const ratio = typeof v.ratio === "number" && v.ratio > 0 && v.ratio < 1 ? v.ratio : 0.5;
  return { kind: "split", id: v.id, dir: v.dir === "col" ? "col" : "row", ratio, a, b };
}

// compareOf is a saved Compare tab's own part, when root is the one row of
// two panes it always is (compare.ts sides); else the tab is a plain one.
function compareOf(v: unknown, root: PaneNode, ids: Set<string>): Compare | undefined {
  if (!isObj(v) || !named(v.a) || !named(v.b) || v.a === v.b) return undefined;
  if (root.kind !== "split" || root.dir !== "row" || root.a.kind !== "leaf" || root.b.kind !== "leaf") return undefined;
  const panes: Compare["panes"] = {};
  if (isObj(v.panes)) {
    for (const k of KINDS) {
      const p = v.panes[k];
      if (Array.isArray(p) && p.length === 2 && named(p[0]) && named(p[1])) panes[k] = [p[0], p[1]];
    }
  }
  const parked = Array.isArray(v.parked) ? v.parked.flatMap((p) => leafOf(p, ids) ?? []) : [];
  // A lane it doesn't know: the agents' terminals, the plainest one.
  return { a: v.a, b: v.b, lane: isLane(v.lane) ? v.lane : "terminal", sync: typeof v.sync === "boolean" ? v.sync : true, panes, parked, ...opt(v, "path") };
}

const mapLeaves = (n: PaneNode, fn: (l: Leaf) => Leaf): PaneNode => (n.kind === "leaf" ? fn(n) : { ...n, a: mapLeaves(n.a, fn), b: mapLeaves(n.b, fn) });

const unfinished = (l: Leaf) => l.content.kind === "starting" || l.content.kind === "error";

// tabOf is a saved tab. Panes that were starting when the app closed did
// not finish: they go, except in a Compare tab, which keeps its two sides
// (one that was starting is empty).
export function tabOf(v: unknown, ids: Set<string>): SavedTab | undefined {
  if (!isObj(v) || !named(v.id)) return undefined;
  const tree = nodeOf(v.root, ids);
  if (!tree) return undefined;
  const compare = v.compare === undefined ? undefined : compareOf(v.compare, tree, ids);
  let root: PaneNode | undefined = tree;
  if (compare) root = mapLeaves(tree, (l) => (unfinished(l) ? { ...l, content: { kind: "empty", label: "Agent" } } : l));
  else for (const l of leaves(tree)) if (unfinished(l)) root = root && remove(root, l.id);
  if (!root) return undefined;
  const all = leaves(root);
  const focus = isStr(v.focus) && all.some((l) => l.id === v.focus) ? v.focus : all[0].id;
  return compare ? { id: v.id, root, focus, compare } : { id: v.id, root, focus };
}

// workspaceOf is a saved workspace, with its tabs that can show.
export function workspaceOf(v: unknown, ids: Set<string>): SavedWorkspace | undefined {
  if (!isObj(v) || !isObj(v.ref) || !named(v.ref.box) || !named(v.ref.path)) return undefined;
  const r = v.ref;
  const path = r.path as string;
  const ref: SavedWorkspace["ref"] = { box: r.box as string, location: isStr(r.location) ? r.location : "", worktree: isStr(r.worktree) ? r.worktree : path.slice(path.lastIndexOf("/") + 1), path, ...(typeof r.main === "boolean" ? { main: r.main } : {}) };
  const tabs: SavedTab[] = [];
  const seen = new Set<string>();
  for (const raw of Array.isArray(v.tabs) ? v.tabs : []) {
    let t: SavedTab | undefined;
    try {
      t = tabOf(raw, ids);
    } catch {
      t = undefined;
    }
    if (t && !seen.has(t.id)) {
      seen.add(t.id);
      tabs.push(t);
    }
  }
  return {
    ref,
    tabs,
    active: tabs.some((t) => t.id === v.active) ? (v.active as string) : tabs[0]?.id,
    hidden: strs(v.hidden),
    ...(Array.isArray(v.known) ? { known: strs(v.known) } : {}),
    ...(typeof v.visitedAt === "number" && Number.isFinite(v.visitedAt) ? { visitedAt: v.visitedAt } : {}),
  };
}

// restoreSpaces is every saved workspace that can show, by its key.
export function restoreSpaces(v: unknown): Record<string, SavedWorkspace> {
  const out: Record<string, SavedWorkspace> = {};
  if (!isObj(v)) return out;
  const ids = new Set<string>();
  for (const [key, raw] of Object.entries(v)) {
    if (!key.includes(":")) continue;
    try {
      const ws = workspaceOf(raw, ids);
      if (ws) out[key] = ws;
    } catch {
      // Dropped: the others still come back.
    }
  }
  return out;
}

// restoreSaved is what berth.workspaces held, made safe to draw.
export function restoreSaved(v: unknown): SavedState {
  const s = isObj(v) ? v : {};
  const spaces = restoreSpaces(s.spaces);
  const tones: Record<string, string> = {};
  if (isObj(s.tones)) for (const [k, t] of Object.entries(s.tones)) if (named(t)) tones[k] = t;
  return {
    current: isStr(s.current) && spaces[s.current] ? s.current : undefined,
    shown: Array.isArray(s.shown) ? strs(s.shown) : undefined,
    folded: strs(s.folded),
    tones,
    spaces,
    recentUrls: strs(s.recentUrls),
  };
}
