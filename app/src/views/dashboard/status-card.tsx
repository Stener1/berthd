import {
  ArrowUpRightIcon,
  BotIcon,
  BrainIcon,
  CheckIcon,
  FilePenIcon,
  FileSearchIcon,
  GlobeIcon,
  HourglassIcon,
  PauseIcon,
  SearchIcon,
  ShieldAlertIcon,
  SparklesIcon,
  SquareIcon,
  TerminalIcon,
  UploadIcon,
  type LucideIcon,
} from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { memo, type ReactNode, useEffect, useRef, useState } from "react";

import { AgentIcon } from "@/components/agent-glyph";
import { openOrchestrate, SessionActions } from "@/components/orchestrate/session-actions";
import { Tip } from "@/components/tip";
import { Checkbox } from "@/components/ui/checkbox";
import { toastManager } from "@/components/ui/toast";
import type { SessionEntry } from "@/hooks/use-agent-counts";
import { isMock } from "@/hooks/use-berth-connection";
import { approvalLabel, doneLine, isApproval, type Pill, type RunStep, type StepKind, stepTime } from "@/lib/agent-run";
import { boxApi } from "@/lib/api";
import { interrupt, modelLabel } from "@/lib/chat-controls";
import { agentOf, atQuestion, ON_ITS_WORK } from "@/lib/derive";
import { errorMessage } from "@/lib/format";
import { permissionChoices } from "@/lib/screen";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { focusSession } from "@/lib/workspaces";
import { describeAgent, startedAt } from "@/views/dashboard/names";
import { confirmStop, StopMenuItems } from "@/views/dashboard/stop";
import { useCardRun } from "@/views/dashboard/use-card-run";
import { duration, useNow } from "@/views/dashboard/agent-card";
import { type Choice, useScreenTail } from "@/views/dashboard/use-screen-tail";

// The most finished steps a card lists: the latest ones.
const LOG_ROWS = 3;

// How the card moves: its height follows its log on a spring, and a new
// headline rolls in from below.
const SPRING = { type: "spring", stiffness: 420, damping: 36, mass: 0.8 } as const;

const STEP_ICONS: Record<StepKind, LucideIcon> = {
  read: FileSearchIcon,
  search: SearchIcon,
  run: TerminalIcon,
  edit: FilePenIcon,
  fetch: GlobeIcon,
  skill: SparklesIcon,
  agent: BotIcon,
  publish: UploadIcon,
  think: BrainIcon,
  ask: ShieldAlertIcon,
};

