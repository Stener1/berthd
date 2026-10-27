import type { Session } from "@/lib/api";
import { agentPresets, worktreeRef } from "@/lib/actions";
import { startLoop } from "@/lib/loops";
import { structuredLaunch } from "@/lib/structured";
import { boxHasRuns, refreshRuns, runs as runsApi } from "@/lib/runs";
import * as core from "@/lib/orchestrate-core";
import { scheduleRefresh, useStore } from "@/lib/store";

export { handoffPrompt, reviewPrompt } from "@/lib/orchestrate-core";

// Orchestration for the app and its plugins: the logic in orchestrate-core,
// bound to the app's connection, with sessions and agents looked up in the
// app's state. See docs/guides/orchestration.mdx.

const call: core.BoxCaller = (box, method, path, body) => {
  const c = useStore.getState().client;
  if (!c) return Promise.reject(new Error("not connected to the Shipyard agent"));
  return c.box(box, method, path, body);
};

// sessionLocation is where a session runs, as the box API names it: "loc"
// or "loc/wt".
export function sessionLocation(box: string, session: string): string {
  const data = useStore.getState().boxes[box];
  const s = data?.sessions?.find((x) => x.name === session);
  if (s?.location) return s.location;
  for (const loc of data?.locations ?? []) {
    for (const wt of loc.worktrees ?? []) if (wt.path === s?.dir) return worktreeRef(loc, wt);
  }
  throw new Error(`cannot tell which worktree ${session} is in`);
}

export const send = (box: string, session: string, text: string, opts: boolean | core.SendOptions = {}) => core.send(call, box, session, text, opts);

export const waitTurn = (box: string, turn: string, o?: Parameters<typeof core.waitTurn>[3]) => core.waitTurn(call, box, turn, o);

export const waitSent = (box: string, session: string, sent: Awaited<ReturnType<typeof core.send>>, o?: Omit<core.WaitOptions, "after">) =>
  core.waitSent(call, box, session, sent, o);

export const wait = (box: string, session: string, states: string[], o?: core.WaitOptions) => core.wait(call, box, session, states, o);

export const exec = (box: string, location: string, command: string, timeout?: string, signal?: AbortSignal) => core.exec(call, box, location, command, timeout, signal);

export interface HandoffOptions {
  box: string;
  // The session handing off.
  from: string;
  // An agent preset id, from the box's or the repository's agents.
  agent: string;
  prompt: string;
  // A new worktree for the next agent; without it, the same worktree.
  worktree?: { name: string; branch?: string; base?: string };
  // Where from ran, when it may have ended since.
  location?: string;
  // Called as soon as the new session exists.
  onStarted?(s: Session): void;
}

function preset(box: string, location: string, id: string) {
  const p = agentPresets(box, location.split("/")[0]).find((x) => x.id === id);
  if (!p) throw new Error(`${box} has no agent ${id}`);
  return p;
}

// runSession follows a run until the step that starts an agent has, and
// returns that session: a hand-off or review run on the box.
async function runSession(box: string, id: string, kind = "start_agent"): Promise<Session> {
  for (let i = 0; ; i++) {
    const r = await runsApi.get(box, id);
    let name: string | undefined;
    core.walkSteps(r.steps, (s) => {
      if (s.kind === kind && s.status === "succeeded" && s.session) name = s.session;
    });
    if (name) {
      scheduleRefresh(box, ["locations", "sessions"]);
      const sessions = await call<Session[]>(box, "GET", "sessions");
      const s = sessions.find((x) => x.name === name);
      if (s) return s;
    }
    if (core.terminal(r.status)) throw new Error(r.error || `the run ${r.status} before it started an agent`);
    await new Promise((res) => setTimeout(res, Math.min(4000, 800 + i * 200)));
  }
}

// handoff starts another agent on the work and returns its session. On a
// box with runs, a hand-off to a new worktree is a run of the handoff
// template: the agent writes a short note, berth adds the diffstat, commits
// and turn log, and the new agent's first prompt points at that packet.
export async function handoff(o: HandoffOptions): Promise<Session> {
  if (o.worktree && boxHasRuns(o.box)) {
    const run = await runsApi.start(o.box, {
      template: "handoff",
      params: { session: o.from, name: o.worktree.name, agent: o.agent, prompt: o.prompt },
    });
    void refreshRuns(o.box);
    const s = await runSession(o.box, run.id);
    o.onStarted?.(s);
    return s;
  }
  let location = o.location ?? "";
  try {
    location = sessionLocation(o.box, o.from);
  } catch (err) {
    if (!location) throw err;
  }
  const agent = o.worktree ? { id: o.agent, command: "" } : preset(o.box, location, o.agent);
  // The next agent runs as Labs runs a new one: structured, or in its terminal.
  const known = agentPresets(o.box, location.split("/")[0]).find((x) => x.id === o.agent);
  const launch = structuredLaunch(o.box, o.agent, known);
  const s = await core.handoff(call, { box: o.box, fromSession: o.from, location, agent, prompt: o.prompt, worktree: o.worktree, launch });
  o.onStarted?.(s);
  scheduleRefresh(o.box, o.worktree ? ["locations", "sessions"] : ["sessions"]);
  return s;
}

// review starts a second agent beside a session to read its changes. On a
// box with runs it is a run of the review template, with its own session
// identity, so its turns never end the author's waits.
export async function review(o: Omit<HandoffOptions, "worktree" | "prompt"> & { prompt?: string }): Promise<Session> {
  if (boxHasRuns(o.box)) {
    const run = await runsApi.start(o.box, { template: "review", params: { session: o.from, agent: o.agent, headless: false, ...(o.prompt ? { criteria: o.prompt } : {}) } });
    void refreshRuns(o.box);
    const s = await runSession(o.box, run.id);
    o.onStarted?.(s);
    return s;
  }
  return handoff({ ...o, prompt: o.prompt ?? core.reviewPrompt(o.from) });
}

// runs is the plugin SDK's orchestrate.runs: durable runs on a box.
export const runs = {
  start: (box: string, req: Parameters<typeof runsApi.start>[1], opts?: { idemKey?: string }) =>
    runsApi.start(box, req, opts?.idemKey).then((r) => {
      void refreshRuns(box);
      return r;
    }),
  get: (box: string, id: string) => runsApi.get(box, id),
  list: (box: string, opts?: { status?: string; template?: string; limit?: number }) => runsApi.list(box, opts),
  cancel: (box: string, id: string) => runsApi.cancel(box, id),
  decide: (box: string, id: string, d: { approve: boolean; note?: string; pick?: number; step?: string }) => runsApi.decide(box, id, d),
  done: (box: string, id: string, opts?: { signal?: AbortSignal; onUpdate?(r: Awaited<ReturnType<typeof runsApi.get>>): void }) => core.runDone(call, box, id, opts),
};

export interface LoopOptions {
  box: string;
  session: string;
  prompt: string;
  check: string;
  max?: number;
  signal?: AbortSignal;
  onProgress?(p: core.LoopProgress): void;
}

// loop starts `berth loop` in the app; the loops panel shows its progress.
export function loop(o: LoopOptions) {
  return startLoop(call, { ...o, max: Math.max(1, o.max ?? 5), location: sessionLocation(o.box, o.session) });
}
