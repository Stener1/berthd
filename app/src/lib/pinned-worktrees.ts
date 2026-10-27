import { create } from "zustand";

// Pinned worktrees sit in their own list at the top of the sidebar, in the
// order they were pinned, whatever their project and whether or not
// anything runs in them. They are kept on this computer, by box and path,
// and a pin goes with its worktree once the box says it went
// (lib/remove-worktree).
//
// No value imports from "@/": pnpm test runs this file in plain node.

const KEY = "berth.pinnedWorktrees";

export const pinKey = (box: string, path: string) => `${box}:${path}`;

// Storage can be missing or throw (private windows, plain node); pins are
// then kept for this run only.
function load(): string[] {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(KEY) ?? "[]");
    return Array.isArray(v) ? v.filter((k): k is string => typeof k === "string") : [];
  } catch {
    return [];
  }
}
function save(keys: string[]) {
  try {
    localStorage.setItem(KEY, JSON.stringify(keys));
  } catch {
    // Kept in memory.
  }
}

export const usePinned = create<{ keys: string[] }>()(() => ({ keys: load() }));

// Another window's pins replace this one's, so neither saves over the other.
if (typeof window !== "undefined") window.addEventListener("storage", (e) => e.key === KEY && usePinned.setState({ keys: load() }));

const set = (keys: string[]) => {
  usePinned.setState({ keys });
  save(keys);
};

export const isPinned = (box: string, path: string, keys = usePinned.getState().keys) => keys.includes(pinKey(box, path));

export function pinWorktree(box: string, path: string) {
  const { keys } = usePinned.getState();
  if (!isPinned(box, path, keys)) set([...keys, pinKey(box, path)]);
}

export function unpinWorktree(box: string, path: string) {
  const { keys } = usePinned.getState();
  if (isPinned(box, path, keys)) set(keys.filter((k) => k !== pinKey(box, path)));
}

// pinnedFirst puts rows in pin order and leaves out the rest; a pin whose
// worktree is not listed (its box away or still loading) shows nothing.
export function pinnedFirst<T>(rows: T[], key: (r: T) => string, keys = usePinned.getState().keys): T[] {
  const byKey = new Map(rows.map((r) => [key(r), r]));
  return keys.flatMap((k) => byKey.get(k) ?? []);
}
