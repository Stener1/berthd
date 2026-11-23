import { useMemo } from "react";
import { create } from "zustand";

import { toastError } from "@/components/error-note";
import { confirm } from "@/components/sidebar/confirm";
import { toastManager } from "@/components/ui/toast";
import { useAllSessions, useBoardSessions } from "@/hooks/use-agent-counts";
import { isMock } from "@/hooks/use-berth-connection";
import { boxApi } from "@/lib/api";
import { useEventLog } from "@/lib/events";
import { lastOf } from "@/lib/last-of";
import { type Answered, type BoxWork, buildQueue, type Member, type QueueInput, type QueueItem, walkOrder, type WorkPin, type WorkRecord } from "@/lib/needs-me";
import { type Poller, poll } from "@/lib/poll";
import { answerQuestions, type QuestionAnswer } from "@/lib/questions";
import { share } from "@/lib/share";
import { useStore } from "@/lib/store";

// Needs me's state: each box's work records and pins, kept fresh while the app
// runs so the sidebar's count is always the list's length, and the walk (which
// card is in front, which were passed over).

interface NeedsMeState {
  byBox: Record<string, BoxWork>;
  // The clock Not now and Waiting on dates are read by, so they come back by
  // themselves. It moves with what changed, and at least every NOW_STEP.
  now: number;
  answered: Record<string, Answered>;
  loaded: boolean;
  // The walk. current stays in front whatever arrives.
  current?: string;
  skipped: string[];
  trail: string[];
}

export const useNeedsMeStore = create<NeedsMeState>()(() => ({ byBox: {}, now: Date.now(), answered: {}, loaded: false, skipped: [], trail: [] }));

const NOW_STEP = 30_000;

const boxHasWork = (box: string) => isMock() || !!useStore.getState().boxes[box]?.info?.capabilities?.includes("work");

const workPath = (id: string, rest = "") => `work/${encodeURIComponent(id)}${rest}`;

async function readBox(box: string): Promise<BoxWork | undefined> {
  const client = useStore.getState().client;
  if (!client) return undefined;
  const [records, pins] = await Promise.all([client.box<WorkRecord[] | null>(box, "GET", "work"), client.box<WorkPin[] | null>(box, "GET", "work/pins")]);
  return { records: records ?? [], pins: pins ?? [] };
}

// refreshWork reads every online box's records and says whether anything changed.
// A box that doesn't answer keeps what it had, and one that's gone offline takes
// its records with it, as its sessions go. Nothing changed draws nothing, except
// that the clock moves now and then.
async function refreshWork(): Promise<boolean> {
  const online = (useStore.getState().status?.boxes ?? []).filter((b) => b.state === "online").map((b) => b.name);
  const was = useNeedsMeStore.getState();
  const read = await Promise.all(online.filter(boxHasWork).map(async (box) => [box, await readBox(box).catch(() => was.byBox[box])] as const));
  const byBox = share(was.byBox, Object.fromEntries(read.filter((e): e is [string, BoxWork] => !!e[1])));
  const changed = byBox !== was.byBox;
  const now = Date.now();
  if (changed || !was.loaded || now - was.now >= NOW_STEP) useNeedsMeStore.setState({ byBox, now, loaded: true });
  return changed;
}

let watch: Poller | undefined;

// lookSoon reads the boxes again as soon as the watch may, coalescing a burst.
export const lookSoon = () => watch?.kick();

// startNeedsMeWatch keeps the records fresh for as long as the app runs. Records
// change by person and by rule, so it listens for their events and looks now and
// then besides, which also brings back what's due.
export function startNeedsMeWatch(): () => void {
  const p = poll(refreshWork, { every: 2_000, max: 30_000 });
  watch = p;
  let last = useEventLog.getState().events[0];
  const offEvents = useEventLog.subscribe((s) => {
    const e = s.events[0];
    if (e === last) return;
    last = e;
    if (e?.type.startsWith("work.") || e?.type === "session.started" || e?.type === "session.stopped") p.kick();
  });
  // A box says it keeps records only once its info comes.
  const withInfo = () => Object.values(useStore.getState().boxes).filter((b) => b.info).length;
  let infos = withInfo();
  const offBoxes = useStore.subscribe(() => {
    const n = withInfo();
    if (n === infos) return;
    infos = n;
    p.kick();
  });
  return () => {
    offEvents();
    offBoxes();
    p.stop();
    watch = undefined;
  };
}

const queue = lastOf((sessions: QueueInput["sessions"], work: QueueInput["work"], now: number, answered: QueueInput["answered"], offBoard: QueueInput["offBoard"]) => buildQueue({ sessions, work, now, answered, offBoard }));

