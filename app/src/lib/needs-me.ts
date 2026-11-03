import type { Session } from "@/lib/api";

import type { Question } from "./questions.ts";
import { type Block, type Card, TITLE_MAX, type Verb } from "./cards.ts";
import { agentLabel, agentOf, guessSessionName, sessionName, type SessionState } from "./derive.ts";

// Needs me's rules, pure so the view and its tests share them: what enters the
// queue, in what order, and the card each piece of work is drawn as. Everything
// here reads what the boxes say (sessions and work records, internal/box/work.go)
// and decides nothing a box should: a record's status doesn't change when its
// date passes, so what a past date means is decided here.

export interface WorkSessionLine {
  box?: string;
  name: string;
  title?: string;
  worktree?: string;
  started_by?: string;
  created?: string;
  ended?: string;
  last_seen?: string;
}

export interface WorkRecord {
  id: string;
  title?: string;
  sessions: WorkSessionLine[];
  status: "open" | "parked" | "waiting" | "closed";
  issue?: string;
  issue_ids?: string[];
  project?: string;
  not_until?: string;
  waiting_on?: string;
  check_back?: string;
  last_seen?: string;
  suggested_with?: string[];
  pinned?: boolean;
  created?: string;
  updated?: string;
}

export interface WorkPin {
  kind: "repo" | "project" | "work";
  target: string;
  at?: string;
}

export interface BoxWork {
  records: WorkRecord[];
  pins: WorkPin[];
}

// A session as Needs me reads it: what the fleet says about any session, plus
// the two fields that arrive from the box's own branches.
export type WorkSession = Session & { agent_sub_state?: string; started_by?: string };

export interface SessionEntry {
  box: string;
  session: WorkSession;
  state: SessionState;
}

type Need = "decision" | "task" | "result";
type QueueKind = Need | "close-out" | "group";

export interface Member {
  box: string;
  name: string;
  title: string;
  session?: WorkSession;
  state?: SessionState;
  need?: Need;
  startedBy?: string;
  // Asks in its last message, with no form to fill in.
  noForm?: boolean;
}

export interface QueueItem {
  key: string;
  kind: QueueKind;
  pass: number;
  pinned: boolean;
  box: string;
  title: string;
  why: string;
  // When a person last looked, in ms: what "longest unseen" sorts by.
  seenAt: number;
  record?: WorkRecord;
  members: Member[];
  focus?: Member;
  // A suggested group's other records.
  others?: WorkRecord[];
  // Back by itself: its Not now or Waiting on date has passed.
  returned?: boolean;
}

// What a person answered a moment ago, by box/session: gone from the list at
// once, before the box says so. It holds only for the state it was answered
// in, and not for long, so an answer that didn't take comes back.
export interface Answered {
  since?: string;
  at: number;
}

const ANSWER_HOLD_MS = 60_000;
const STALE_MS = 3 * 24 * 3_600_000;

// How long a card takes, in seconds, for the header's "about 9 min".
const ESTIMATE_S: Record<QueueKind, number> = { decision: 90, task: 120, result: 45, "close-out": 15, group: 10 };

// Pinned decisions, other decisions, pinned tasks, other tasks, results, the
// close-out deck. A suggested group tidies up, it never unblocks, so it comes
// with the deck.
const PASSES: Record<QueueKind, [number, number]> = { decision: [1, 2], task: [3, 4], result: [5, 5], "close-out": [6, 6], group: [7, 7] };

// A title that says it stopped on a question or a block, as a worker titles its
// own session when it ends without a form.
const STOPPED_TITLE = /^\s*(?:❓|⛔)/u;

const ms = (t: string | undefined) => {
  const n = t ? Date.parse(t) : NaN;
  return Number.isNaN(n) ? undefined : n;
};

export const repoOf = (location: string | undefined) => location?.split("/")[0];

const workTitle = (r: WorkRecord) => r.title || r.sessions[0]?.title || "Untitled work";

// whoOf is a member's agent, as people say it.
export const whoOf = (m: Member) => {
  const agent = m.session && agentOf(m.session);
  return agent ? agentLabel(agent) : "The agent";
};

// due says a parked or waiting record's date has passed. Parked with no date
// stays parked: that's what parking means.
export function due(r: Pick<WorkRecord, "status" | "not_until" | "check_back">, now: number): boolean {
  const at = r.status === "parked" ? ms(r.not_until) : r.status === "waiting" ? ms(r.check_back) : undefined;
  return at !== undefined && at <= now;
}