// StatusCard is one agent on the board as a live status card (Settings ›
// Agents › Agent Dashboard › Live status cards): what it is
// doing now and for how long, its model and how far through its task list
// it is, the steps it took this turn, and what you can do about it (pause
// or stop it, approve what it asks, open it). While the board is selecting
// (or with ⌘ or ⇧ held), a click picks it instead.
export function StatusCard({ entry, selecting, selected, onSelect }: { entry: SessionEntry; selecting?: boolean; selected?: boolean; onSelect?(): void }) {
  const { box, session, state } = entry;
  const locations = useStore((s) => s.boxes[box]?.locations);
  const sessions = useStore((s) => s.boxes[box]?.sessions);
  const canPause = useStore((s) => !!s.boxes[box]?.info?.capabilities?.includes("controls")) || isMock();
  const { where, place, name, crowded, prompt, title: work, agent } = describeAgent(session, sessions, locations);
  // A titled agent is named by its work, its place in the tip; otherwise its place.
  const title = work ?? place;
  const now = useNow();
  const ref = useRef<HTMLElement>(null);
  const { run, model } = useCardRun(entry, ref);
  // Without a record of its turn (a shell, an older box) the card shows
  // the agent's screen instead; a waiting one's screen has its options. A
  // ready agent has said nothing yet: its screen is only a banner.
  const { tail, choices: onScreen } = useScreenTail(box, session, 3, state === "running" && !run, state === "waiting" || (!run && state !== "ready"));
  const since = new Date(session.state_since ?? session.created).getTime();
  const [shown, setShown] = useState(false);
  useEffect(() => {
    const raf = requestAnimationFrame(() => setShown(true));
    return () => cancelAnimationFrame(raf);
  }, []);

  const open = () => void focusSession(box, session.name);
  const waiting = atQuestion(session, state);
  // A structured session offers its answers with its ask; a terminal's are
  // on its screen.
  const choices: Choice[] = session.ask?.choices?.length ? session.ask.choices : onScreen;
  const pending = run?.steps[run.steps.length - 1]?.status === "waiting" ? run.steps[run.steps.length - 1] : undefined;
  const approval = waiting && isApproval(session.ask?.tool ?? run?.ask?.tool) ? permissionChoices(choices) : undefined;
  const allow = approval?.find((c) => c.label === "Allow");
  const deny = approval?.find((c) => c.label === "Deny");
  const approving = !!allow && !!deny;
  // A form of questions (AskUserQuestion, or one its record shows) isn't
  // answered by one number: it is answered in the agent's own view.
  const form = waiting && (session.ask?.tool === "AskUserQuestion" || (pending?.kind === "ask" && !run?.ask));
  const asking = waiting && !approving && !form && choices.length > 0;
  const onItsWork = state === "running" && !!session.unfinished;
  const parked = state === "ready" && !!session.parked;

  // What it does now, what it asks, or how its turn went: one line under
  // its title, so the title stays what the board is scanned by. A plain
  // ready agent has nothing to say there.
  const doing: { text: string; icon: LucideIcon; tip?: string; tone?: "info" | "success"; since?: number } | undefined = onItsWork
    ? { text: "Waiting on its work", icon: HourglassIcon, tip: ON_ITS_WORK }
    : parked
      ? { text: "Parked: waiting on a reply or a later time", icon: HourglassIcon, tip: "Its work is paused until someone else replies or a later time. Nothing runs and nothing needs you; it picks up by itself." }
      : state === "running"
        ? { text: run?.active?.label ?? "Working", icon: STEP_ICONS[run?.active?.kind ?? "think"], since: run?.active?.since }
        : state === "waiting"
          ? { text: session.ask ? approvalLabel(session.ask.tool, session.ask.input, session.ask.message) : (pending?.label ?? (session.soft_wait ? "Waiting on your reply" : "Needs you")), icon: ShieldAlertIcon, tone: "info" }
          : state === "finished"
            ? { text: run ? doneLine(run, since) : "Done", icon: CheckIcon, tip: `Finished ${new Date(since).toLocaleString()}`, tone: "success" }
            : undefined;

  // The log: the steps it took before the one it is on, or the screen's
  // last lines.
  const steps = run?.steps.filter((s) => s.status === "done").slice(-LOG_ROWS) ?? [];
  const lines = !run && state !== "ready" ? (asking ? (tail ?? []).filter((l) => !/^\s*(?:[❯›>]\s*)?\d[.)]\s/.test(l)) : tail) : undefined;
  const plan = run?.plan;
  const progress = plan ? (state === "finished" ? `${plan.done} of ${plan.total} steps` : `Step ${plan.current} of ${plan.total}`) : undefined;

  return (
    <article
      ref={ref}
      role="button"
      tabIndex={0}
      aria-pressed={selecting ? !!selected : undefined}
      data-state={state}
      onClick={(e) => (onSelect && (selecting || e.metaKey || e.shiftKey) ? onSelect() : open())}
      onKeyDown={(e) => e.key === "Enter" && e.target === e.currentTarget && (selecting && onSelect ? onSelect() : open())}
      className={cn(
        "group relative flex cursor-pointer flex-col overflow-hidden rounded-2xl border bg-card text-left shadow-xs/5 outline-none transition-[opacity,translate,border-color] duration-200 hover:border-ring/40 focus-visible:ring-2 focus-visible:ring-ring",
        shown ? "translate-y-0 opacity-100" : "translate-y-1 opacity-0",
        // Waiting is said by the whole outline, never a stripe down one side.
        state === "waiting" && "border-warning/45 hover:border-warning/70",
        selected && "border-primary/60 ring-1 ring-primary/30 hover:border-primary/60",
      )}
    >
      <div className="px-3 pt-2.5">
        <div className="flex items-center gap-2">
          {selecting && <Checkbox checked={!!selected} tabIndex={-1} aria-hidden className="pointer-events-none" />}
          <AgentMark agent={agentOf(session)} state={onItsWork || parked ? "ready" : state} />
          <span className="flex min-w-0 flex-1 items-baseline gap-1.5">
            <span className="min-w-0 truncate text-row">{title}</span>
            {/* Several agents in one worktree: which one this is. */}
            {!work && crowded && (
              <Tip label={`Session ${session.name}`}>
                <span className="shrink-0 text-meta text-muted-foreground">{name}</span>
              </Tip>
            )}
          </span>
          {/* The clock, and in its place on hover or focus the card's ⋯
              menu, so neither moves anything. Done: its turn's length is
              on the line below, and the corner holds only the menu. */}
          <span className="relative flex min-w-6 shrink-0 items-center justify-end" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
            {state !== "finished" && (
              <Tip label={`Since ${new Date(since).toLocaleString()}`}>
                <span
                  data-testid="card-timer"
                  className={cn(
                    "font-mono text-meta tabular-nums transition-opacity group-focus-within:opacity-0 group-hover:opacity-0",
                    state === "waiting" ? "text-warning-foreground" : "text-muted-foreground",
                  )}
                >
                  {duration(now - since)}
                </span>
              </Tip>
            )}
            {!selecting && (
              <SessionActions
                box={box}
                session={session.name}
                className="-mr-1 absolute right-0 opacity-0 transition-opacity focus-visible:opacity-100 group-focus-within:opacity-100 group-hover:opacity-100 data-popup-open:opacity-100"
              >
                <StopMenuItems entry={entry} />
              </SessionActions>
            )}
          </span>
        </div>

        <div className="mt-0.5 flex items-center gap-2 pl-7 text-meta text-muted-foreground">
          {/* Where first, as the board is scanned by it; the model last,
              the first to go in a narrow column. */}
          <span className="min-w-0 flex-1 truncate">
            {work && `${place} · `}
            {box}
            {where && !(work && where.worktree.main) && ` · ${where.location.name}`}
            {crowded && ` · started ${startedAt(session.created)}`}
            {where?.worktree.branch && where.worktree.branch !== place && <span className="opacity-70"> · {where.worktree.branch}</span>}
            {` · ${model ? modelLabel(model) : agent}`}
          </span>
          {progress && <span className="shrink-0 tabular-nums">{progress}</span>}
        </div>

        {plan && <Pills pills={plan.pills} live={state === "running"} />}

        {doing && (
          // Its own box holds the spacing: a Tip without a label is just its child.
          <div className="mt-2.5 flex min-w-0 pl-7">
            <Tip label={doing.tip}>
              <span className="flex min-w-0 flex-1">
                <Now {...doing} now={now} />
              </span>
            </Tip>
          </div>
        )}

        {prompt && !run && (!work || !prompt.startsWith(work.replace(/…$/, ""))) && (
          <div className="mt-1 truncate pl-7 text-meta text-foreground/70 italic" title={prompt}>
            “{prompt}”
          </div>
        )}

        <div>
          {steps.length > 0 && (
            <ul aria-label="Steps" className="mt-1.5 pl-7">
              <AnimatePresence initial={false}>
                {steps.map((s) => (
                  <StepRow key={s.id} step={s} />
                ))}
              </AnimatePresence>
            </ul>
          )}
          {lines && lines.length > 0 && (
            <div className={cn("mt-2 space-y-px rounded-lg bg-muted/50 px-2 py-1.5 font-mono text-tag", state === "waiting" ? "text-foreground/85" : "text-muted-foreground")}>
              {lines.map((l, i) => (
                <div key={i} className="truncate whitespace-pre" title={l}>
                  {l}
                </div>
              ))}
            </div>
          )}
          {asking && <Answers box={box} session={session.name} choices={choices} />}
        </div>
      </div>

      <footer className="mt-2.5 flex h-9 items-center gap-1.5 border-t px-2.5" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
        {!selecting &&
          (approving ? (
            <Approval box={box} session={session.name} allow={allow} deny={deny} />
          ) : state === "running" ? (
            <>
              {canPause && !onItsWork && <PauseButton box={box} session={session.name} who={agent} />}
              <PillButton onClick={() => confirmStop(entry)} label={`Stop ${name} in ${place}`} tip="Stop agent…">
                <SquareIcon className="size-2.5" />
                Stop
              </PillButton>
            </>
          ) : state === "waiting" || state === "ready" ? (
            <>
              {form ? (
                <button
                  type="button"
                  onClick={open}
                  className="inline-flex h-6 items-center gap-0.5 rounded-full bg-blue-600 px-3 font-medium text-tag text-white outline-none transition-colors hover:bg-blue-600/90 focus-visible:ring-2 focus-visible:ring-ring"
                >
                  Answer
                  <ArrowUpRightIcon className="size-3" aria-hidden />
                </button>
              ) : (
                <PillButton onClick={() => openOrchestrate("send", box, session.name)}>{state === "ready" ? "Prompt…" : "Reply…"}</PillButton>
              )}
              <PillButton onClick={() => confirmStop(entry)} label={`Stop ${name} in ${place}`} tip="Stop agent…">
                <SquareIcon className="size-2.5" />
                Stop
              </PillButton>
            </>
          ) : null)}
        <button type="button" onClick={open} className="-mr-1 ml-auto inline-flex h-6 items-center gap-0.5 rounded-md px-1.5 text-muted-foreground text-tag outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring">
          Open run
          <ArrowUpRightIcon className="size-3" aria-hidden />
        </button>
      </footer>
    </article>
  );
}

