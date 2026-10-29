import { useEffect } from "react";
import { create } from "zustand";

import type { BerthEvent, Client } from "@/lib/api";
import { useEventLog } from "@/lib/events";
import { type FlowRun, flowsApi } from "@/lib/flows";
import { shareByKey } from "@/lib/share";
import { load, save } from "@/lib/storage";
import { useStore } from "@/lib/store";
import { placeLabel, titleAt, useTitleAt } from "@/lib/worktree-names";

// The review inbox: agents' finished work on every box, from each box's
// GET review. An item leaves when its agent works again, its changes are
// committed and pushed, or it is marked reviewed (remembered here by the
// state it was reviewed in, so new work brings it back).

export interface ReviewFile {
  path: string;
  from?: string;
  code: string;
  added: number;
  removed: number;
  binary?: boolean;
}

export interface ReviewCommit {
  sha: string;
  subject: string;
  author: string;
  when: string;
}

export interface ReviewItem {
  location: string;
  worktree: string;
  path: string;
  branch?: string;
  head?: string;
  main?: boolean;
  base?: string;
  upstream?: string;
  ahead: number;
  behind: number;
  files: ReviewFile[];
  added: number;
  removed: number;
  commits: ReviewCommit[];
  base_ahead: number;
  committed: ReviewFile[];
  session: string;
  agent: string;
  agent_state: string;
  state_since?: string;
  // What the worktree's agent browser left: its last shots, its URL, and
  // its console errors.
  browser?: import("@/lib/agent-browser").BrowserArtifacts;
}

export interface ReviewEntry extends ReviewItem {
  box: string;
  key: string;
}

export interface PullRequest {
  number: number;
  state: string;
  isDraft?: boolean;
  url: string;
  title?: string;
  reviewDecision?: string;
}

interface ReviewState {
  entries: ReviewEntry[];
  loading: boolean;
  loaded: boolean;
  // Boxes whose daemon has no review endpoint yet.
  outdated: string[];
  errors: Record<string, string>;
  runs: Record<string, FlowRun>;
  prs: Record<string, PullRequest | null>;
  reviewed: Record<string, string>;
}

export const useReview = create<ReviewState>()(() => ({
  entries: [],
  loading: false,
  loaded: false,
  outdated: [],
  errors: {},
  runs: {},
  prs: {},
  reviewed: load<Record<string, string>>("berth.review.reviewed", {}),
}));

export const entryKey = (box: string, path: string) => `${box}|${path}`;

// reviewName is what an item is called on screen: its worktree's display
// name when it was given one (lib/worktree-names), else its name.
export const reviewName = (e: ReviewEntry) => titleAt(e.box, e.path) ?? (e.main ? e.location : e.worktree);
export const useReviewName = (e: ReviewEntry) => useTitleAt(e.box, e.path) ?? (e.main ? e.location : e.worktree);

// ProjectGroup is one project's items on one box, as the inbox lists them.
export interface ProjectGroup {
  key: string;
  box: string;
  location: string;
  entries: ReviewEntry[];
}

// byProject groups the inbox by project and box, keeping its order: the
// project with the most recent work first, and its items newest first.
export function byProject(entries: ReviewEntry[]): ProjectGroup[] {
  const groups = new Map<string, ProjectGroup>();
  for (const e of entries) {
    const key = `${e.box}|${e.location}`;
    let g = groups.get(key);
    if (!g) groups.set(key, (g = { key, box: e.box, location: e.location, entries: [] }));
    g.entries.push(e);
  }
  return [...groups.values()];
}

// The branch, unless it only repeats the worktree's name ("me/search-perf"
// in search-perf): the detail always shows it.
const telling = (e: ReviewEntry) => (e.branch && e.branch !== e.worktree && !e.branch.endsWith(`/${e.worktree}`) ? e.branch : undefined);

