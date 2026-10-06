import { create } from "zustand";

import type { BerthEvent, Client } from "@/lib/api";
import { errorMessage } from "@/lib/format";
import { scheduleRefresh, useStore } from "@/lib/store";

// Team setup: an org on GitHub publishes how its engineers' boxes are set
// up, in a repo of its own, <org>/.berth (team.json, schema berth.team/v1:
// box steps run by a script of its own, the repos and where each one's
// setup comes from, and the keys they need). The laptop agent reads it with
// this computer's own GitHub CLI (gh), checks which repos the person can
// read, and hands the reviewed commit to a box, which runs the steps in a
// terminal the person can see (sudo asks them there), signs in to GitHub
// on its own (gh auth login, a credential the box keeps), clones the repos
// and sets each one up. Under the hood it is a "workspace kit".
// docs/guides/team-setup.mdx.

export interface GitHubState {
  state: "missing" | "signed-out" | "ready";
  login?: string;
  name?: string;
  avatar_url?: string;
  install?: { os: "darwin" | "linux" | "windows"; command: string; url: string };
  error?: string;
}

export interface TeamStep {
  id: string;
  title: string;
  detail?: string;
  sudo?: boolean;
}

export interface TeamProjectSpec {
  id: string;
  repo: string;
  path?: string;
  required?: boolean;
  kit?: string;
  init?: string;
  init_detail?: string;
  first_task?: string;
}

export interface TeamKeys {
  from?: string;
  shared?: Record<string, string>;
  ask?: string[];
}

// TeamSetup is team.json as the org wrote it.
export interface TeamSetup {
  schema: string;
  id: string;
  name: string;
  org: string;
  description?: string;
  contact?: string;
  docs?: string;
  // settings reach the box script as BERTH_SETTING_<NAME>.
  box?: { os?: string[]; script?: string; steps?: TeamStep[]; settings?: Record<string, string> };
  projects?: TeamProjectSpec[];
  keys?: Record<string, TeamKeys>;
  updates?: { notify?: boolean };
}

export interface TeamFile {
  path: string;
  size: number;
  text?: string;
}

// A box step as the plan shows it, with Berth's own steps among them:
// "github" (the box's own gh auth login) and, when the keys are 1Password
// references, "1password" (op signed in on the box).
export interface PlanStep {
  id: string;
  title: string;
  detail?: string;
  sudo: boolean;
  berth?: boolean;
  commands: string[];
}

export type ProjectSource = "repo" | "kit" | "none";

export interface ProjectView {
  id: string;
  repo: string;
  path: string;
  required: boolean;
  access: boolean;
  private: boolean;
  size_kb?: number;
  description?: string;
  default_branch?: string;
  source: ProjectSource;
  config_hash?: string;
  kit?: { ref: string; commit?: string; id: string; name: string; hash: string };
  services: string[];
  init?: string;
  init_detail?: string;
  first_task?: string;
  commands: string[];
}

export interface TeamChange {
  kind: "add" | "remove" | "change";
  area: "step" | "setting" | "project" | "key" | "file" | "setup";
  id: string;
  text: string;
  detail?: string;
  sudo?: boolean;
}

export interface TeamUpdate {
  from: string;
  to: string;
  author: string;
  date: string;
  commits: number;
  changes: TeamChange[];
  sudo: string[];
}

export interface TeamOrg {
  login: string;
  name: string;
  avatar_url: string;
  verified: boolean;
  type: "Organization" | "User";
  html_url: string;
}

export interface OrgRepo {
  full_name: string;
  private: boolean;
  description: string;
  pushed_at: string;
  has_berth: boolean;
}

// Where a setup was read: the org's <org>/.berth, or a link to any repo,
// branch or folder (to try one before <org>/.berth exists). key is what
// the routes take for it: the org name, or the link.
export interface TeamSource {
  kind: "org" | "link";
  repo: string;
  ref?: string;
  ref_kind?: "branch" | "tag" | "commit";
  path?: string;
  html_url: string;
  label: string;
  key: string;
}

export interface TeamView {
  // For a link, the owner of the repo read, not the team the setup is for
  // (that is setup.org).
  org: TeamOrg;
  source?: TeamSource;
  state: "found" | "none" | "unreadable" | "no-org";
  repo?: { full_name: string; private: boolean; default_branch: string; html_url: string };
  commit?: { sha: string; short: string; author: string; author_avatar?: string; date: string; message: string };
  setup?: TeamSetup;
  files?: TeamFile[];
  steps?: PlanStep[];
  projects: ProjectView[];
  access: { readable: number; total: number; missing: string[] };
  keys: { shared: number; ask: { project: string; key: string; set?: boolean }[] };
  repos?: OrgRepo[];
  accepted?: { commit: string; box: string; at: string };
  update?: TeamUpdate | null;
  warnings: string[];
}