// AgentMark is the agent's mark in a ring that says its state: a turning
// arc while it works, a pulse while it waits, and a check once it is done.
function AgentMark({ agent, state }: { agent?: string; state: SessionEntry["state"] }) {
  const reduce = useReducedMotion();
  const done = state === "finished";
  return (
    <span className="relative inline-flex size-5 shrink-0 items-center justify-center" aria-hidden>
      <span
        className={cn(
          "absolute inset-0 rounded-full border-[1.5px]",
          state === "running" ? "animate-spin border-info/25 border-t-info [animation-duration:1.4s] motion-reduce:animate-none" : state === "waiting" ? "border-warning/60" : done ? "border-success/50" : "border-border",
        )}
      />
      {state === "waiting" && <span className="absolute inset-0 animate-ping rounded-full border border-warning/40 [animation-duration:2s] motion-reduce:hidden" />}
      <AnimatePresence initial={false} mode="popLayout">
        <motion.span key={done ? "done" : "mark"} className="inline-flex" initial={reduce ? false : { scale: 0.4, opacity: 0 }} animate={{ scale: 1, opacity: 1 }} exit={reduce ? undefined : { scale: 0.4, opacity: 0 }} transition={SPRING}>
          {done ? <CheckIcon className="size-3 text-success" strokeWidth={3} /> : <AgentIcon agent={agent} className="size-3" />}
        </motion.span>
      </AnimatePresence>
    </span>
  );
}

