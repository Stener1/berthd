// node --experimental-strip-types --test src/lib/agent-run.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { approvalLabel, askOf, doneLine, MAX_PILLS, planOf, runOf, stepTime } from "./agent-run.ts";
import type { ChatTodo } from "./chat-controls.ts";
import type { TranscriptItem } from "./transcript.ts";

const T = 1_000_000;
const turn: TranscriptItem[] = [
  { kind: "user", id: "u0", text: "An earlier prompt" },
  { kind: "tools", id: "old", verb: "Read", done: true, items: [{ verb: "Read", target: "old.ts", at: T - 50_000 }] },
  { kind: "user", id: "u1", text: "Fix the webhook" },
  { kind: "text", id: "t1", text: "Starting from the failing test." },
  { kind: "tools", id: "g1", verb: "Read", done: true, items: [{ verb: "Read", target: "src/a.ts", at: T }, { verb: "Read", target: "src/b.ts", at: T + 500 }] },
  { kind: "tools", id: "g2", verb: "Search", done: true, items: [{ verb: "Search", target: "paymentId", at: T + 1900 }] },
  { kind: "edit", id: "e1", file: "src/webhook.ts", added: 3, removed: 1 },
  { kind: "edit", id: "e2", file: "src/retry.ts", added: 9, removed: 0 },
  { kind: "edit", id: "e3", file: "src/webhook.ts", added: 1, removed: 1 },
  { kind: "tools", id: "g3", verb: "Run", done: false, items: [{ verb: "Run", target: "pnpm test payments", at: T + 6000 }] },
];

test("a working turn: finished steps in the past tense with their lengths, the live one in the present", () => {
  const run = runOf(turn, undefined, { working: true, since: T - 1000 });
  assert.deepEqual(
    run.steps.map((s) => [s.label, s.status, s.ms]),
    [
      ["Read 2 files", "done", 1900],
      ["Searched for paymentId", "done", 4100],
      ["Edited 2 files", "done", undefined],
      ["Running pnpm test payments", "active", undefined],
    ],
  );
  assert.equal(run.active?.label, "Running pnpm test payments");
  assert.equal(run.active?.since, T + 6000);
  // Only this turn's: the earlier prompt's read is not in it.
  assert.equal(run.files, 2);
  assert.equal(run.began, T);
});

test("between calls it is thinking, timed from its last write", () => {
  const items = turn.slice(0, -1);
  const run = runOf(items, undefined, { working: true, since: T - 1000, wrote: T + 4000 });
  assert.equal(run.active?.label, "Thinking");
  assert.equal(run.active?.since, T + 4000);
  assert.equal(run.steps.length, 4);
});

test("a finished turn has no step under way and says how long it took", () => {
  const run = runOf(turn, undefined, { working: false, since: T + 13_400 });
  assert.equal(run.active, undefined);
  assert.ok(run.steps.every((s) => s.status === "done"));
  assert.equal(doneLine(run, T + 13_400), "Done in 13s · 2 files changed");
  assert.equal(doneLine(runOf([{ kind: "text", id: "x", text: "hi" }], undefined, { working: false, since: T }), T), "Done");
});

test("a timed prompt starts the turn, and a timed edit is a timed step", () => {
  const items: TranscriptItem[] = [
    { kind: "user", id: "u", text: "Go", at: T - 2000 },
    { kind: "tools", id: "g", verb: "Read", done: true, items: [{ verb: "Read", target: "a.ts", at: T }] },
    { kind: "edit", id: "e", file: "a.ts", added: 1, removed: 0, at: T + 3000 },
    { kind: "tools", id: "r", verb: "Run", done: true, items: [{ verb: "Run", target: "pnpm test", at: T + 4200 }] },
  ];
  const run = runOf(items, undefined, { working: false, since: T + 9000 });
  assert.equal(run.began, T - 2000);
  assert.deepEqual(
    run.steps.map((s) => s.ms),
    [3000, 1200, 4800],
  );
  assert.equal(doneLine(run, T + 9000), "Done in 11s · 1 file changed");
});

