import { ArrowUpRightIcon, CircleAlertIcon, CircleCheckIcon, CircleIcon, HourglassIcon, PauseIcon, SquareIcon } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { type KeyboardEvent, memo, useEffect, useRef, useState } from "react";

import { AgentIcon } from "@/components/agent-glyph";
import { openOrchestrate, SessionActions } from "@/components/orchestrate/session-actions";
import { Tip } from "@/components/tip";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Kbd } from "@/components/ui/kbd";
import { toastManager } from "@/components/ui/toast";
import type { SessionEntry } from "@/hooks/use-agent-counts";
import { isMock } from "@/hooks/use-berth-connection";
import { approvalLabel, askOf, doneLine, isApproval, type Pill, type RunStep, stepTime } from "@/lib/agent-run";
import { boxApi } from "@/lib/api";
import { interrupt, modelLabel } from "@/lib/chat-controls";
import { agentOf, atQuestion, ON_ITS_WORK } from "@/lib/derive";
import { errorMessage } from "@/lib/format";
import { permissionChoices } from "@/lib/screen";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { focusSession } from "@/lib/workspaces";
import { duration, useNow } from "@/views/dashboard/agent-card";
import { describeAgent, startedAt } from "@/views/dashboard/names";
import { confirmStop, StopMenuItems } from "@/views/dashboard/stop";
import { useCardRun } from "@/views/dashboard/use-card-run";
import { type Choice, useScreenTail } from "@/views/dashboard/use-screen-tail";

// The card follows devl's agent components (devl.dev, on coss ui like the
// app): Run List's status icon and clock at the head, Working Out Loud's
// rail of steps, and Go / No-go's approval with the exact command and the
// Y and N keys. The data is the app's own (lib/agent-run).

// The most finished steps a card lists: the latest ones.
const LOG_ROWS = 3;

// A new step's words roll in from below.
const SPRING = { type: "spring", stiffness: 420, damping: 36, mass: 0.8 } as const;

// clock is a running time as devl's Run List shows it, "1:05" and "14:05",
// and past an hour the board's own "1h 15m".
function clock(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s >= 3600) return duration(ms);
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

type Tone = "work" | "wait" | "done" | "rest";