// useNeedsMe is the queue, the one list every count reads.
export function useNeedsMe(): QueueItem[] {
  const all = useAllSessions();
  const board = useBoardSessions();
  const byBox = useNeedsMeStore((s) => s.byBox);
  const now = useNeedsMeStore((s) => s.now);
  const answered = useNeedsMeStore((s) => s.answered);
  const off = useMemo(() => {
    if (all.length === board.length) return undefined;
    const shown = new Set(board);
    return new Set(all.filter((e) => !shown.has(e)).map((e) => `${e.box}/${e.session.name}`));
  }, [all, board]);
  return queue(board, byBox, now, answered, off);
}

// The walk.

export const walkTo = (key: string | undefined) => useNeedsMeStore.setState((s) => (s.current === key ? s : { current: key, trail: s.current ? [...s.trail.slice(-49), s.current] : s.trail }));

// walkOn leaves the card in front for the next one. A card passed over waits
// behind the rest, so a walk reaches everything before it comes round again.
// from is the card leaving: one already gone took the walk on with it (an answer
// takes it off the list), and moving again would skip the next card unseen.
export function walkOn(items: QueueItem[], from: string) {
  const { current, skipped } = useNeedsMeStore.getState();
  if (current !== from || !items.some((i) => i.key === from)) return;
  const [front, next] = walkOrder(items, current, skipped);
  if (!next) return;
  const passed = new Set([...skipped, front.key]);
  // Everything's been passed over: a fresh round, with this one last.
  const left = items.filter((i) => !passed.has(i.key));
  useNeedsMeStore.setState({ skipped: left.length ? [...passed] : [front.key] });
  walkTo(next.key);
}

export function walkBack(items: QueueItem[]) {
  const { trail } = useNeedsMeStore.getState();
  const known = new Set(items.map((i) => i.key));
  for (let i = trail.length - 1; i >= 0; i--) {
    if (!known.has(trail[i])) continue;
    useNeedsMeStore.setState((s) => ({ current: trail[i], trail: trail.slice(0, i), skipped: s.skipped.filter((k) => k !== trail[i]) }));
    return;
  }
}

// What a person does, each through the box. A change that fails says so and
// leaves the card where it is.

const client = () => {
  const c = useStore.getState().client;
  if (!c) throw new Error("Not connected");
  return c;
};

const changeBox = (box: string, f: (w: BoxWork) => BoxWork) => useNeedsMeStore.setState((s) => (s.byBox[box] ? { byBox: { ...s.byBox, [box]: f(s.byBox[box]) } } : s));

async function act(box: string, title: string, run: () => Promise<unknown>): Promise<boolean> {
  try {
    await run();
    lookSoon();
    return true;
  } catch (err) {
    toastError(err, { title, box });
    return false;
  }
}

// saveRecord changes a record and shows the box's answer at once.
async function saveRecord(box: string, method: string, path: string, body?: unknown) {
  const record = await client().box<WorkRecord>(box, method, path, body);
  changeBox(box, (w) => ({ ...w, records: w.records.map((r) => (r.id === record.id ? record : r)) }));
}

const patch = (box: string, id: string, p: Partial<Pick<WorkRecord, "status" | "not_until" | "waiting_on" | "check_back">>) => act(box, "Couldn't change it", () => saveRecord(box, "PATCH", workPath(id), p));

export const notNow = (box: string, id: string, until: string) => patch(box, id, { status: "parked", not_until: until });
export const waitOn = (box: string, id: string, who: string, checkBack: string) => patch(box, id, { status: "waiting", waiting_on: who, check_back: checkBack });
export const park = (box: string, id: string) => patch(box, id, { status: "parked", not_until: "" });
// Open again clears its dates, on the box.
export const bringBack = (box: string, id: string) => patch(box, id, { status: "open" });
// Seen: read, or "still needed" on the deck, which starts its 3 days again.
export const markSeen = (box: string, id: string) => act(box, "Couldn't mark it seen", () => saveRecord(box, "POST", workPath(id, "/seen")));

// read is seen, nothing more to do. Something back by itself is settled too. A
// question still waits, so it goes to the back of its pass rather than leaving.
export async function read(item: QueueItem, id: string): Promise<boolean> {
  if (item.returned && !(await bringBack(item.box, id))) return false;
  return markSeen(item.box, id);
}

// What's running now, by box/name, read at the moment of acting: a card can be
// behind what the box has moved on to.
async function sessionsNow(boxes: Iterable<string>) {
  const lists = await Promise.all([...new Set(boxes)].map(async (b) => (await boxApi.sessions(client(), b)).map((s) => [`${b}/${s.name}`, s] as const)));
  return new Map(lists.flat());
}