function needOf(e: SessionEntry, line: WorkSessionLine | undefined, now: number, answered: Record<string, Answered> | undefined): Pick<Member, "need" | "noForm"> {
  const s = e.session;
  if (!agentOf(s) || s.queued || s.parked) return {};
  const said = answered?.[`${e.box}/${s.name}`];
  if (said && said.since === s.state_since && now - said.at < ANSWER_HOLD_MS) return {};
  if (e.state === "waiting") return { need: s.agent_sub_state === "act" ? "task" : "decision", noForm: !!s.soft_wait };
  if (e.state !== "finished" && e.state !== "exited") return {};
  // "Not seen since it ended" needs a record to say when it was seen.
  if (!line) return {};
  const ended = ms(s.state_since) ?? ms(s.created) ?? 0;
  const seen = ms(line.last_seen);
  if (seen !== undefined && seen >= ended) return {};
  if (e.state === "finished" && STOPPED_TITLE.test(s.title ?? "")) return { need: "decision", noForm: true };
  return { need: "result" };
}

const RANK: Record<Need, number> = { decision: 0, task: 1, result: 2 };

const focusOf = (members: Member[]) =>
  members
    .filter((m) => m.need)
    .sort((a, b) => RANK[a.need!] - RANK[b.need!] || (ms(a.session?.state_since) ?? 0) - (ms(b.session?.state_since) ?? 0))[0];

export function whyOf(m: Member): string {
  const who = whoOf(m);
  if (m.need === "task") return `${who} needs you to do something`;
  if (m.need === "result") return `${who} finished, and you haven't read it`;
  if (!m.noForm) return `${who} is waiting for your answer`;
  return m.state === "waiting" ? `${who} asked in its last message` : `${who} stopped and needs you`;
}

const days = (from: number, now: number) => Math.max(1, Math.floor((now - from) / (24 * 3_600_000)));

export interface QueueInput {
  sessions: SessionEntry[];
  work: Record<string, BoxWork | undefined>;
  now: number;
  answered?: Record<string, Answered>;
  // Sessions in projects kept off the Agent Dashboard ("box/name"): their work
  // stays out, and doesn't look abandoned for having no live session here.
  offBoard?: ReadonlySet<string>;
}