export type StepState = "todo" | "running" | "waiting" | "done" | "skipped" | "failed";
export type RepoState = "queued" | "cloning" | "setting-up" | "ready" | "failed" | "skipped";

export interface TeamStatus {
  id: string;
  name: string;
  org: string;
  commit: string;
  box: string;
  phase: "steps" | "projects" | "done" | "failed";
  session?: string;
  steps: { id: string; title: string; sudo: boolean; state: StepState; secs?: number; error?: string; code?: string; url?: string }[];
  projects: { id: string; repo: string; state: RepoState; location?: string; error?: string; trust?: string; warnings?: string[]; line?: string }[];
  keys_set: string[];
  started: string;
  updated: string;
  error?: string;
}

export interface Accepted {
  org: string;
  // What the routes take for it again, and where it was read.
  key?: string;
  source?: TeamSource;
  id: string;
  name: string;
  commit: string;
  box: string;
  at: string;
}

export interface SetupRequest {
  box: string;
  commit?: string;
  projects?: string[];
  keys?: Record<string, Record<string, string>>;
  repos?: string[];
}

const enc = encodeURIComponent;

export const teamApi = {
  github: (c: Client) => c.laptop<GitHubState>("GET", "/v1/github"),
  githubLogin: (c: Client) => c.laptop<{ opened: boolean; command: string; error?: string }>("POST", "/v1/github/login"),
  view: (c: Client, org: string, o: { box?: string; from?: string } = {}) => {
    const q = new URLSearchParams();
    if (o.box) q.set("box", o.box);
    if (o.from) q.set("from", o.from);
    const qs = q.toString();
    return c.laptop<TeamView>("GET", `/v1/team/${enc(org)}${qs ? `?${qs}` : ""}`);
  },
  setup: (c: Client, org: string, req: SetupRequest) => c.laptop<TeamStatus>("POST", `/v1/team/${enc(org)}/setup`, req),
  retry: (c: Client, org: string, box: string, from: string) => c.laptop<TeamStatus>("POST", `/v1/team/${enc(org)}/retry`, { box, from }),
  update: (c: Client, org: string) => c.laptop<TeamUpdate | { update: null }>("GET", `/v1/team/${enc(org)}/update`),
  accepted: async (c: Client) => (await c.laptop<Accepted[] | null>("GET", "/v1/team")) ?? [],
  // The box's own record of the setups it ran.
  onBox: async (c: Client, box: string) => (await c.box<TeamStatus[] | null>(box, "GET", "team")) ?? [],
  status: (c: Client, box: string, id: string) => c.box<TeamStatus>(box, "GET", `team/${enc(id)}`),
};

export { isLink, teamRef, validOrg } from "@/lib/team-ref";

// ——— live state ———

// Where the page was opened from, which it says (a link names the org).
export type TeamFrom = "onboarding" | "addbox" | "link" | "sidebar" | "palette";

interface TeamState {
  github?: GitHubState;
  // What the boxes report, by "box/id".
  runs: Record<string, TeamStatus>;
  accepted: Accepted[];
  // Newer commits of accepted setups, by key (the org, or the link).
  updates: Record<string, TeamUpdate>;
  // Repos announced ready, so each says so once.
  announced: Record<string, true>;
}

export const useTeam = create<TeamState>()(() => ({ runs: {}, accepted: [], updates: {}, announced: {} }));

export const runKey = (box: string, id: string) => `${box}/${id}`;

export function putRun(s: TeamStatus) {
  useTeam.setState((t) => ({ runs: { ...t.runs, [runKey(s.box, s.id)]: s } }));
}

// The run for a team on a box, or the latest for the org on any box.
export function runFor(t: Pick<TeamState, "runs">, org: string, box?: string): TeamStatus | undefined {
  const all = Object.values(t.runs).filter((r) => r.org.toLowerCase() === org.toLowerCase() && (!box || r.box === box));
  return all.sort((a, b) => (a.updated < b.updated ? 1 : -1))[0];
}

// keyOf is what opens a run's page again: the link it was set up from, or
// its org.
export function keyOf(t: Pick<TeamState, "accepted">, run: Pick<TeamStatus, "id" | "org" | "box">): string {
  const a = t.accepted.find((x) => x.id === run.id && x.box === run.box) ?? t.accepted.find((x) => x.id === run.id);
  return a?.key ?? run.org;
}

// The run in progress anywhere, for the sidebar and the status bar.
export function activeRun(t: Pick<TeamState, "runs">): TeamStatus | undefined {
  const all = Object.values(t.runs).filter((r) => r.phase === "steps" || r.phase === "projects" || r.phase === "failed");
  return all.sort((a, b) => (a.updated < b.updated ? 1 : -1))[0];
}

// A run that finished a moment ago keeps its repos marked new in the
// sidebar until someone opens one or a while passes.
export function recentlyDone(t: Pick<TeamState, "runs">, within = 30 * 60_000): TeamStatus | undefined {
  return Object.values(t.runs).find((r) => r.phase === "done" && Date.now() - new Date(r.updated).getTime() < within);
}