// StatusCard is one agent on the board as a live status card (Settings ›
// Agents › Agent Dashboard › Live status cards): its title and where it
// works, how far through its task list it is, the steps it took this turn
// on a rail ending in the one it is on, and what you can do about it (pause
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
  const asked = session.ask?.tool ?? run?.ask?.tool;
  const approval = waiting && isApproval(asked) ? permissionChoices(choices) : undefined;
  const allow = approval?.find((c) => c.label === "Allow");
  const deny = approval?.find((c) => c.label === "Deny");
  const approving = !!allow && !!deny;
  const answer = useAnswer(box, session.name);
  // A form of questions (AskUserQuestion, or one its record shows) isn't
  // answered by one number: it is answered in the agent's own view.
  const form = waiting && (session.ask?.tool === "AskUserQuestion" || (pending?.kind === "ask" && !run?.ask));
  const asking = waiting && !approving && !form && choices.length > 0;
  const onItsWork = state === "running" && !!session.unfinished;
  const parked = state === "ready" && !!session.parked;
  const tone: Tone = onItsWork || parked || state === "ready" ? "rest" : state === "running" ? "work" : state === "waiting" ? "wait" : "done";

  // What it does now, what it asks, or how its turn went: the rail's last
  // row. An approval has its own block instead; a plain ready agent has
  // nothing to say.
  const doing: { text: string; tip?: string; since?: number } | undefined = approving
    ? undefined
    : onItsWork
      ? { text: "Waiting on its work", tip: ON_ITS_WORK }
      : parked
        ? { text: "Parked: waiting on a reply or a later time", tip: "Its work is paused until someone else replies or a later time. Nothing runs and nothing needs you; it picks up by itself." }
        : state === "running"
          ? { text: run?.active?.label ?? "Working", since: run?.active?.since }
          : state === "waiting"
            ? { text: session.ask ? approvalLabel(session.ask.tool, session.ask.input, session.ask.message) : (pending?.label ?? (session.soft_wait ? "Waiting on your reply" : "Needs you")) }
            : state === "finished"
              ? { text: run ? doneLine(run, since) : "Done", tip: `Finished ${new Date(since).toLocaleString()}` }
              : undefined;
  const ask = approving ? askOf(asked, session.ask?.input ?? run?.ask?.detail) : undefined;

  const steps = run?.steps.filter((s) => s.status === "done").slice(-LOG_ROWS) ?? [];
  const lines = !run && state !== "ready" ? (asking ? (tail ?? []).filter((l) => !/^\s*(?:[❯›>]\s*)?\d[.)]\s/.test(l)) : tail) : undefined;
  const plan = run?.plan;
  const progress = plan ? (state === "finished" ? `${plan.done} of ${plan.total} steps` : `Step ${plan.current} of ${plan.total}`) : undefined;

  // Go / No-go: Y approves and N denies while the card has focus.
  const onKeyDown = (e: KeyboardEvent<HTMLElement>) => {
    if (e.target !== e.currentTarget) return;
    if (e.key === "Enter") selecting && onSelect ? onSelect() : open();
    else if (approving && !answer.sent && !e.metaKey && !e.ctrlKey && !e.altKey && (e.key === "y" || e.key === "n")) {
      e.preventDefault();
      void (e.key === "y" ? answer.answer(allow, "Approved") : answer.answer(deny, "Denied"));
    }
  };

  return (
    <article
      ref={ref}
      role="button"
      tabIndex={0}
      aria-pressed={selecting ? !!selected : undefined}
      data-state={state}
      onClick={(e) => (onSelect && (selecting || e.metaKey || e.shiftKey) ? onSelect() : open())}
      onKeyDown={onKeyDown}
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
          <StatusIcon tone={tone} hourglass={onItsWork || parked} />
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
              on the rail, and the corner holds only the menu. */}
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
                  {clock(now - since)}
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
          {/* Where first, as the board is scanned by it; the agent and its
              model last, the first to go in a narrow column. */}
          <span className="min-w-0 flex-1 truncate">
            {work && `${place} · `}
            {box}
            {where && !(work && where.worktree.main) && ` · ${where.location.name}`}
            {crowded && ` · started ${startedAt(session.created)}`}
            {where?.worktree.branch && where.worktree.branch !== place && <span className="opacity-70"> · {where.worktree.branch}</span>}
            {" · "}
            <AgentIcon agent={agentOf(session)} className="mr-1 inline size-3 align-[-2px]" />
            {model ? modelLabel(model) : agent}
          </span>
          {progress && <span className="shrink-0 tabular-nums">{progress}</span>}
        </div>

        {plan && <Pills pills={plan.pills} live={state === "running"} />}

        {(steps.length > 0 || doing) && (
          <ol aria-label="Steps" className="mt-2.5">
            {steps.map((s, i) => (
              <StepRow key={s.id} step={s} line={i < steps.length - 1 || !!doing} />
            ))}
            {doing && (
              <Tip label={doing.tip}>
                <li className="flex">
                  <Now text={doing.text} tone={tone} since={doing.since} now={now} hourglass={onItsWork || parked} />
                </li>
              </Tip>
            )}
          </ol>
        )}

        {ask && (
          <div data-testid="card-now" data-status="waiting" className="mt-2.5 pl-7">
            <p className="text-desc">
              <span className="font-medium">{agent}</span>
              <span className="text-muted-foreground"> wants to </span>
              {ask.action}
            </p>
            {ask.detail && <div className="mt-1.5 line-clamp-3 wrap-anywhere rounded-lg bg-muted px-2.5 py-1.5 font-mono text-tag">{ask.detail}</div>}
          </div>
        )}

        {prompt && !run && (!work || !prompt.startsWith(work.replace(/…$/, ""))) && (
          <div className="mt-1 truncate pl-7 text-meta text-foreground/70 italic" title={prompt}>
            “{prompt}”
          </div>
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

      <footer className="mt-2.5 flex h-9 items-center gap-1.5 border-t px-2.5" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
        {!selecting &&
          (approving ? (
            <>
              <Tip label={deny?.title}>
                <Button size="xs" variant="outline" disabled={!!answer.sent} onClick={() => deny && void answer.answer(deny, "Denied")}>
                  Deny
                  <Kbd>N</Kbd>
                </Button>
              </Tip>
              <Tip label={allow?.title}>
                <Button size="xs" disabled={!!answer.sent} onClick={() => allow && void answer.answer(allow, "Approved")}>
                  Approve
                  <Kbd className="bg-current/20 text-current">Y</Kbd>
                </Button>
              </Tip>
            </>
          ) : state === "running" ? (
            <>
              {canPause && !onItsWork && <PauseButton box={box} session={session.name} who={agent} />}
              <StopButton onClick={() => confirmStop(entry)} label={`Stop ${name} in ${place}`} />
            </>
          ) : state === "waiting" || state === "ready" ? (
            <>
              {form ? (
                <Button size="xs" onClick={open}>
                  Answer
                  <ArrowUpRightIcon />
                </Button>
              ) : (
                <Button size="xs" variant="outline" onClick={() => openOrchestrate("send", box, session.name)}>
                  {state === "ready" ? "Prompt…" : "Reply…"}
                </Button>
              )}
              <StopButton onClick={() => confirmStop(entry)} label={`Stop ${name} in ${place}`} />
            </>
          ) : null)}
        <Button size="xs" variant="ghost" className="-mr-1 ml-auto text-muted-foreground" onClick={open}>
          Open run
          <ArrowUpRightIcon />
        </Button>
      </footer>
    </article>
  );
}

