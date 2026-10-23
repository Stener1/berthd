import { AlertTriangleIcon, BrushCleaningIcon, CheckIcon, EllipsisIcon, ListChecksIcon, SquareIcon, Trash2Icon, XIcon } from "lucide-react";
import { useRef, useState } from "react";
import { create } from "zustand";

import { AgentIcon, StateGlyph } from "@/components/agent-glyph";
import { sessionWord } from "@/lib/state-model";
import { PickOne } from "@/components/pick-one";
import { removeWorktree } from "@/components/sidebar/actions";
import { confirm } from "@/components/sidebar/confirm";
import { Tip } from "@/components/tip";
import { AlertDialog, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogPopup, AlertDialogTitle } from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Menu, MenuItem, MenuPopup, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { Spinner } from "@/components/ui/spinner";
import { toastManager } from "@/components/ui/toast";
import type { SessionEntry } from "@/hooks/use-agent-counts";
import { boxApi } from "@/lib/api";
import { planArchive, type WorktreeTarget } from "@/lib/archive-after-stop";
import { agentOf, type SessionState } from "@/lib/derive";
import { plainError } from "@/lib/errors";
import { errorMessage } from "@/lib/format";
import { removeWorktreeOnBox } from "@/lib/remove-worktree";
import { removalKey } from "@/lib/removing";
import { load, save } from "@/lib/storage";
import { scheduleRefresh, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { duration } from "@/views/dashboard/agent-card";
import { type AgentNames, describeAgent, startedAt } from "@/views/dashboard/names";

// Stopping agents from the board: one from its card, the picked ones, a
// column, or the ones that have sat idle. Stopping ends the agent's session
// on its box; its worktree and files stay, so another agent can pick up
// there. Nothing working or waiting on you is stopped unless you picked it.
// The bulk dialog can also archive their worktrees after, where it's safe.

export const entryKey = (e: SessionEntry) => `${e.box}/${e.session.name}`;
const sinceOf = (e: SessionEntry) => new Date(e.session.state_since ?? e.session.created).getTime();
const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

// Busy agents: stopping one loses the turn it is in.
const busy = (s: SessionState) => s === "running" || s === "waiting";

// A few at a time, so a long list is quick without every request landing
// on a box at once.
const CONCURRENCY = 3;

// stopOne ends one session and drops it from the board straight away; the
// box's next listing confirms it.
async function stopOne(box: string, name: string) {
  const client = useStore.getState().client;
  if (!client) throw new Error("Not connected to the laptop agent");
  await boxApi.stopSession(client, box, name);
  useStore.setState((s) => {
    const d = s.boxes[box];
    return d ? { boxes: { ...s.boxes, [box]: { ...d, sessions: d.sessions?.filter((x) => x.name !== name) } } } : {};
  });
  scheduleRefresh(box, ["sessions"]);
}

interface Progress {
  state: "running" | "ok" | "failed";
  message?: string;
}

// stopAgents stops several, CONCURRENCY at a time, reports each, and
// answers with the keys that stopped.
async function stopAgents(entries: SessionEntry[], onEach: (key: string, p: Progress) => void): Promise<Set<string>> {
  const queue = [...entries];
  const stopped = new Set<string>();
  const worker = async () => {
    for (let e = queue.shift(); e; e = queue.shift()) {
      const key = entryKey(e);
      onEach(key, { state: "running" });
      try {
        await stopOne(e.box, e.session.name);
        stopped.add(key);
        onEach(key, { state: "ok" });
      } catch (err) {
        onEach(key, { state: "failed", message: plainError(err) });
      }
    }
  };
  await Promise.all(Array.from({ length: Math.min(CONCURRENCY, entries.length) }, worker));
  return stopped;
}

// What happened to a stopped agent's worktree when the dialog archived.
interface WorktreeOutcome {
  state: "running" | "archived" | "archiving" | "kept" | "failed";
  message: string;
}

// keepsWork is git refusing, in its own words, to lose uncommitted work.
const keepsWork = (err: unknown) => /modified|untracked|uncommitted|contains/i.test(errorMessage(err));

const leftAsItWas = (o?: WorktreeOutcome) => o?.state === "kept" || o?.state === "failed";

// archiveStopped archives the stopped rows' worktrees the way the sidebar's
// Archive… does: the archive script runs, the branch is kept, and git
// refuses to lose uncommitted work. One at a time on each box, as removals
// go; the boxes side by side. It answers with each worktree's outcome.
async function archiveStopped(rows: Row[], stopped: Set<string>, onEach: (wtKey: string, o: WorktreeOutcome) => void): Promise<Record<string, WorktreeOutcome>> {
  const out: Record<string, WorktreeOutcome> = {};
  const report = (key: string, o: WorktreeOutcome) => {
    out[key] = o;
    onEach(key, o);
  };
  const { client } = useStore.getState();
  // The boxes' own listing, fresh, so an agent started a moment ago counts.
  // A box that doesn't answer keeps its worktrees: nothing says they're free.
  const candidates = [...new Set(rows.filter((r) => r.names.where && !r.names.where.worktree.main).map((r) => r.entry.box))];
  const listed = await Promise.all(candidates.map(async (box) => ({ box, sessions: client ? await boxApi.sessions(client, box).catch(() => undefined) : undefined })));
  const live = listed.flatMap(({ box, sessions }) => (sessions ?? []).filter((s) => !s.exited && agentOf(s)).map((s) => ({ box, dir: s.dir, name: s.name })));
  const plan = planArchive(
    rows.map((r) => ({ key: r.key, box: r.entry.box, where: r.names.where && { location: r.names.where.location.name, worktree: r.names.where.worktree } })),
    stopped,
    live,
    new Set(listed.filter((l) => !l.sessions).map((l) => l.box)),
  );
  for (const k of plan.kept) report(k.key, { state: "kept", message: `Kept: ${k.why}` });
  const archiveOne = async (w: WorktreeTarget) => {
    report(w.key, { state: "running", message: "Archiving…" });
    try {
      if (!client) throw new Error("Not connected to the laptop agent");
      const res = await removeWorktreeOnBox(client, w.box, w.location, w.wt, "archive");
      report(w.key, res.archive ? { state: "archiving", message: "Archiving: its archive script runs, then it goes" } : { state: "archived", message: "Archived; its branch is kept" });
    } catch (err) {
      report(w.key, keepsWork(err) ? { state: "kept", message: "Kept: uncommitted changes" } : { state: "failed", message: `Couldn't archive: ${plainError(err)}` });
    }
  };
  const boxesOf = [...new Set(plan.archive.map((w) => w.box))];
  await Promise.all(
    boxesOf.map(async (box) => {
      for (const w of plan.archive.filter((x) => x.box === box)) await archiveOne(w);
    }),
  );
  return out;
}

// archiveSummary is " · archived 2 worktrees" (or archiving, while a script
// runs), for the toast and the done title alike.
function archiveSummary(outcomes: WorktreeOutcome[]) {
  const gone = outcomes.filter((o) => o.state === "archived" || o.state === "archiving");
  return gone.length ? ` · ${gone.some((o) => o.state === "archiving") ? "archiving" : "archived"} ${plural(gone.length, "worktree")}` : "";
}

function namesOf(e: SessionEntry): AgentNames {
  const d = useStore.getState().boxes[e.box];
  return describeAgent(e.session, d?.sessions, d?.locations);
}

// "shop / checkout-fix", or "shop" for the main checkout.
const atOf = (n: AgentNames) => (n.where ? (n.where.worktree.main ? n.where.location.name : `${n.where.location.name} / ${n.where.worktree.name}`) : n.place);

// confirmStop asks before stopping one agent, naming it and where it runs.
export function confirmStop(e: SessionEntry) {
  const n = namesOf(e);
  const at = atOf(n);
  confirm({
    title: `Stop ${n.name} in ${n.place}?`,
    description: `Its session in ${at} on ${e.box} ends${busy(e.state) ? `, along with the turn it is ${e.state === "waiting" ? "waiting on you in" : "in the middle of"}` : ""}. The worktree and its files are kept, so you can start another agent there.`,
    detail: (
      <>
        {e.box} · {at}
        <br />
        {n.where?.worktree.branch && (
          <>
            <span className="text-muted-foreground">branch {n.where.worktree.branch}</span>
            <br />
          </>
        )}
        <span className="text-muted-foreground">
          {e.session.name} · started {startedAt(e.session.created)}
        </span>
      </>
    ),
    confirm: "Stop agent",
    destructive: true,
    run: async () => {
      await stopOne(e.box, e.session.name);
      toastManager.add({ title: `Stopped ${n.name}`, description: `${at} on ${e.box}. Its worktree is kept.`, type: "success" });
    },
  });
}

// StopMenuItems go at the end of a card's ⋯ menu.
export function StopMenuItems({ entry }: { entry: SessionEntry }) {
  const where = namesOf(entry).where;
  const others = (useStore.getState().boxes[entry.box]?.sessions ?? []).filter((s) => s.dir === entry.session.dir && !s.exited && s.name !== entry.session.name).length;
  return (
    <>
      <MenuSeparator />
      <MenuItem variant="destructive" onClick={() => confirmStop(entry)}>
        <SquareIcon />
        Stop agent…
      </MenuItem>
      {where && !where.worktree.main && (
        // The sidebar's own removal, with its checks: git refuses to lose
        // uncommitted work unless told to.
        <MenuItem variant="destructive" onClick={() => removeWorktree(entry.box, where.location, where.worktree)}>
          <Trash2Icon />
          <span className="flex-1">Stop and remove worktree</span>
          {others > 0 && <span className="text-muted-foreground text-meta">+{others} more</span>}
        </MenuItem>
      )}
    </>
  );
}

// The bulk dialog: "stop" for picked agents or a column, "cleanup" for the
// idle ones in Done and Ready.
interface StopRequest {
  kind: "stop" | "cleanup";
  entries: SessionEntry[];
  // The column it came from, for the title.
  column?: string;
  id: number;
}

const useStopDialog = create<{ req?: StopRequest }>()(() => ({}));
let nextId = 0;

export const openStop = (req: Omit<StopRequest, "id">) => req.entries.length > 0 && useStopDialog.setState({ req: { ...req, id: ++nextId } });

type After = "1h" | "4h" | "1d";
const AFTER: { value: After; label: string; ms: number }[] = [
  { value: "1h", label: "1 hour", ms: 3_600_000 },
  { value: "4h", label: "4 hours", ms: 4 * 3_600_000 },
  { value: "1d", label: "1 day", ms: 86_400_000 },
];

// StopDialog is the host; the dashboard renders it once.
export function StopDialog() {
  const req = useStopDialog((s) => s.req);
  const [running, setRunning] = useState(false);
  const close = () => !running && useStopDialog.setState({ req: undefined });
  return (
    <AlertDialog open={!!req} onOpenChange={(o) => !o && close()}>
      {req && <Body key={req.id} req={req} onRunning={setRunning} onClose={() => useStopDialog.setState({ req: undefined })} />}
    </AlertDialog>
  );
}

interface Row {
  entry: SessionEntry;
  key: string;
  names: AgentNames;
  idle: number;
}

function Body({ req, onRunning, onClose }: { req: StopRequest; onRunning(r: boolean): void; onClose(): void }) {
  const cleanup = req.kind === "cleanup";
  // Names are taken when the dialog opens: they renumber as agents stop.
  const [rows] = useState<Row[]>(() => {
    const now = Date.now();
    return req.entries.map((entry) => ({ entry, key: entryKey(entry), names: namesOf(entry), idle: now - sinceOf(entry) })).sort((a, b) => b.idle - a.idle);
  });
  const [after, setAfter] = useState<After>(() => load("berth.dashboard.cleanupAfter", "4h"));
  // A parked agent (turn check read it as waiting on a reply or a later
  // time) is idle on purpose: it is listed, never ticked for you.
  const overFor = (a: After) => new Set(rows.filter((r) => !r.entry.session.parked && r.idle > AFTER.find((x) => x.value === a)!.ms).map((r) => r.key));
  const [ticked, setTicked] = useState<Set<string>>(() => (cleanup ? overFor(after) : new Set(rows.map((r) => r.key))));
  const [progress, setProgress] = useState<Record<string, Progress>>({});
  const [archive, setArchive] = useState(() => load("berth.dashboard.stopArchives", false));
  const [outcomes, setOutcomes] = useState<Record<string, WorktreeOutcome>>({});
  const [phase, setPhase] = useState<"confirm" | "running" | "done">("confirm");
  const cancel = useRef<HTMLButtonElement>(null);

  const chosen = rows.filter((r) => ticked.has(r.key));
  const busyCount = chosen.filter((r) => busy(r.entry.state)).length;
  const ok = rows.filter((r) => progress[r.key]?.state === "ok").length;
  const failed = rows.filter((r) => progress[r.key]?.state === "failed");
  // The box shows only when there is a worktree to archive.
  const canArchive = chosen.some((r) => r.names.where && !r.names.where.worktree.main);
  const archiving = archive && canArchive;
  const outcomeOf = (r: Row) => (r.names.where ? outcomes[removalKey(r.entry.box, r.names.where.worktree.path)] : undefined);
  const boxes = [...new Set(rows.map((r) => r.entry.box))].sort();
  const toggle = (key: string, on: boolean) =>
    setTicked((s) => {
      const n = new Set(s);
      if (on) n.add(key);
      else n.delete(key);
      return n;
    });

  const run = async () => {
    const targets = chosen.map((r) => r.entry);
    setPhase("running");
    onRunning(true);
    const stopped = await stopAgents(targets, (key, p) => setProgress((s) => ({ ...s, [key]: p })));
    const found = archiving ? Object.values(await archiveStopped(chosen, stopped, (key, o) => setOutcomes((s) => ({ ...s, [key]: o })))) : [];
    onRunning(false);
    if (stopped.size < targets.length || found.some(leftAsItWas)) {
      setPhase("done");
      return;
    }
    const perBox = [...new Set(targets.map((t) => t.box))].map((b) => `${targets.filter((t) => t.box === b).length} on ${b}`);
    toastManager.add({
      title: `Stopped ${plural(targets.length, "agent")}${archiveSummary(found)}`,
      description: `${perBox.join(", ")}. ${found.length ? "Their branches are kept." : "Their worktrees are kept."}`,
      type: "success",
    });
    onClose();
  };

  const summary = archiveSummary(Object.values(outcomes));

  const title =
    phase === "done"
      ? failed.length
        ? `Stopped ${ok} of ${ok + failed.length}${summary}`
        : `Stopped ${plural(ok, "agent")}${summary}`
      : cleanup
        ? "Clean up idle agents"
        : req.column
          ? `Stop ${chosen.length === rows.length ? "all " : ""}${plural(chosen.length, "agent")} in ${req.column}?`
          : `Stop ${plural(chosen.length, "agent")}?`;
  const description =
    phase === "done"
      ? `${failed.length ? "The rest were left running. " : ""}${Object.values(outcomes).some(leftAsItWas) ? "Some worktrees were kept as they are. " : ""}Each one says why below.`
      : cleanup
        ? `Stops agents in Done and Ready that have sat idle for a while. Working agents and ones waiting on you are left alone. ${archiving ? "Their worktrees are archived where it's safe; branches are kept." : "Worktrees and their files are kept."}`
        : `Each one's session ends on its box; ${archiving ? "then its worktree is archived where it's safe, and its branch is kept." : "its worktree and files are kept."}${busyCount ? ` ${busyCount === 1 ? "One is" : `${busyCount} are`} still working or waiting on you, and ${busyCount === 1 ? "its turn ends" : "their turns end"} too.` : ""}`;

  return (
    // The keyboard starts on Cancel: Enter right after ⌫ or a menu item must
    // never stop agents mid-turn.
    <AlertDialogPopup className="sm:max-w-lg" initialFocus={cancel}>
      <AlertDialogHeader>
        <AlertDialogTitle>{title}</AlertDialogTitle>
        <AlertDialogDescription>{description}</AlertDialogDescription>
      </AlertDialogHeader>

      {cleanup && phase === "confirm" && (
        <div className="mx-6 mb-3 flex flex-wrap items-center gap-x-3 gap-y-2 text-body">
          <span className="text-muted-foreground">Idle longer than</span>
          <PickOne
            label="Idle longer than"
            value={after}
            options={AFTER}
            onChange={(a) => {
              setAfter(a);
              save("berth.dashboard.cleanupAfter", a);
              setTicked(overFor(a));
            }}
          />
        </div>
      )}

      <div className="mx-6 max-h-72 overflow-y-auto rounded-lg border" role="group" aria-label="Agents to stop">
        {rows.length === 0 && <p className="px-3 py-4 text-center text-muted-foreground text-meta">Nothing in Done or Ready.</p>}
        {boxes.map((box) => {
          const group = rows.filter((r) => r.entry.box === box);
          return (
            <div key={box}>
              <div className="sticky top-0 z-1 flex items-center gap-2 border-b bg-muted/80 px-3 py-1 font-mono text-meta text-muted-foreground backdrop-blur-sm">
                <span className="flex-1">{box}</span>
                <span className="tabular-nums">
                  {group.filter((r) => ticked.has(r.key)).length}/{group.length}
                </span>
              </div>
              <ul className="divide-y divide-border/60">
                {group.map((r) => (
                  <StopRow key={r.key} row={r} ticked={ticked.has(r.key)} progress={progress[r.key]} outcome={outcomeOf(r)} locked={phase !== "confirm"} cleanup={cleanup} onToggle={(on) => toggle(r.key, on)} />
                ))}
              </ul>
            </div>
          );
        })}
      </div>

      {cleanup && phase === "confirm" && rows.length > 0 && (
        <p className="mx-6 mt-2 text-muted-foreground text-meta">
          {chosen.length ? `${plural(chosen.length, "agent")} of ${rows.length} picked.` : `None idle that long.`} Tick or untick any of them.
        </p>
      )}

      {phase === "confirm" && canArchive && (
        <label className="mx-6 mt-3 flex cursor-pointer items-start gap-2.5 text-body">
          <Checkbox
            className="mt-0.5"
            checked={archive}
            onCheckedChange={(v) => {
              setArchive(v === true);
              save("berth.dashboard.stopArchives", v === true);
            }}
          />
          <span>
            Archive their worktrees where it's safe
            <span className="block text-muted-foreground text-meta">The archive script runs and branches are kept. One with uncommitted changes, or another agent still in it, stays as it is.</span>
          </span>
        </label>
      )}

      <AlertDialogFooter>
        {phase === "done" ? (
          <Button onClick={onClose}>Done</Button>
        ) : (
          <>
            <Button ref={cancel} variant="ghost" disabled={phase === "running"} onClick={onClose}>
              Cancel
            </Button>
            <Button variant="destructive" loading={phase === "running"} disabled={!chosen.length} onClick={() => void run()}>
              <SquareIcon />
              {chosen.length ? `Stop ${plural(chosen.length, "agent")}` : "Stop agents"}
            </Button>
          </>
        )}
      </AlertDialogFooter>
    </AlertDialogPopup>
  );
}

function StopRow({ row, ticked, progress, outcome, locked, cleanup, onToggle }: { row: Row; ticked: boolean; progress?: Progress; outcome?: WorktreeOutcome; locked: boolean; cleanup: boolean; onToggle(on: boolean): void }) {
  const { entry, names } = row;
  const state = entry.state;
  const stopped = progress?.state === "ok";
  return (
    <li>
      <label className={cn("flex items-center gap-2.5 px-3 py-1.5 text-body", !locked && "cursor-pointer hover:bg-accent/50", !ticked && "text-muted-foreground")}>
        <span className="flex w-4 shrink-0 justify-center">
          {progress?.state === "running" || (stopped && outcome?.state === "running") ? (
            <Spinner className="size-3.5" />
          ) : stopped && leftAsItWas(outcome) ? (
            <AlertTriangleIcon className="size-3.5 text-warning-foreground" />
          ) : stopped ? (
            <CheckIcon className="size-3.5 text-success" />
          ) : progress?.state === "failed" ? (
            <XIcon className="size-3.5 text-destructive-foreground" />
          ) : (
            <Checkbox checked={ticked} disabled={locked} onCheckedChange={(v) => onToggle(!!v)} aria-label={`Stop ${names.name} in ${names.place}`} />
          )}
        </span>
        <AgentIcon agent={agentOf(entry.session)} />
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-1.5">
            <span className="truncate">{names.place}</span>
            {names.crowded && <span className="shrink-0 text-muted-foreground text-meta">{names.name}</span>}
          </span>
          {progress?.state === "failed" ? (
            <span className="block whitespace-normal break-words text-destructive-foreground text-meta">{progress.message}</span>
          ) : stopped && outcome ? (
            <span className={cn("block whitespace-normal break-words text-meta", leftAsItWas(outcome) ? "text-warning-foreground" : "text-muted-foreground")}>Stopped · {outcome.message}</span>
          ) : stopped ? (
            <span className="block truncate text-meta text-muted-foreground">Stopped</span>
          ) : (
            <span className="block truncate text-meta text-muted-foreground">
              {[entry.session.parked && "parked", names.where?.location.name, names.crowded && `started ${startedAt(entry.session.created)}`, names.where?.worktree.branch].filter(Boolean).join(" · ")}
            </span>
          )}
        </span>
        {busy(state) && !cleanup ? (
          <span className="flex shrink-0 items-center gap-1 text-meta text-warning-foreground">
            <AlertTriangleIcon className="size-3" />
            {state === "waiting" ? "needs you" : "working"}
          </span>
        ) : (
          <Tip label={`${sessionWord(state)} for ${duration(row.idle)}`}>
            <span className="flex shrink-0 items-center gap-1.5 font-mono text-meta text-muted-foreground tabular-nums">
              <StateGlyph state={state} />
              {duration(row.idle)}
            </span>
          </Tip>
        )}
      </label>
    </li>
  );
}

// ColumnMenu is a column header's ⋯: pick the column's agents, stop them
// all, or clean up the idle ones (from Done and Ready).
export function ColumnMenu({ title, items, cleanup, onPick }: { title: string; items: SessionEntry[]; cleanup?: SessionEntry[]; onPick(): void }) {
  return (
    <Menu>
      <Tip label={`${title}: actions`}>
        <MenuTrigger
          render={
            <button
              type="button"
              aria-label={`${title} column actions`}
              className="inline-flex size-5 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground data-popup-open:bg-accent data-popup-open:text-foreground"
            />
          }
        >
          <EllipsisIcon className="size-3.5" />
        </MenuTrigger>
      </Tip>
      <MenuPopup align="end" className="min-w-52">
        <MenuItem onClick={onPick}>
          <ListChecksIcon />
          Select all {items.length}
        </MenuItem>
        {cleanup && (
          <MenuItem disabled={!cleanup.length} onClick={() => openStop({ kind: "cleanup", entries: cleanup })}>
            <BrushCleaningIcon />
            Clean up idle agents…
          </MenuItem>
        )}
        <MenuSeparator />
        <MenuItem variant="destructive" onClick={() => openStop({ kind: "stop", entries: items, column: title })}>
          <SquareIcon />
          Stop all {items.length} in {title}…
        </MenuItem>
      </MenuPopup>
    </Menu>
  );
}