// buildQueue is everything that needs the person, in the order they meet it. A
// count anywhere is this list's length.
export function buildQueue({ sessions, work, now, answered, offBoard }: QueueInput): QueueItem[] {
  const live = new Map(sessions.map((e) => [`${e.box}/${e.session.name}`, e]));
  const held = new Set<string>();
  const items: QueueItem[] = [];

  // A pin on a repo or a project means the same thing whichever box holds it.
  const allPins = Object.values(work).flatMap((w) => w?.pins ?? []);
  const repos = new Set(allPins.filter((p) => p.kind === "repo").map((p) => p.target));
  const projects = new Set(allPins.filter((p) => p.kind === "project").map((p) => p.target));
  const isPinned = (record: WorkRecord | undefined, locations: (string | undefined)[]) => !!record?.pinned || (!!record?.project && projects.has(record.project)) || locations.some((l) => !!l && repos.has(repoOf(l)!));

  const add = (kind: QueueKind, item: Omit<QueueItem, "kind" | "pass">) => {
    const [pinned, other] = PASSES[kind];
    items.push({ ...item, kind, pass: item.pinned ? pinned : other });
  };

  for (const [box, boxWork] of Object.entries(work)) {
    if (!boxWork) continue;
    for (const record of boxWork.records) {
      if (offBoard && record.sessions.some((l) => offBoard.has(`${box}/${l.name}`))) continue;
      const members: Member[] = record.sessions.map((line) => {
        // A line names its box by the box's own name, which needn't be the name
        // this computer knows it by: here first, then wherever it says.
        const e = live.get(`${box}/${line.name}`) ?? (line.box ? live.get(`${line.box}/${line.name}`) : undefined);
        if (!e) return { box: line.box ?? box, name: line.name, title: line.title || guessSessionName(line.name), startedBy: line.started_by };
        held.add(`${e.box}/${e.session.name}`);
        return {
          box: e.box,
          name: e.session.name,
          title: e.session.title || line.title || sessionName(e.session),
          session: e.session,
          state: e.state,
          startedBy: e.session.started_by ?? line.started_by,
          ...needOf(e, line, now, answered),
        };
      });
      const returned = due(record, now);
      if (record.status !== "open" && !returned) continue;

      const key = `${box}/${record.id}`;
      const title = record.title || members.find((m) => m.session)?.title || workTitle(record);
      const seenAt = ms(record.last_seen) ?? ms(record.created) ?? 0;
      const pinned = isPinned(record, members.map((m) => m.session?.location ?? m.session?.dir));
      const focus = focusOf(members);
      const anySession = members.find((m) => m.session);
      const common = { key, box, title, record, members, pinned, seenAt };
      const backWhy = record.status === "waiting" ? `Waiting on ${record.waiting_on || "someone"}: time to check back` : "You asked to see this again";

      if (focus) {
        add(focus.need!, { ...common, focus, returned, why: returned ? backWhy : whyOf(focus) });
      } else if (returned) {
        // Nothing asks now, but the person said to bring it back: a task for them.
        add("task", { ...common, focus: anySession, returned, why: backWhy });
      } else {
        // Stale since a person last looked, and not on its way to anything:
        // sessions at work, queued or parked stay out.
        const busy = members.some((m) => m.session && (m.state === "running" || m.session.queued || m.session.parked));
        if (!busy && now - seenAt >= STALE_MS) {
          add("close-out", { ...common, focus: anySession, why: `Nobody has looked at this for ${days(seenAt, now)} days` });
        }
      }

      // Proposed, not yet accepted; only records still open.
      const others = (record.suggested_with ?? []).map((id) => boxWork.records.find((r) => r.id === id)).filter((r): r is WorkRecord => !!r && r.status === "open");
      if (record.status === "open" && others.length) {
        add("group", { ...common, key: `group:${key}`, title: `Group ${title} with ${workTitle(others[0])}?`, others, pinned: false, why: "They look like one piece of work" });
      }
    }
  }

  // Sessions no record holds yet (a box with no records, or a moment before it
  // sweeps): one still waiting on a person shouldn't have to wait for that.
  for (const e of sessions) {
    if (held.has(`${e.box}/${e.session.name}`)) continue;
    const need = needOf(e, undefined, now, answered);
    if (!need.need) continue;
    const m: Member = { box: e.box, name: e.session.name, title: sessionName(e.session), session: e.session, state: e.state, startedBy: e.session.started_by, ...need };
    add(need.need, { key: `${e.box}/s:${e.session.name}`, box: e.box, title: m.title, members: [m], focus: m, pinned: isPinned(undefined, [e.session.location]), seenAt: ms(e.session.state_since) ?? ms(e.session.created) ?? 0, why: whyOf(m) });
  }

  return items.sort((a, b) => a.pass - b.pass || a.seenAt - b.seenAt || a.key.localeCompare(b.key));
}

// walkOrder is the list as the person walks it: the card in front stays in
// front whatever arrives, so a new pinned question sorts in right behind it and
// becomes next. Cards passed over go after the rest.
export function walkOrder(items: QueueItem[], current: string | undefined, skipped: readonly string[] = []): QueueItem[] {
  const front = items.find((i) => i.key === current) ?? items[0];
  const rest = items.filter((i) => i !== front);
  const passed = new Set(skipped);
  return [...(front ? [front] : []), ...rest.filter((i) => !passed.has(i.key)), ...rest.filter((i) => passed.has(i.key))];
}

// leftLine is the header: "7 left, about 9 min".
export function leftLine(items: Pick<QueueItem, "kind">[]): string {
  if (!items.length) return "Nothing left";
  const seconds = items.reduce((s, i) => s + ESTIMATE_S[i.kind], 0);
  return `${items.length} left, about ${Math.max(1, Math.ceil(seconds / 60))} min`;
}

const PASS_LABEL: Record<number, string> = { 1: "Pinned decisions", 2: "Decisions", 3: "Pinned tasks for you", 4: "Tasks for you", 5: "Results to read", 6: "Close-out", 7: "Suggested groups" };
export const passLabel = (pass: number) => PASS_LABEL[pass] ?? "";