// StatusIcon is the card's state at its head, as devl's Run List draws it:
// a turning arc while it works, an alert while it waits on you, a check
// once done; an hourglass while its work runs on without it, or it is
// parked; an empty ring at rest.
function StatusIcon({ tone, hourglass }: { tone: Tone; hourglass: boolean }) {
  return (
    <span className="inline-flex size-5 shrink-0 items-center justify-center" role="img" aria-label={hourglass ? "Waiting on its work" : { work: "Working", wait: "Needs you", done: "Done", rest: "Ready" }[tone]}>
      {hourglass ? (
        <HourglassIcon className="size-3.5 text-muted-foreground" aria-hidden />
      ) : tone === "work" ? (
        <svg aria-hidden className="size-4 animate-spin text-info motion-reduce:animate-none" viewBox="0 0 16 16">
          <circle cx="8" cy="8" fill="none" opacity="0.2" r="6" stroke="currentColor" strokeWidth="2" />
          <path d="M8 2a6 6 0 0 1 6 6" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="2" />
        </svg>
      ) : tone === "wait" ? (
        <CircleAlertIcon className="size-4 text-warning" aria-hidden />
      ) : tone === "done" ? (
        <CircleCheckIcon className="size-4 text-success" aria-hidden />
      ) : (
        <CircleIcon className="size-4 text-muted-foreground/60" aria-hidden />
      )}
    </span>
  );
}

