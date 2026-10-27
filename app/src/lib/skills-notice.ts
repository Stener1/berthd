import { useEffect, useMemo, useState } from "react";
import { create } from "zustand";

import { toastManager } from "@/components/ui/toast";
import { announce } from "@/lib/announce";
import type { BerthEvent } from "@/lib/api";
import { explain } from "@/lib/errors";
import { errorMessage } from "@/lib/format";
import { type SkillAgent, type SkillsReport, skillsApi } from "@/lib/skills";
import {
  nextWake,
  NO_PREFS,
  normalizePrefs,
  type NoticeKind,
  noticeFor,
  shown,
  type SkillsNotice,
  type SkillsNoticePrefs,
  type SnoozeChoice,
  snoozeUntil,
  withNever,
  withoutSnooze,
  withSnooze,
} from "@/lib/skills-notice-model";
import { NONE, useStore } from "@/lib/store";

type AppState = ReturnType<typeof useStore.getState>;

// The sidebar's quiet word that a box's Shipyard skills aren't installed or
// are out of date (components/sidebar/skills-notice.tsx), with Install now
// and Snooze. The rule and the snooze's timing are in skills-notice-model.
//
// Each box is asked where its user's skills stand (GET skills, as Settings
// → Agents does) when it connects, when its berthd changes (an upgrade),
// when the agents it can start change, and when it says skills were
// installed or removed. Never on a timer.
//
// What the person chose (a snooze, "Don't remind me for this box") is kept
// on the laptop agent (/v1/app/skills-notice), beside the notification
// centre's own snoozes: the same in every window, and still there after the
// app is reinstalled. It stays on this computer; another computer paired
// with the same box asks for itself.

const DOC = "/v1/app/skills-notice";
const DOC_KEY = "skills-notice";

// An Install now that didn't go through, with why; it stays until the
// person tries again, snoozes it, or the skills are in place.
export interface Failure {
  notice: SkillsNotice;
  title: string;
  message: string;
  details?: string;
}

interface State {
  // Each box's user-level skills, as it last said.
  reports: Record<string, SkillsReport>;
  prefs: SkillsNoticePrefs;
  // The person's choices are read before anything shows, so a notice they
  // snoozed never appears for a moment at launch.
  loaded: boolean;
  installing: Record<string, true>;
  failed: Record<string, Failure>;
}

export const useSkillsNotice = create<State>()(() => ({ reports: {}, prefs: NO_PREFS, loaded: false, installing: {}, failed: {} }));

// agentIds is the agents a box can start, once it has said (its info).
function agentIds(s: AppState, box: string): string[] | undefined {
  return s.boxes[box]?.info?.agents?.map((a) => a.id);
}

// ---- Asking the boxes ------------------------------------------------------

const asked = new Map<string, number>();

// checkSkills asks a box where its user's skills stand. A later answer
// never loses to an earlier one that arrives after it.
export async function checkSkills(box: string) {
  const client = useStore.getState().client;
  if (!client) return;
  const n = (asked.get(box) ?? 0) + 1;
  asked.set(box, n);
  try {
    const report = await skillsApi.list(client, box);
    if (asked.get(box) === n) setReport(box, report);
  } catch (err) {
    // A berthd from before skills (404) has nothing to remind about. Any
    // other failure keeps what was known: an offline box shows nothing
    // anyway, and is asked again when it is back.
    if (asked.get(box) === n && (err as { status?: number }).status === 404) setReport(box, undefined);
  }
}

function setReport(box: string, report: SkillsReport | undefined) {
  useSkillsNotice.setState((s) => {
    const reports = { ...s.reports };
    if (report) reports[box] = report;
    else delete reports[box];
    // Skills now in place settle a failure left from before.
    const failed = s.failed[box] && report && !noticeFor({ name: box, state: "online" }, agentIds(useStore.getState(), box), report) ? without(s.failed, box) : s.failed;
    return { reports, failed };
  });
}

function without<T>(map: Record<string, T>, key: string): Record<string, T> {
  const { [key]: _gone, ...rest } = map;
  return rest;
}

// settle forgets a box's failed install: it went through, was put away, or
// the box has been away and back since.
const settle = (box: string) => useSkillsNotice.setState((s) => (s.failed[box] ? { failed: without(s.failed, box) } : s));

