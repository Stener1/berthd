import type { BerthPluginContext, LinearIssue, LinearIssueDetail, LinearStatus, LinearTeam } from "@berth/plugin";
import { useSyncExternalStore } from "react";

// Linear's side of Issues: who is connected, the teams, the lists and the
// threads, read through the laptop (berth.linear) and kept for the session,
// so coming back to the screen is instant. Lists older than two minutes are
// fetched again when the screen shows them.

export type Problem = { kind: "off" } | { kind: "auth"; message: string } | { kind: "error"; message: string };

export type Load<T> = { status: "loading"; prev?: T } | { status: "ok"; value: T; at: number } | { status: "problem"; problem: Problem; at: number };

// "mine" is everything assigned to the key's owner; anything else is a
// team's key.
export type Scope = string;
export const MINE = "mine";

let status: Load<LinearStatus> | undefined;
let teams: Load<LinearTeam[]> | undefined;
const lists = new Map<Scope, Load<LinearIssue[]>>();
const details = new Map<string, Load<LinearIssueDetail>>();
let version = 0;
const listeners = new Set<() => void>();

function changed() {
  version++;
  for (const l of listeners) l();
}

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

export const useLinearStore = () => useSyncExternalStore(subscribe, () => version);

export const statusOf = () => status;
export const teamsOf = () => teams;
export const listOf = (scope: Scope) => lists.get(scope);
export const detailOf = (id: string) => details.get(id);

// problemOf sorts a failed call into what the screen can say: no key, a key
// Linear refuses, or anything else in the laptop's own words.
export function problemOf(err: unknown): Problem {
  const code = (err as { code?: string } | null)?.code;
  const message = err instanceof Error ? err.message : String(err);
  if (code === "linear_off") return { kind: "off" };
  if (code === "linear_auth") return { kind: "auth", message };
  return { kind: "error", message };
}

const FRESH = 2 * 60_000;
const prevOf = <T,>(l: Load<T> | undefined) => (l?.status === "ok" ? l.value : l?.status === "loading" ? l.prev : undefined);

export async function loadStatus(berth: BerthPluginContext, force = false) {
  if (status?.status === "loading" || (!force && status?.status === "ok")) return;
  status = { status: "loading", prev: prevOf(status) };
  changed();
  try {
    status = { status: "ok", value: await berth.linear.status(), at: Date.now() };
  } catch (err) {
    status = { status: "problem", problem: problemOf(err), at: Date.now() };
  }
  // Another key is another workspace: nothing read with the old one holds.
  if (force) {
    teams = undefined;
    lists.clear();
    details.clear();
  }
  changed();
}

export async function loadTeams(berth: BerthPluginContext, force = false) {
  if (teams?.status === "loading" || (!force && teams?.status === "ok")) return;
  teams = { status: "loading", prev: prevOf(teams) };
  changed();
  try {
    teams = { status: "ok", value: await berth.linear.teams(), at: Date.now() };
  } catch (err) {
    teams = { status: "problem", problem: problemOf(err), at: Date.now() };
  }
  changed();
}

export async function loadList(berth: BerthPluginContext, scope: Scope, force = false) {
  const cur = lists.get(scope);
  if (cur?.status === "loading") return;
  if (!force && cur && Date.now() - cur.at < FRESH) return;
  lists.set(scope, { status: "loading", prev: prevOf(cur) });
  changed();
  try {
    const value = await berth.linear.issues(scope === MINE ? { mine: true } : { team: scope });
    lists.set(scope, { status: "ok", value, at: Date.now() });
  } catch (err) {
    lists.set(scope, { status: "problem", problem: problemOf(err), at: Date.now() });
  }
  changed();
}

export async function loadDetail(berth: BerthPluginContext, id: string, force = false): Promise<LinearIssueDetail | undefined> {
  const cur = details.get(id);
  if (cur?.status === "ok" && !force) return cur.value;
  if (cur?.status === "loading") return waitFor(id);
  details.set(id, { status: "loading", prev: prevOf(cur) });
  changed();
  try {
    details.set(id, { status: "ok", value: await berth.linear.issue(id), at: Date.now() });
  } catch (err) {
    details.set(id, { status: "problem", problem: problemOf(err), at: Date.now() });
  }
  changed();
  const done = details.get(id);
  return done?.status === "ok" ? done.value : undefined;
}

// waitFor resolves once a thread another caller is fetching arrives.
function waitFor(id: string): Promise<LinearIssueDetail | undefined> {
  return new Promise((resolve) => {
    const stop = subscribe(() => {
      const d = details.get(id);
      if (d?.status === "loading") return;
      stop();
      resolve(d?.status === "ok" ? d.value : undefined);
    });
  });
}

// postComment comments on an issue as the key's owner, then reads the
// thread again.
export async function postComment(berth: BerthPluginContext, id: string, body: string) {
  await berth.linear.comment(id, body);
  await loadDetail(berth, id, true);
}

export function describeProblem(p: Problem): { title: string; body: string; icon: string } {
  switch (p.kind) {
    case "off":
      return { icon: "Cable", title: "Linear isn't connected", body: "Add a personal API key in Settings → Integrations, and your Linear issues show up here." };
    case "auth":
      return { icon: "KeyRound", title: "Linear didn't accept the API key", body: "It may have been revoked. Make a new one in Linear and paste it in Settings → Integrations." };
    default:
      return { icon: "TriangleAlert", title: "Linear couldn't be read", body: p.message };
  }
}
