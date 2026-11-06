// An agent's current turn as the dashboard's card tells it: the steps it
// took (past tense, with how long each took) and the one it is on (present
// tense, timed live), how far through its task list it is, what it asks
// you to approve, and once done how long the turn took and how many files
// it changed. Read from the agent's own record (lib/transcript) and its
// signals (lib/chat-controls); nothing here is kept.

import type { ChatTodo } from "./chat-controls.ts";
import type { TranscriptItem } from "./transcript.ts";

export type StepKind = "read" | "search" | "run" | "edit" | "fetch" | "skill" | "agent" | "publish" | "think" | "ask";

export interface RunStep {
  id: string;
  kind: StepKind;
  label: string;
  status: "done" | "active" | "waiting";
  // When it began (ms), when the record says.
  since?: number;
  // How long a finished step took, when the record says when the next began.
  ms?: number;
}

export type Pill = "done" | "active" | "todo";

export interface RunPlan {
  done: number;
  total: number;
  // The step it is on, counted from 1 (total once all are done).
  current: number;
  pills: Pill[];
}

export interface Run {
  steps: RunStep[];
  // The step under way: the last one, while the agent works.
  active?: RunStep;
  plan?: RunPlan;
  // Files the turn edited or wrote.
  files: number;
  // When the turn began (ms): its prompt, or its first call. A finished
  // turn's length counts from it.
  began?: number;
  // The permission it waits on, from its record, until it is answered.
  ask?: { tool: string; detail: string };
}

// The most pills the bar draws; a longer task list shares them out.
export const MAX_PILLS = 8;

const base = (path: string) => path.split("/").filter(Boolean).pop() ?? path;
const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

function kindOf(verb: string): StepKind {
  switch (verb) {
    case "Read":
      return "read";
    case "Search":
      return "search";
    case "Edit":
      return "edit";
    case "Fetch":
      return "fetch";
    case "Skill":
      return "skill";
    default:
      return "run";
  }
}

// What a group of calls did, in the past tense: "Read webhook.ts", "Read 5
// files", "Ran pnpm test".
export function pastTense(verb: string, targets: string[]): string {
  const n = targets.length;
  const one = n === 1 ? targets[0] : undefined;
  switch (verb) {
    case "Read":
      return one ? `Read ${base(one)}` : `Read ${plural(n, "file")}`;
    case "Search":
      return one ? `Searched for ${one}` : `Searched ${plural(n, "time")}`;
    case "Run":
      return one ? `Ran ${one}` : `Ran ${plural(n, "command")}`;
    case "Edit":
      return one ? `Edited ${base(one)}` : `Edited ${plural(n, "file")}`;
    case "Fetch":
      return one ? `Fetched ${one}` : `Fetched ${plural(n, "page")}`;
    case "Skill":
      return one ? `Used the ${one} skill` : `Used ${plural(n, "skill")}`;
  }
  return one ? `${verb} ${one}` : `${verb} ${n}`;
}

// What a call under way does, in the present tense: "Running pnpm test".
export function presentTense(verb: string, target: string): string {
  switch (verb) {
    case "Read":
      return `Reading ${base(target)}`;
    case "Search":
      return `Searching for ${target}`;
    case "Run":
      return `Running ${target}`;
    case "Edit":
      return `Editing ${base(target)}`;
    case "Fetch":
      return `Fetching ${target}`;
    case "Skill":
      return `Using the ${target} skill`;
  }
  return `${verb} ${target}`;
}

// The current turn: what came after the last prompt, and when that was
// written (newer boxes).
function turnOf(items: TranscriptItem[]): { turn: TranscriptItem[]; at?: number } {
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (it.kind === "user") return { turn: items.slice(i + 1), at: it.at };
  }
  return { turn: items };
}