// Mark is a step's place on the rail, as devl's Working Out Loud draws it:
// filled with a check once done, a turning edge for the one under way.
function Mark({ state, tone }: { state: "done" | "now"; tone?: Tone }) {
  return (
    <span className="relative z-10 inline-flex size-3.5 shrink-0 items-center justify-center" aria-hidden>
      {state === "done" ? (
        // Softer than devl's solid green: a board holds many of these.
        <span className="flex size-3.5 items-center justify-center rounded-full bg-success/45">
          <svg className="size-2.5 text-white" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth={3.2} viewBox="0 0 24 24">
            <path d="M5 12.5 10 17.5 19 7" />
          </svg>
        </span>
      ) : tone === "work" ? (
        <>
          <span className="absolute inset-0 rounded-full border-[1.5px] border-border" />
          <span className="absolute inset-0 animate-spin rounded-full border-[1.5px] border-transparent border-t-foreground [animation-duration:0.8s] motion-reduce:animate-none" />
        </>
      ) : tone === "wait" ? (
        <span className="size-2 rounded-full bg-warning" />
      ) : tone === "done" ? (
        <span className="size-2 rounded-full bg-success" />
      ) : (
        <span className="size-2 rounded-full bg-muted-foreground/50" />
      )}
    </span>
  );
}

// StepRow is a step it took this turn, quiet on the rail above what it does
// now: in the past tense, with how long it took, and the rail running on to
// the next. A new one fades in; the one it pushes off the top leaves at
// once, so a full log never holds an extra row and the card keeps its
// height as steps roll by. Memo: the board's clock ticks every card once a
// second, and a finished step has nothing to redraw.
const StepRow = memo(function StepRow({ step, line }: { step: RunStep; line: boolean }) {
  const reduce = useReducedMotion();
  return (
    <motion.li
      initial={reduce ? false : { opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={SPRING}
      data-status={step.status}
      className="relative flex h-5 items-center gap-2 text-muted-foreground text-tag"
    >
      {line && <span aria-hidden className="absolute top-[13px] -bottom-[3px] left-[9.5px] w-px bg-success/30" />}
      <span className="inline-flex w-5 shrink-0 justify-center">
        <Mark state="done" />
      </span>
      <span className="min-w-0 flex-1 truncate">{step.label}</span>
      {step.ms !== undefined && <span className="shrink-0 font-mono tabular-nums opacity-80">{stepTime(step.ms)}</span>}
    </motion.li>
  );
});

// Now is the rail's last row: what the agent does now, rolling in from
// below when it changes, with the step's own clock; what it waits on; or
// how its turn went.
function Now({ text, tone, since, now, hourglass }: { text: string; tone: Tone; since?: number; now: number; hourglass: boolean }) {
  const reduce = useReducedMotion();
  return (
    <span
      data-testid="card-now"
      data-status={tone === "wait" ? "waiting" : tone === "done" ? "done" : "active"}
      className="relative flex h-6 min-w-0 flex-1 items-center gap-2 overflow-hidden text-desc text-foreground/90"
    >
      <span className="inline-flex w-5 shrink-0 justify-center">{hourglass ? <HourglassIcon className="size-3.5 text-muted-foreground" aria-hidden /> : <Mark state="now" tone={tone} />}</span>
      <AnimatePresence initial={false} mode="popLayout">
        <motion.span
          key={text}
          className="min-w-0 flex-1 truncate"
          initial={reduce ? false : { y: "100%", opacity: 0 }}
          animate={{ y: 0, opacity: 1 }}
          exit={reduce ? undefined : { y: "-100%", opacity: 0 }}
          transition={SPRING}
        >
          {text}
        </motion.span>
      </AnimatePresence>
      {since !== undefined && <span className="shrink-0 font-mono text-tag text-muted-foreground tabular-nums">{clock(now - since)}</span>}
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

function StopButton({ onClick, label }: { onClick(): void; label: string }) {
  return (
    <Tip label="Stop agent…">
      <Button size="xs" variant="outline" aria-label={label} onClick={onClick}>
        <SquareIcon />
        Stop
      </Button>
    </Tip>
  );
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
    <Tip label="Ends its turn where it is (Esc). Prompt it to carry on.">
      <Button size="xs" variant="outline" disabled={busy} onClick={() => void pause()}>
        <PauseIcon />
        Pause
      </Button>
    </Tip>
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