// placeOf says where an item is, project first: "shop / checkout-fix · devl"
// under a title that names the work, "shop · devl" under the worktree's own
// name.
export const placeOf = (e: ReviewEntry, titled: boolean) => `${titled ? placeLabel(e) : e.location} · ${e.box}`;

// rowMeta is the line under a row's title: where it is, then its branch.
// Under a project's heading, the project and box go without saying.
export function rowMeta(e: ReviewEntry, { name, titled, grouped }: { name: string; titled: boolean; grouped: boolean }): string {
  if (!grouped) return [placeOf(e, titled), telling(e)].filter(Boolean).join(" · ");
  return [titled && name, telling(e) ?? (!titled && e.branch)].filter(Boolean).join(" · ");
}

// where is the exec location for an item: "shop" or "shop/checkout".
export const where = (e: ReviewItem) => (e.main ? e.location : `${e.location}/${e.worktree}`);

// signature is the state an item was reviewed in.
export const signature = (e: ReviewItem) => `${e.head ?? ""}:${e.files.map((f) => `${f.code}${f.path}+${f.added}-${f.removed}`).join(",")}:${e.ahead}`;

export function isReviewed(e: ReviewEntry, reviewed: Record<string, string>) {
  return reviewed[e.key] === signature(e);
}

export function markReviewed(e: ReviewEntry) {
  const reviewed = { ...useReview.getState().reviewed, [e.key]: signature(e) };
  useReview.setState({ reviewed });
  save("berth.review.reviewed", reviewed);
}

// visibleEntries is the inbox: everything not marked reviewed.
export const visibleEntries = (s: Pick<ReviewState, "entries" | "reviewed">) => s.entries.filter((e) => !isReviewed(e, s.reviewed));

let inflight: Promise<void> | undefined;

// refreshReview reads the inbox again: every online box's, or only those
// named (an event on one box changes only its inbox; a box's GET review
// runs git in each of its worktrees with finished work).
export function refreshReview(only?: Iterable<string>): Promise<void> {
  if (inflight) return inflight;
  inflight = doRefresh(only ? new Set(only) : undefined).finally(() => {
    inflight = undefined;
  });
  return inflight;
}

async function doRefresh(only?: Set<string>) {
  const { client, status } = useStore.getState();
  if (!client || !status) return;
  useReview.setState({ loading: true });
  const online = status.boxes.filter((b) => b.state === "online").map((b) => b.name);
  const boxes = only ? online.filter((b) => only.has(b)) : online;
  // The other online boxes keep what they had.
  const kept = (box: string) => !!only && !only.has(box) && online.includes(box);
  const was = useReview.getState();
  const outdated: string[] = was.outdated.filter(kept);
  const errors: Record<string, string> = Object.fromEntries(Object.entries(was.errors).filter(([b]) => kept(b)));
  const runs: Record<string, FlowRun> = Object.fromEntries(Object.entries(was.runs).filter(([k]) => kept(k.slice(0, k.indexOf("|")))));
  const lists = await Promise.all(
    boxes.map(async (box) => {
      try {
        // Both at once: the last check a flow ran in each worktree, if any.
        const checks = flowsApi.runs(client, box, undefined, 50).catch(() => [] as FlowRun[]);
        const items = (await client.box<ReviewItem[] | null>(box, "GET", "review")) ?? [];
        const recent = await checks;
        for (const r of recent) {
          const path = r.event?.data?.path as string | undefined;
          if (!path || !r.steps.some((s) => s.kind === "run")) continue;
          const k = entryKey(box, path);
          if (!runs[k] || runs[k].started < r.started) runs[k] = r;
        }
        return items.map<ReviewEntry>((i) => ({ ...i, box, key: entryKey(box, i.path) }));
      } catch (err) {
        const msg = String((err as Error)?.message ?? err);
        if (/404|not found/i.test(msg)) outdated.push(box);
        else errors[box] = msg;
        return [];
      }
    }),
  );
  // An item a read didn't change is the object it was. The page showing it
  // reads its diff again whenever the item changes, with a command on the
  // box, and a command finishing is one of the events that bring the inbox
  // here to be read again: with a new object every time, an open item kept
  // the box reading its inbox and its diff in a loop for as long as it showed.
  const read = [...useReview.getState().entries.filter((e) => kept(e.box)), ...lists.flat()].sort((a, b) => (b.state_since ?? "").localeCompare(a.state_since ?? ""));
  const entries = shareByKey(useReview.getState().entries, read);
  useReview.setState({ entries, runs, outdated, errors, loading: false, loaded: true });
  void fetchPullRequests(client, entries);
}