// stepsOf reads the turn's steps from its record, oldest first. Its words
// are skipped; the card shows what it did.
function stepsOf(turn: TranscriptItem[]): RunStep[] {
  const steps: RunStep[] = [];
  let edits: { step: RunStep; files: Set<string> } | undefined;
  for (const it of turn) {
    const last = steps[steps.length - 1];
    switch (it.kind) {
      case "tools": {
        const calls = it.items ?? [];
        if (!calls.length) break;
        const since = calls.find((c) => c.at)?.at;
        steps.push(
          it.done
            ? { id: it.id, kind: kindOf(it.verb), label: pastTense(it.verb, calls.map((c) => c.target)), status: "done", since }
            : { id: it.id, kind: kindOf(it.verb), label: presentTense(it.verb, calls[calls.length - 1].target), status: "active", since: calls[calls.length - 1].at ?? since },
        );
        break;
      }
      case "edit": {
        // Edits one after another are one step: "Edited 3 files".
        if (edits && last === edits.step) {
          edits.files.add(it.file);
          last.label = pastTense("Edit", [...edits.files]);
        } else {
          edits = { step: { id: it.id, kind: "edit", label: pastTense("Edit", [it.file]), status: "done", since: it.at }, files: new Set([it.file]) };
          steps.push(edits.step);
        }
        break;
      }
      case "crew":
        steps.push({ id: it.id, kind: "agent", label: `Started ${plural(it.names.length, "helper")}`, status: "done" });
        break;
      case "artifact":
        if (it.done && !it.error) steps.push({ id: it.id, kind: "publish", label: `Published ${it.text}`, status: "done" });
        break;
      case "thinking":
        steps.push({
          id: it.id,
          kind: it.step ? kindOf(it.step.verb) : "think",
          label: it.step ? presentTense(it.step.verb, it.step.target) : (it.label?.replace(/…$/, "") ?? "Thinking"),
          status: "active",
          since: it.since,
        });
        break;
      case "ask":
        if (!it.decided) steps.push({ id: it.id, kind: "ask", label: approvalLabel(it.tool, it.detail), status: "waiting" });
        break;
      case "question":
        if (!it.done) steps.push({ id: it.id, kind: "ask", label: it.questions.length === 1 ? "Asking you a question" : `Asking you ${it.questions.length} questions`, status: "waiting" });
        break;
    }
  }
  // A step's length runs to the start of the next step that has one.
  for (let i = 0; i < steps.length - 1; i++) {
    const a = steps[i];
    const next = steps.slice(i + 1).find((b) => b.since)?.since;
    if (a.status === "done" && a.since && next && next >= a.since) a.ms = next - a.since;
  }
  // Only the last step can be under way: an earlier open group was left
  // behind by the record (a call it never closed), and thinking that came
  // to something is that something's.
  for (let i = 0; i < steps.length - 1; i++) if (steps[i].status !== "done") steps[i] = { ...steps[i], status: "done" };
  return steps.filter((s, i) => s.kind !== "think" || i === steps.length - 1);
}

// planOf is the agent's task list as the bar's pills and "Step N of M".
export function planOf(todos: ChatTodo[] | undefined): RunPlan | undefined {
  if (!todos?.length) return undefined;
  const total = todos.length;
  const done = todos.filter((t) => t.status === "completed").length;
  const working = todos.some((t) => t.status === "in_progress");
  const current = Math.min(total, done + (working ? 1 : 0)) || 1;
  const each: Pill[] = todos.map((t) => (t.status === "completed" ? "done" : t.status === "in_progress" ? "active" : "todo"));
  if (total <= MAX_PILLS) return { done, total, current, pills: each };
  // A long list: each pill stands for a share of it, done when all of its
  // share is.
  const pills: Pill[] = [];
  for (let i = 0; i < MAX_PILLS; i++) {
    const part = each.slice(Math.floor((i * total) / MAX_PILLS), Math.floor(((i + 1) * total) / MAX_PILLS));
    pills.push(part.every((p) => p === "done") ? "done" : part.some((p) => p !== "todo") ? "active" : "todo");
  }
  return { done, total, current, pills };
}

// What an approval asks for, in words: "Needs your approval to run git push".
export function approvalLabel(tool: string | undefined, input: string | undefined, message?: string): string {
  const what = input?.split("\n")[0].trim();
  const clip = (s: string) => (s.length > 60 ? `${s.slice(0, 59)}…` : s);
  const mcp = tool ? /^mcp__(.+?)__(.+)$/.exec(tool) : null;
  if (mcp) return `Needs your approval to use ${mcp[2].replaceAll("_", " ")}`;
  switch (tool) {
    case "Bash":
      return what ? `Needs your approval to run ${clip(what)}` : "Needs your approval to run a command";
    case "Edit":
    case "MultiEdit":
    case "NotebookEdit":
      return what ? `Needs your approval to edit ${base(what)}` : "Needs your approval to edit a file";
    case "Write":
      return what ? `Needs your approval to write ${base(what)}` : "Needs your approval to write a file";
    case "Read":
      return what ? `Needs your approval to read ${base(what)}` : "Needs your approval to read a file";
    case "WebFetch":
      return what ? `Needs your approval to fetch ${clip(what)}` : "Needs your approval to fetch a page";
    case "WebSearch":
      return "Needs your approval to search the web";
    case "ExitPlanMode":
      return "Has a plan ready for your approval";
    case "AskUserQuestion":
      return message || "Has a question for you";
    case undefined:
    case "":
      return message || "Needs you";
  }
  return `Needs your approval to use ${tool}`;
}