// Now is what the agent does now, in the column under its title: an icon
// and the words, rolling in from below when they change, with the step's
// own clock. An approval it waits on is tinted blue, a finished turn green.
function Now({ text, icon: Icon, tone, since, now }: { text: string; icon: LucideIcon; tone?: "info" | "success"; since?: number; now: number }) {
  const reduce = useReducedMotion();
  return (
    <span
      data-testid="card-now"
      data-status={tone === "info" ? "waiting" : tone === "success" ? "done" : "active"}
      className={cn("relative flex h-6 min-w-0 flex-1 items-center overflow-hidden rounded-md text-desc", tone === "info" ? "bg-info/10 px-2 text-info-foreground" : "text-foreground/85")}
    >
      <AnimatePresence initial={false} mode="popLayout">
        <motion.span
          key={text}
          className="flex min-w-0 flex-1 items-center gap-1.5"
          initial={reduce ? false : { y: "100%", opacity: 0 }}
          animate={{ y: 0, opacity: 1 }}
          exit={reduce ? undefined : { y: "-100%", opacity: 0 }}
          transition={SPRING}
        >
          <Icon className={cn("size-3.5 shrink-0", tone === "info" ? "text-info" : tone === "success" ? "text-success" : "text-muted-foreground")} aria-hidden />
          {/* No shimmer: it repaints the line every frame, on every working
              card, and the turning ring already says it is at work. */}
          <span className="min-w-0 truncate">{text}</span>
        </motion.span>
      </AnimatePresence>
      {since !== undefined && <span className="ml-2 shrink-0 font-mono text-tag text-muted-foreground tabular-nums">{duration(now - since)}</span>}
    </span>
  );
}

// Pills are the agent's task list as a bar: one pill a task, blue once
// done, pulsing on the one it is on.
function Pills({ pills, live }: { pills: Pill[]; live: boolean }) {
  return (
    <div className="mt-2 flex gap-1 pl-7" aria-hidden>
      {pills.map((p, i) => (
        <span
          key={i}
          className={cn(
            "h-1 flex-1 rounded-full transition-colors duration-500",
            p === "done" ? "bg-info" : p === "active" ? cn("bg-info/70", live && "animate-pulse motion-reduce:animate-none") : "bg-muted",
          )}
        />
      ))}
    </div>
  );
}