// handleSkillsEvent hears a box say its skills changed, from here, Settings,
// the CLI or another laptop. A project's copies aren't this notice's.
export function handleSkillsEvent(e: BerthEvent) {
  if (e.type === "app.changed") {
    // Another window's choice. This one's own, still being saved, is
    // already what shows.
    if (e.data?.key === DOC_KEY && !saving) void loadSkillsNoticePrefs();
    return;
  }
  if (!e.box || (e.type !== "skills.installed" && e.type !== "skills.removed")) return;
  if (e.data?.target === "project") return;
  void checkSkills(e.box);
}

// startSkillsNoticeWatch asks each box once it is online and has said which
// agents it has, and again when its build or its agents change. A box that
// goes away is asked afresh when it comes back.
export function startSkillsNoticeWatch(): () => void {
  void loadSkillsNoticePrefs();
  const seen = new Map<string, string>();
  const look = (s: AppState) => {
    // The choices didn't arrive (the agent was restarting, say): asked
    // again as the app hears from it, a few times a minute at most.
    if (!useSkillsNotice.getState().loaded && Date.now() - readAt > 15_000) void loadSkillsNoticePrefs();
    const online = new Set<string>();
    for (const b of s.status?.boxes ?? NONE) {
      if (b.state !== "online") continue;
      online.add(b.name);
      const info = s.boxes[b.name]?.info;
      if (!info?.agents) continue;
      const key = `${info.build ?? ""}|${info.agents.map((a) => a.id).join(",")}`;
      if (seen.get(b.name) === key) continue;
      // Back after being away: why an install failed then is old news.
      if (!seen.has(b.name)) settle(b.name);
      seen.set(b.name, key);
      void checkSkills(b.name);
    }
    for (const name of [...seen.keys()]) if (!online.has(name)) seen.delete(name);
  };
  const unsub = useStore.subscribe(look);
  look(useStore.getState());
  return unsub;
}

// ---- What shows ------------------------------------------------------------

export interface ShownNotice extends SkillsNotice {
  installing: boolean;
  failure?: Failure;
}

function notices(boxes: { name: string; state: string }[], s: AppState, st: State, now: number): ShownNotice[] {
  if (!st.loaded) return [];
  const out: ShownNotice[] = [];
  for (const b of boxes) {
    const failure = st.failed[b.name];
    // A failed install keeps its place even when the box has since gone
    // offline: that is often why it failed.
    const n = noticeFor(b, agentIds(s, b.name), st.reports[b.name]) ?? failure?.notice;
    if (!n || !shown(st.prefs, n, now)) continue;
    out.push({ ...n, installing: !!st.installing[b.name], failure });
  }
  return out;
}

// useSkillsNotices is the notices to show now, one per box, in the boxes'
// own order. It looks again when the soonest snooze ends.
export function useSkillsNotices(): ShownNotice[] {
  const boxes = useStore((s) => s.status?.boxes ?? NONE);
  // Which agents each box has, as one string: a box's sessions and stats
  // change all the time, its agents hardly ever.
  const agents = useStore((s) => (s.status?.boxes ?? NONE).map((b) => `${b.name}:${agentIds(s, b.name)?.join(",") ?? "?"}`).join(" "));
  const st = useSkillsNotice();
  const [tick, setTick] = useState(0);
  const wake = nextWake(st.prefs, Date.now());
  useEffect(() => {
    if (wake === undefined) return;
    // Timers stop at about 24 days; a longer snooze is looked at again then.
    const t = window.setTimeout(() => setTick((n) => n + 1), Math.min(wake - Date.now() + 250, 2 ** 31 - 1));
    return () => window.clearTimeout(t);
  }, [wake, tick]);
  // agents and tick aren't read, but what is read changed when they did:
  // the boxes' agents, and the clock moving past a snooze.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return useMemo(() => notices(boxes, useStore.getState(), st, Date.now()), [boxes, agents, st, tick]);
}

// ---- Install now -----------------------------------------------------------