// sessionTree is a piece of work's sessions with whoever started the others on
// top, each worker under the session that started it. A parent outside the
// group, or a loop in what the box says, just makes a root.
export function sessionTree(members: Member[]): { member: Member; depth: number }[] {
  const byName = new Set(members.map((m) => m.name));
  const hasParent = (m: Member) => !!m.startedBy && byName.has(m.startedBy) && m.startedBy !== m.name;
  const children = new Map<string, Member[]>();
  for (const m of members) if (hasParent(m)) children.set(m.startedBy!, [...(children.get(m.startedBy!) ?? []), m]);
  const roots = members.filter((m) => !hasParent(m));
  // Whoever started others goes first: the orchestrator on top.
  roots.sort((a, b) => Number(children.has(b.name)) - Number(children.has(a.name)));
  const out: { member: Member; depth: number }[] = [];
  const seen = new Set<string>();
  const walk = (m: Member, depth: number) => {
    if (seen.has(m.name)) return;
    seen.add(m.name);
    out.push({ member: m, depth });
    for (const c of children.get(m.name) ?? []) walk(c, depth + 1);
  };
  for (const r of roots) walk(r, 0);
  // Sessions that only start each other have no root; they still show.
  for (const m of members) walk(m, 0);
  return out;
}

const shortDate = (at: string | undefined) => {
  const n = ms(at);
  return n === undefined ? "" : new Date(n).toLocaleDateString(undefined, { month: "short", day: "numeric" });
};

// laterLine says whom a record waits for and what it needs, in words: never its
// status.
function laterLine(r: WorkRecord): string {
  if (r.status === "waiting") return `Waiting on ${r.waiting_on || "someone"}${r.check_back ? `, check back ${shortDate(r.check_back)}` : ""}`;
  return r.not_until ? `Not now, back ${shortDate(r.not_until)}` : "Parked until you bring it back";
}

// laterList is what's put off, soonest back first: out of the count, and still
// somewhere to be found.
export function laterList(work: Record<string, BoxWork | undefined>, now: number): { box: string; record: WorkRecord; line: string }[] {
  const when = (r: WorkRecord) => ms(r.status === "waiting" ? r.check_back : r.not_until) ?? Number.POSITIVE_INFINITY;
  return Object.entries(work)
    .flatMap(([box, w]) => (w?.records ?? []).filter((r) => (r.status === "parked" || r.status === "waiting") && !due(r, now)).map((record) => ({ box, record, line: laterLine(record) })))
    .sort((a, b) => when(a.record) - when(b.record));
}

// What the view read about the session in front, for its card.
export interface CardFacts {
  // Its open form of questions, from its record.
  form?: { tool: string; questions: Question[] };
  // The answers its screen or its host offers, by key.
  choices?: { key: string; label: string; title?: string }[];
  // What it wants to run, when it asks for a permission.
  asks?: string;
  // Its last message.
  said?: string;
}

const EVERY: Verb[] = ["read", "not-now", "waiting-on", "close", "pin"];

// buildCard draws a queue item in the card format: the only way anything gets
// on screen in Needs me (lib/cards).
export function buildCard(item: QueueItem, facts: CardFacts = {}): Card {
  const base = { kind: item.kind, title: item.title.slice(0, TITLE_MAX), why: item.why };
  if (item.kind === "group") {
    const records = [item.record!, ...(item.others ?? [])];
    return {
      ...base,
      blocks: [{ type: "table", columns: ["Piece of work", "Sessions"], rows: records.map((r) => [workTitle(r), String(r.sessions.length)]) }],
      actions: ["accept", "reject"],
    };
  }
  if (item.kind === "close-out") {
    const blocks: Block[] = facts.said ? [{ type: "text", text: facts.said }] : [];
    return { ...base, blocks, actions: ["close", "park", "still-needed"] };
  }
  const blocks: Block[] = [];
  const actions: Verb[] = [];
  if (facts.asks) blocks.push({ type: "text", text: facts.asks });
  if (facts.form) {
    blocks.push({ type: "question", tool: facts.form.tool, questions: facts.form.questions });
    actions.push("answer");
  } else {
    if (facts.said) blocks.push({ type: "text", text: facts.said });
    if (facts.choices?.length) {
      blocks.push({ type: "question", choices: facts.choices });
      actions.push("answer");
    } else if (item.focus?.session && !item.focus.session.exited) {
      actions.push("reply");
      if (item.kind === "task") actions.push("done");
    }
  }
  // Without a record there's nothing to keep a date or a pin on.
  actions.push(...(item.record ? EVERY : (["close"] as Verb[])));
  return { ...base, blocks, actions };
}