// StepRow is a step it took this turn, quiet beside what it does now: in
// the past tense, with how long it took. A new one fades in; the one it
// pushes off the top leaves at once, so a full log never holds an extra
// row for a moment and the card keeps its height as steps roll by.
// Memo: the board's clock ticks every card once a second, and a finished
// step has nothing to redraw (nor its layout animation to measure).
const StepRow = memo(function StepRow({ step }: { step: RunStep }) {
  const reduce = useReducedMotion();
  return (
    <motion.li
      layout={reduce ? false : "position"}
      initial={reduce ? false : { opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={SPRING}
      data-status={step.status}
      className="flex h-5 items-center gap-1.5 text-muted-foreground text-tag"
    >
      <CheckIcon className="size-2.5 shrink-0 opacity-70" strokeWidth={2.5} aria-hidden />
      <span className="min-w-0 flex-1 truncate">{step.label}</span>
      {step.ms !== undefined && <span className="shrink-0 font-mono tabular-nums opacity-80">{stepTime(step.ms)}</span>}
    </motion.li>
  );
});

function PillButton({ onClick, label, tip, className, disabled, children }: { onClick(): void; label?: string; tip?: string; className?: string; disabled?: boolean; children: ReactNode }) {
  const button = (
    <button
      type="button"
      aria-label={label}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "inline-flex h-6 items-center gap-1 rounded-full border bg-background px-2.5 font-medium text-tag outline-none transition-colors hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50",
        className,
      )}
    >
      {children}
    </button>
  );
  return tip ? <Tip label={tip}>{button}</Tip> : button;
}

// PauseButton presses Esc for the agent: its turn ends where it is, and
// it waits for the next prompt (a prompt to carry on picks it up again).
function PauseButton({ box, session, who }: { box: string; session: string; who: string }) {
  const [busy, setBusy] = useState(false);
  const pause = async () => {
    setBusy(true);
    try {
      const r = await interrupt(box, session);
      if (!r.stopped) toastManager.add({ type: "info", title: `${who} hasn't paused yet`, description: "Esc reached it, but its screen still shows it working. Open it to look." });
    } catch (err) {
      toastManager.add({ type: "error", title: `Couldn't pause ${who}`, description: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <PillButton onClick={() => void pause()} disabled={busy} tip="Ends its turn where it is (Esc). Prompt it to carry on.">
      <PauseIcon className="size-2.5" />
      Pause
    </PillButton>
  );
}

// Approval is Approve and Deny in the footer's place while the agent asks
// for a permission; the footer keeps its height, so nothing moves.
function Approval({ box, session, allow, deny }: { box: string; session: string; allow?: Choice; deny?: Choice }) {
  const send = useAnswer(box, session);
  if (!allow || !deny) return null;
  return (
    <>
      <Tip label={allow.title}>
        <button
          type="button"
          disabled={!!send.sent}
          onClick={() => void send.answer(allow, "Approved")}
          className="inline-flex h-6 items-center rounded-full bg-blue-600 px-3 font-medium text-tag text-white outline-none transition-colors hover:bg-blue-600/90 focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60"
        >
          Approve
        </button>
      </Tip>
      <Tip label={deny.title}>
        <button
          type="button"
          disabled={!!send.sent}
          onClick={() => void send.answer(deny, "Denied")}
          className="inline-flex h-6 items-center rounded-full bg-secondary px-3 font-medium text-secondary-foreground text-tag outline-none transition-colors hover:bg-secondary/80 focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60"
        >
          Deny
        </button>
      </Tip>
    </>
  );
}

// useAnswer types one of the agent's numbered options into its session,
// which is how Claude Code takes an answer.
function useAnswer(box: string, session: string) {
  const client = useStore((s) => s.client);
  const [sent, setSent] = useState<string>();
  const answer = async (c: Choice, said?: string) => {
    if (!client) return;
    setSent(c.key);
    try {
      // The person is answering the question, so the box may type into it.
      await boxApi.send(client, box, session, c.key, false, { when: "now", force: true });
      toastManager.add({ title: said ?? `Answered ${c.key}`, description: c.title ?? c.label, type: "success" });
    } catch (err) {
      setSent(undefined);
      toastManager.add({ title: "Couldn't answer", description: errorMessage(err), type: "error" });
    }
  };
  return { sent, answer };
}

// Answers are the agent's numbered options as buttons, for a question that
// is not a plain approval.
function Answers({ box, session, choices }: { box: string; session: string; choices: Choice[] }) {
  const { sent, answer } = useAnswer(box, session);
  return (
    <div className="mt-2 flex flex-wrap gap-1" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
      {choices.map((c) => (
        // The whole answer when it is cut short.
        <Tip key={c.key} label={c.label.length > 28 ? c.label : undefined} wrapClassName="max-w-full">
          <button
            type="button"
            disabled={!!sent}
            onClick={() => void answer(c)}
            className={cn(
              "inline-flex h-6 max-w-full items-center gap-1.5 rounded-md border bg-background px-2 text-tag transition-colors hover:border-ring/50 disabled:opacity-50",
              sent === c.key && "border-success/50 text-success",
            )}
          >
            <span className="font-mono text-muted-foreground">{c.key}</span>
            <span className="truncate">{c.label.length > 28 ? `${c.label.slice(0, 27)}…` : c.label}</span>
          </button>
        </Tip>
      ))}
    </div>
  );
}