// installSkills installs or updates all of Shipyard's skills for the box's
// user, for the agents the box has, with the call Settings → Agents makes.
export async function installSkills(n: SkillsNotice) {
  const client = useStore.getState().client;
  const box = n.box;
  if (!client || useSkillsNotice.getState().installing[box]) return;
  // A failure from before stays until this try ends: it is what keeps the
  // notice in place when the box is offline. The notice shows the try.
  useSkillsNotice.setState((s) => ({ installing: { ...s.installing, [box]: true } }));
  const all = useSkillsNotice.getState().reports[box]?.agents ?? [];
  // One call when the box has every agent the skills are for, else one each.
  const agents: (SkillAgent | "all")[] = n.agents.length === all.length ? ["all"] : n.agents;
  try {
    let report: SkillsReport | undefined;
    for (const agent of agents) report = await skillsApi.install(client, box, { skills: "all", agent, target: "user" });
    asked.set(box, (asked.get(box) ?? 0) + 1);
    settle(box);
    setReport(box, report);
    announce(`Shipyard's skills are ${n.kind === "outdated" ? "up to date" : "installed"} on ${box}.`);
  } catch (err) {
    const e = explain(err, { box });
    useSkillsNotice.setState((s) => ({ failed: { ...s.failed, [box]: { notice: n, title: e.title, message: e.message, details: e.details } } }));
    announce(`Couldn't ${n.kind === "outdated" ? "update" : "install"} Shipyard's skills on ${box}. ${e.title}.`);
    // Some of it may have gone through before the failure.
    void checkSkills(box);
  } finally {
    useSkillsNotice.setState((s) => ({ installing: without(s.installing, box) }));
  }
}

// ---- Snooze, and Don't remind me --------------------------------------------

let reading: Promise<void> | undefined;
let readAt = 0;

export function loadSkillsNoticePrefs(): Promise<void> {
  const client = useStore.getState().client;
  if (!client) return Promise.resolve();
  readAt = Date.now();
  reading ??= client
    .laptop<Partial<SkillsNoticePrefs> | null>("GET", DOC)
    .then(
      (doc) => useSkillsNotice.setState({ prefs: normalizePrefs(doc), loaded: true }),
      // Without the person's choices nothing shows, rather than a notice
      // they may have turned off; the watch reads them again.
      () => {},
    )
    .finally(() => {
      reading = undefined;
    });
  return reading;
}

let saving = 0;

async function savePrefs(next: SkillsNoticePrefs, what: string): Promise<boolean> {
  const before = useSkillsNotice.getState().prefs;
  useSkillsNotice.setState({ prefs: next });
  const client = useStore.getState().client;
  saving++;
  try {
    if (!client) throw new Error("no agent token");
    await client.laptop("PUT", DOC, next);
    return true;
  } catch (err) {
    useSkillsNotice.setState({ prefs: before });
    toastManager.add({ type: "error", title: `Couldn't ${what}`, description: errorMessage(err) });
    return false;
  } finally {
    saving--;
  }
}

// snoozeSkillsNotice puts a box's notice away until a time, for the state it
// is in now.
export async function snoozeSkillsNotice(n: Pick<SkillsNotice, "box" | "kind">, choice: SnoozeChoice) {
  const now = new Date();
  const until = snoozeUntil(choice, now);
  if (!(await savePrefs(withSnooze(useSkillsNotice.getState().prefs, n.box, n.kind, until, now.getTime()), "snooze that"))) return;
  settle(n.box);
  announce(`Snoozed. Shipyard reminds you about ${n.box}'s skills again ${choice === "tomorrow" ? "tomorrow" : choice === "week" ? "in a week" : "in a month"}.`);
}

// endSnooze is Settings' "Remind me now".
export function endSnooze(box: string) {
  return savePrefs(withoutSnooze(useSkillsNotice.getState().prefs, box, Date.now()), "change that");
}

// setRemind turns a box's reminder off for good ("Don't remind me for
// devl") or back on, from the notice or from Settings → Agents.
export async function setRemind(box: string, on: boolean, opts: { undo?: boolean } = {}) {
  const before = useSkillsNotice.getState().prefs;
  if (!(await savePrefs(withNever(before, box, !on, Date.now()), on ? "turn that back on" : "turn that off"))) return;
  if (on) return;
  settle(box);
  // Said once, with the way back, as "Don't suggest again" is for a team's
  // setup (lib/team-suggest.ts).
  if (opts.undo)
    toastManager.add({
      title: `No more reminders about ${box}'s skills`,
      description: "Settings → Agents turns them back on.",
      actionProps: { children: "Undo", onClick: () => void undoNever(box, before.snoozed[box]) },
    });
}

// undoNever puts a box's reminder back as it was: on, with the snooze it
// had if that still holds.
function undoNever(box: string, snooze?: { kind: NoticeKind; until: string }) {
  const now = Date.now();
  const on = withNever(useSkillsNotice.getState().prefs, box, false, now);
  return savePrefs(snooze && Date.parse(snooze.until) > now ? withSnooze(on, box, snooze.kind, new Date(snooze.until), now) : on, "undo that");
}
