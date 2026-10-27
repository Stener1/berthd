// A plugin's helper does not outlive it: turning the plugin off, or removing
// it, stops its program on the boxes. These are the words Settings → Plugins
// says that with, before and after, from what the laptop agent answers
// (internal/agent/helpercleanup.go). Kept free of imports so it can be
// tested on its own.

// HelperOnBox is a plugin's helper on one paired box.
export interface HelperOnBox {
  box: string;
  // The box's word (off, installing, starting, running, failed), offline
  // for a box that can't be asked now, or unsupported.
  state: string;
  version?: string;
  // A removal waiting for the box to connect.
  pending?: "remove" | "purge";
}

// PluginHelpers is GET /v1/plugins/{id}/helpers: where the helper is.
export interface PluginHelpers {
  plugin: string;
  // The helper's name, when the plugin's manifest names one.
  name?: string;
  // The plugin is off until its new files are allowed, and its helper was
  // left running meanwhile.
  held?: boolean;
  boxes: HelperOnBox[];
}

export interface AttachedService {
  location: string;
  worktree: string;
  service: string;
}

// HelperRemoved is what became of the helper on one box.
export interface HelperRemoved {
  box: string;
  state: "removed" | "none" | "pending" | "failed";
  purged?: boolean;
  error?: string;
  // Services still running with what the helper told them: they need a restart.
  attached?: AttachedService[];
}

// listOf is names as a sentence has them: "a", "a and b", "a, b and c".
export function listOf(names: string[]): string {
  if (names.length <= 1) return names.join("");
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

// helperWhere is the boxes the helper is on, and the ones that can't be
// asked. known is false while the agent hasn't answered (or can't).
export function helperWhere(h: PluginHelpers | undefined): { known: boolean; on: string[]; away: string[] } {
  if (!h) return { known: false, on: [], away: [] };
  const idle = new Set(["off", "offline", "unsupported", "none"]);
  return {
    known: true,
    on: h.boxes.filter((b) => !idle.has(b.state)).map((b) => b.box),
    away: h.boxes.filter((b) => b.state === "offline").map((b) => b.box),
  };
}

// stopNote says what turning a plugin off (or, with remove, removing it)
// does to its program, naming the boxes when they are known.
export function stopNote(program: string, h: PluginHelpers | undefined, remove = false): string {
  const { known, on, away } = helperWhere(h);
  if (!known) {
    return `Its program, ${program}, is stopped and removed on every box it runs on. What it recorded there is ${remove ? "deleted too" : "kept"}.`;
  }
  const parts: string[] = [];
  if (on.length > 0) {
    parts.push(`Its program, ${program}, is stopped and removed on ${listOf(on)}.`);
    parts.push(remove ? "What it recorded on your boxes is deleted too." : "What it recorded there is kept.");
  } else if (away.length > 0) {
    parts.push(`Its program, ${program}, isn't running on a box Shipyard can reach.`);
    if (remove) parts.push("What it recorded on your boxes is deleted.");
  } else {
    parts.push(`Its program, ${program}, isn't running on any box${remove ? "." : ", so nothing is stopped."}`);
    if (remove) parts.push("What it recorded on your boxes is deleted.");
  }
  if (away.length === 1) parts.push(`${away[0]} is offline: if it runs there, it's stopped when ${away[0]} next connects.`);
  if (away.length > 1) parts.push(`${listOf(away)} are offline: if it runs there, it's stopped when each next connects.`);
  return parts.join(" ");
}

// stoppedNote says what was done, once the plugin is off or removed: where
// the program was stopped, where that still waits, and which services to
// restart. Nothing to say is undefined.
export function stoppedNote(program: string, results: HelperRemoved[] | undefined): string | undefined {
  if (!results?.length) return undefined;
  const of = (state: HelperRemoved["state"]) => results.filter((r) => r.state === state);
  const parts: string[] = [];
  const removed = of("removed").map((r) => r.box);
  if (removed.length > 0) parts.push(`Stopped ${program} on ${listOf(removed)}.`);
  const pending = of("pending").map((r) => r.box);
  if (pending.length === 1) parts.push(`${pending[0]} can't be reached: it's stopped there when ${pending[0]} next connects.`);
  if (pending.length > 1) parts.push(`${listOf(pending)} can't be reached: it's stopped on each when it next connects.`);
  for (const r of of("failed")) parts.push(`${r.box} couldn't remove it${r.error ? `: ${r.error}` : ""}.`);
  for (const r of results) {
    const services = (r.attached ?? []).map((s) => `${s.service} in ${s.location}/${s.worktree}`);
    if (services.length > 0) parts.push(`Restart ${listOf(services)} on ${r.box}: ${services.length === 1 ? "it still points" : "they still point"} at ${program}.`);
  }
  return parts.length > 0 ? parts.join(" ") : undefined;
}

// heldNote says a helper was left running while its plugin waits to be
// reviewed again, and where. Undefined when it isn't, or runs nowhere.
export function heldNote(program: string, h: PluginHelpers | undefined): string | undefined {
  const { on } = helperWhere(h);
  if (!h?.held || on.length === 0) return undefined;
  return `${program} is still running on ${listOf(on)} while this waits for your review.`;
}