test("thinking is a step only while it is the one under way", () => {
  const items: TranscriptItem[] = [
    { kind: "thinking", id: "k1", since: T },
    { kind: "tools", id: "g", verb: "Read", done: true, items: [{ verb: "Read", target: "a.ts", at: T + 1000 }] },
    { kind: "thinking", id: "k2", since: T + 2000 },
  ];
  assert.deepEqual(
    runOf(items, undefined, { working: true, since: T }).steps.map((s) => s.label),
    ["Read a.ts", "Thinking"],
  );
  assert.deepEqual(
    runOf(items, undefined, { working: false, since: T + 5000 }).steps.map((s) => s.label),
    ["Read a.ts"],
  );
});

test("an open group the record left behind is not the step under way", () => {
  const items: TranscriptItem[] = [
    { kind: "tools", id: "a", verb: "Run", done: false, items: [{ verb: "Run", target: "pnpm dev", at: T }] },
    { kind: "tools", id: "b", verb: "Read", done: false, items: [{ verb: "Read", target: "x.ts", at: T + 100 }] },
  ];
  const run = runOf(items, undefined, { working: true, since: T });
  assert.deepEqual(
    run.steps.map((s) => s.status),
    ["done", "active"],
  );
  assert.equal(run.active?.label, "Reading x.ts");
});

test("an approval waits in the log, in words", () => {
  const items: TranscriptItem[] = [...turn.slice(0, -1), { kind: "ask", id: "a1", tool: "Bash", detail: "git push origin fix" }];
  const run = runOf(items, undefined, { working: false, waiting: true, since: T });
  assert.deepEqual(run.steps.at(-1), { id: "a1", kind: "ask", label: "Needs your approval to run git push origin fix", status: "waiting" });
  assert.equal(run.ask?.tool, "Bash");
  // A turn that ended with it still open no longer waits on it.
  const ended = runOf(items, undefined, { working: false, since: T });
  assert.ok(ended.steps.every((s) => s.status === "done"));
  assert.equal(ended.ask, undefined);
  assert.equal(approvalLabel("Edit", "apps/web/lib/webhook.ts"), "Needs your approval to edit webhook.ts");
  assert.equal(approvalLabel("mcp__linear__save_issue", "{}"), "Needs your approval to use save issue");
  assert.equal(approvalLabel("AskUserQuestion", undefined, "Three questions about the release"), "Three questions about the release");
  assert.equal(approvalLabel(undefined, undefined), "Needs you");
});

const todos = (s: string): ChatTodo[] => [...s].map((c, i) => ({ text: `Task ${i}`, status: c === "x" ? "completed" : c === ">" ? "in_progress" : "pending" }));

test("the task list is the bar's pills and the step it is on", () => {
  assert.equal(planOf(undefined), undefined);
  assert.deepEqual(planOf(todos("xx>...")), { done: 2, total: 6, current: 3, pills: ["done", "done", "active", "todo", "todo", "todo"] });
  assert.deepEqual(planOf(todos("xxxxxx"))?.current, 6);
  // Nothing started yet: it is on the first.
  assert.equal(planOf(todos("...."))?.current, 1);
});

test("a long task list shares the pills out", () => {
  const plan = planOf(todos("xxxxxxxxxx>.........."));
  assert.equal(plan?.pills.length, MAX_PILLS);
  assert.equal(plan?.total, 21);
  assert.equal(plan?.current, 11);
  assert.deepEqual(plan?.pills, ["done", "done", "done", "done", "active", "todo", "todo", "todo"]);
});

test("step lengths read as people say them", () => {
  assert.equal(stepTime(1940), "1.9s");
  assert.equal(stepTime(13_400), "13s");
  assert.equal(stepTime(72_000), "1m 12s");
  assert.equal(stepTime(3_725_000), "1h 2m");
  assert.equal(stepTime(-5), "0.0s");
});

test("a permission reads as what the agent wants to do, with what it would run or touch as it is", () => {
  assert.deepEqual(askOf("Bash", "  git push origin fix\n"), { action: "run a command", detail: "git push origin fix" });
  assert.deepEqual(askOf("Edit", "apps/web/lib/webhook.ts"), { action: "edit a file", detail: "apps/web/lib/webhook.ts" });
  assert.deepEqual(askOf("mcp__linear__save_issue", undefined), { action: "use save issue (linear)", detail: undefined });
  assert.deepEqual(askOf("ExitPlanMode", "the plan"), { action: "start on its plan" });
  assert.deepEqual(askOf("Frobnicate", ""), { action: "use Frobnicate", detail: undefined });
});