// fetchPullRequests asks gh on each box which PR, if any, a branch has.
// It is best effort: no gh, or no GitHub remote, just means no chip.
async function fetchPullRequests(client: Client, entries: ReviewEntry[]) {
  const todo = entries.filter((e) => !(e.key in useReview.getState().prs) && e.branch && !e.main);
  for (let i = 0; i < todo.length; i += 3) {
    await Promise.all(
      todo.slice(i, i + 3).map(async (e) => {
        try {
          const r = await client.box<{ exit_code: number; output: string }>(e.box, "POST", "exec", {
            location: where(e),
            command: "gh pr view --json number,state,isDraft,url,title,reviewDecision 2>/dev/null",
            timeout: "30s",
          });
          const pr = r.exit_code === 0 ? (JSON.parse(r.output) as PullRequest) : null;
          useReview.setState((s) => ({ prs: { ...s.prs, [e.key]: pr } }));
        } catch {
          useReview.setState((s) => ({ prs: { ...s.prs, [e.key]: null } }));
        }
      }),
    );
  }
}

// forgetPullRequest refetches an item's PR next time, after one was opened.
export function forgetPullRequest(key: string) {
  useReview.setState((s) => {
    const prs = { ...s.prs };
    delete prs[key];
    return { prs };
  });
}

// Events that can change what is waiting for review.
const RELEVANT = /^(agent\.|worktree\.|session\.(started|stopped)|flow\.finished|exec\.finished|kit\.)/;
let started = false;

// watchReview keeps the inbox current: on relevant events (debounced) and
// every 60s. It is started once, by whoever first shows a count.
export function watchReview() {
  if (started) return;
  started = true;
  let timer = 0;
  // The boxes whose events came since the last read; all: every box.
  let all = false;
  const boxes = new Set<string>();
  const soon = (box?: string) => {
    if (box) boxes.add(box);
    else all = true;
    window.clearTimeout(timer);
    timer = window.setTimeout(() => {
      const only = all ? undefined : [...boxes];
      all = false;
      boxes.clear();
      void refreshReview(only);
    }, 1500);
  };
  let last: BerthEvent | undefined;
  useEventLog.subscribe((s) => {
    const e = s.events[0];
    if (!e || e === last) return;
    last = e;
    if (RELEVANT.test(e.type)) soon(e.box);
  });
  const onlineOf = (st: ReturnType<typeof useStore.getState>) =>
    (st.status?.boxes ?? [])
      .filter((b) => b.state === "online")
      .map((b) => b.name)
      .join(",");
  useStore.subscribe((s, prev) => {
    // A box that came back is read too.
    if (s.client !== prev.client || s.status?.boxes.length !== prev.status?.boxes.length || (s.status !== prev.status && onlineOf(s) !== onlineOf(prev))) soon();
  });
  // A backstop to the events above, and not while the window is hidden.
  window.setInterval(() => !document.hidden && void refreshReview(), 60_000);
  soon();
}

// useReviewCount is the inbox's size, for the sidebar. Showing it starts
// keeping the inbox current.
export function useReviewCount(): number {
  useEffect(() => watchReview(), []);
  return useReview((s) => visibleEntries(s).length);
}