let loading: Promise<void> | undefined;

// loadTeams asks the laptop which setups it accepted, and each online box
// for its runs; quiet when the agent or a box doesn't know about teams yet.
export function loadTeams(): Promise<void> {
  const client = useStore.getState().client;
  if (!client) return Promise.resolve();
  loading ??= (async () => {
    const accepted = await teamApi.accepted(client).catch(() => [] as Accepted[]);
    useTeam.setState({ accepted });
    const boxes = useStore.getState().status?.boxes.filter((b) => b.state === "online") ?? [];
    await Promise.all(
      boxes.map(async (b) => {
        const runs = await teamApi.onBox(client, b.name).catch(() => [] as TeamStatus[]);
        runs.forEach(putRun);
      }),
    );
    // Updates of accepted setups, shown in the sidebar until reviewed.
    await Promise.all(
      accepted.map(async (a) => {
        const key = a.key ?? a.org;
        const u = await teamApi.update(client, key).catch(() => null);
        if (u && "changes" in u) useTeam.setState((t) => ({ updates: { ...t.updates, [key]: u } }));
      }),
    );
  })().finally(() => {
    loading = undefined;
  });
  return loading;
}

export async function refreshGitHub(): Promise<GitHubState | undefined> {
  const client = useStore.getState().client;
  if (!client) return;
  try {
    const github = await teamApi.github(client);
    useTeam.setState({ github });
    return github;
  } catch (err) {
    const github: GitHubState = { state: "missing", error: errorMessage(err) };
    useTeam.setState({ github });
    return github;
  }
}

const pendingRuns = new Map<string, number>();

// refreshRun reads one run again, debounced: events come in bursts.
export function refreshRun(box: string, id: string) {
  const k = runKey(box, id);
  window.clearTimeout(pendingRuns.get(k));
  pendingRuns.set(
    k,
    window.setTimeout(async () => {
      pendingRuns.delete(k);
      const client = useStore.getState().client;
      if (!client) return;
      try {
        const s = await teamApi.status(client, box, id);
        const before = useTeam.getState().runs[k];
        putRun(s);
        announce(before, s);
      } catch {
        // The box answers the next one.
      }
    }, 60),
  );
}

// Listeners the page and the toasts register for repos turning ready.
type ReadyListener = (run: TeamStatus, project: TeamStatus["projects"][number]) => void;
const readyListeners = new Set<ReadyListener>();
export function onRepoReady(fn: ReadyListener) {
  readyListeners.add(fn);
  return () => void readyListeners.delete(fn);
}

function announce(before: TeamStatus | undefined, now: TeamStatus) {
  for (const p of now.projects) {
    if (p.state !== "ready") continue;
    const k = `${runKey(now.box, now.id)}/${p.id}`;
    if (useTeam.getState().announced[k]) continue;
    const was = before?.projects.find((x) => x.id === p.id)?.state;
    useTeam.setState((t) => ({ announced: { ...t.announced, [k]: true } }));
    if (was && was !== "ready") readyListeners.forEach((fn) => fn(now, p));
  }
}

// handleTeamEvent keeps runs current from the box's team.* events.
export function handleTeamEvent(e: BerthEvent) {
  const d = (e.data ?? {}) as { team?: string; org?: string; project?: string; state?: string; location?: string };
  if (e.type === "team.update" && d.org) {
    void loadTeams();
    return;
  }
  if (!e.box || !d.team) return;
  if (e.type === "team.project" && (d.state === "ready" || d.state === "cloning")) scheduleRefresh(e.box, ["locations"]);
  refreshRun(e.box, d.team);
}

// ——— words ———

export const SOURCE_LABEL: Record<ProjectSource, string> = {
  repo: "repo's .berth/config.json",
  kit: "team kit",
  none: "no setup",
};

export function plural(n: number, one: string, many = `${one}s`) {
  return `${n} ${n === 1 ? one : many}`;
}

// keysLine is the keys step in one line: "5 from 1Password · 1 to enter".
export function keysLine(v: Pick<TeamView, "keys">, entered = 0): string {
  const ask = v.keys.ask.filter((k) => !k.set).length - entered;
  const parts: string[] = [];
  if (v.keys.shared) parts.push(`${v.keys.shared} from 1Password`);
  if (ask > 0) parts.push(`${ask} to enter`);
  return parts.join(" · ");
}

export const sudoCount = (v: Pick<TeamView, "steps">) => (v.steps ?? []).filter((s) => s.sudo).length;

export function durationOf(secs?: number): string {
  if (secs === undefined) return "";
  if (secs < 60) return `${Math.round(secs)}s`;
  const m = Math.floor(secs / 60);
  const s = Math.round(secs % 60);
  return s ? `${m}m ${s}s` : `${m}m`;
}