// close ends its sessions, whose transcripts stay so they can be resumed, and
// keeps the record. It goes by what the box says now: a session at work is stopped
// only after asking (approved, by name), and a new one under an old name is left.
export async function closeWork(item: QueueItem, onClosed: () => void, approved: string[] = []) {
  try {
    const now = await sessionsNow(item.members.filter((m) => m.session).map((m) => m.box));
    const live = item.members.filter((m) => {
      const s = now.get(`${m.box}/${m.name}`);
      return s && !s.exited && s.created === m.session?.created;
    });
    const working = live.filter((m) => {
      const s = now.get(`${m.box}/${m.name}`);
      return s?.agent_state === "running" || (s?.agent_state === "finished" && s.unfinished);
    });
    if (working.some((m) => !approved.includes(m.name))) {
      confirm({
        title: "Close it while it's working?",
        description: `${working.map((m) => m.title).join(", ")} ${working.length === 1 ? "is" : "are"} still working. Closing stops ${working.length === 1 ? "it" : "them"}. The transcript stays, so you can resume.`,
        confirm: working.length === 1 ? "Stop and close" : `Stop ${working.length} and close`,
        destructive: true,
        run: () =>
          closeWork(
            item,
            onClosed,
            working.map((m) => m.name),
          ),
      });
      return;
    }
    await Promise.all(live.map((m) => boxApi.stopSession(client(), m.box, m.name)));
    if (item.record) await saveRecord(item.box, "PATCH", workPath(item.record.id), { status: "closed" });
    lookSoon();
    onClosed();
  } catch (err) {
    toastError(err, { title: "Couldn't close it", box: item.box });
  }
}

const PIN_WARN = 5;

export async function pin(box: string, kind: WorkPin["kind"], target: string, on: boolean) {
  const done = await act(box, on ? "Couldn't pin it" : "Couldn't unpin it", () => (on ? client().box(box, "POST", "work/pins", { kind, target }) : client().box(box, "DELETE", `work/pins?kind=${encodeURIComponent(kind)}&target=${encodeURIComponent(target)}`)));
  if (!done) return;
  changeBox(box, (w) => {
    const rest = w.pins.filter((p) => !(p.kind === kind && p.target === target));
    return { pins: on ? [...rest, { kind, target }] : rest, records: kind === "work" ? w.records.map((r) => (r.id === target ? { ...r, pinned: on } : r)) : w.records };
  });
  const count = Object.values(useNeedsMeStore.getState().byBox).reduce((n, w) => n + w.pins.length, 0);
  if (on && count > PIN_WARN) toastManager.add({ title: `${count} pins`, description: "Pins help most when only a few things come first.", type: "warning" });
}

export const accept = (box: string, id: string) => act(box, "Couldn't group them", () => client().box(box, "POST", workPath(id, "/accept")));
export const reject = (box: string, id: string) => act(box, "Couldn't turn it down", () => client().box(box, "POST", workPath(id, "/reject")));
export const groupWith = (box: string, id: string, other: string) => act(box, "Couldn't group them", () => client().box(box, "POST", workPath(id, "/group"), { with: [other] }));
export const ungroup = (box: string, id: string, session: string) => act(box, "Couldn't ungroup it", () => client().box(box, "POST", workPath(id, "/ungroup"), { sessions: [session] }));

// An answer takes the card off at once: the box says so a moment later.
const noteAnswered = (m: Member) => useNeedsMeStore.setState((s) => ({ answered: { ...s.answered, [`${m.box}/${m.name}`]: { since: m.session?.state_since, at: Date.now() } }, now: Date.now() }));

export async function answerForm(m: Member, tool: string, answers: QuestionAnswer[]) {
  await answerQuestions(client(), m.box, m.name, { tool, answers });
  noteAnswered(m);
}

// reply sends words to a session, never forced: it goes at once to an agent at its
// prompt, and if the agent has reached a question of its own since, it waits rather
// than being typed into it.
export async function reply(m: Member, text: string) {
  await boxApi.send(client(), m.box, m.name, text, true, { when: "idle" });
  noteAnswered(m);
}

// choose presses the key a permission offers. That has to be forced, so it checks
// first that this is still the wait the card shows.
export async function choose(m: Member, key: string) {
  const s = (await sessionsNow([m.box])).get(`${m.box}/${m.name}`);
  if (s?.agent_state !== "waiting" || s.state_since !== m.session?.state_since) throw new Error("It has moved on since this card was drawn. Look at it again.");
  await boxApi.send(client(), m.box, m.name, key, false, { when: "now", force: true });
  noteAnswered(m);
}