// askOf is a permission in the words the card uses under the agent's name,
// "<agent> wants to run a command", with what it would run or touch
// (detail) to show as it is.
export function askOf(tool: string | undefined, input: string | undefined): { action: string; detail?: string } {
  const detail = input?.trim() || undefined;
  const mcp = tool ? /^mcp__(.+?)__(.+)$/.exec(tool) : null;
  if (mcp) return { action: `use ${mcp[2].replaceAll("_", " ")} (${mcp[1]})`, detail };
  switch (tool) {
    case "Bash":
      return { action: "run a command", detail };
    case "Edit":
    case "MultiEdit":
    case "NotebookEdit":
      return { action: "edit a file", detail };
    case "Write":
      return { action: "write a file", detail };
    case "Read":
      return { action: "read a file", detail };
    case "WebFetch":
      return { action: "fetch a page", detail };
    case "WebSearch":
      return { action: "search the web", detail };
    case "ExitPlanMode":
      return { action: "start on its plan" };
  }
  return { action: `use ${tool ?? "a tool"}`, detail };
}

// isApproval says an ask is a permission to approve or deny, not a
// question to answer in words or by picking.
export const isApproval = (tool: string | undefined) => !!tool && tool !== "AskUserQuestion";

// runOf is the turn so far. wrote is when the agent last wrote to its
// record (ms): with no call under way, it has been thinking since.
export function runOf(items: TranscriptItem[] | undefined, todos: ChatTodo[] | undefined, opts: { working: boolean; waiting?: boolean; since: number; wrote?: number }): Run {
  const { turn, at } = turnOf(items ?? []);
  // A question or permission left open by a turn that ended without it
  // waits for no one: it isn't a step.
  const steps = stepsOf(turn).filter((s) => s.status !== "waiting" || opts.waiting);
  const files = new Set(turn.flatMap((it) => (it.kind === "edit" ? [it.file] : []))).size;
  // From its prompt; a box that doesn't time prompts, from its first call.
  let began = at;
  if (!began) for (const it of turn) if (it.kind === "tools") for (const c of it.items ?? []) if (c.at && (!began || c.at < began)) began = c.at;
  const open = turn[turn.length - 1];
  const ask = opts.waiting && open?.kind === "ask" && !open.decided ? { tool: open.tool, detail: open.detail } : undefined;
  const last = steps[steps.length - 1];
  if (!opts.working) {
    // Nothing is under way once its turn ended, and its last step ran
    // until it did.
    if (last?.kind === "think") steps.pop();
    else if (last && last.status === "active") steps[steps.length - 1] = { ...last, status: "done" };
    const end = steps[steps.length - 1];
    if (end && end.ms === undefined && end.since && opts.since >= end.since) end.ms = opts.since - end.since;
    return { steps, plan: planOf(todos), files, began, ask };
  }
  let active = last && last.status !== "done" ? last : undefined;
  if (!active) {
    // Between calls: it thinks, since its record last changed.
    active = { id: "live:thinking", kind: "think", label: "Thinking", status: "active", since: Math.max(opts.wrote ?? 0, opts.since) || undefined };
    steps.push(active);
  }
  return { steps, active, plan: planOf(todos), files, began };
}

// stepTime is a step's length as its row shows it: "1.9s", "13s", "1m 12s".
export function stepTime(ms: number): string {
  if (ms < 0) ms = 0;
  if (ms < 10_000) return `${(Math.floor(ms / 100) / 10).toFixed(1)}s`;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

// doneLine is a finished turn's headline: "Done in 13s · 6 files changed".
export function doneLine(run: Run, ended: number): string {
  const took = run.began && ended >= run.began ? ` in ${stepTime(ended - run.began).replace(/\.\ds$/, "s")}` : "";
  return `Done${took}${run.files ? ` · ${plural(run.files, "file")} changed` : ""}`;
}
