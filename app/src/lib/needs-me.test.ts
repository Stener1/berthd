import assert from "node:assert/strict";
import test from "node:test";

import { validateCard } from "./cards.ts";
import { type BoxWork, buildCard, buildQueue, due, laterList, leftLine, type QueueInput, sessionTree, type SessionEntry, walkOrder, type WorkRecord, type WorkSession } from "./needs-me.ts";

const NOW = Date.parse("2026-11-10T12:00:00Z");
const ago = (h: number) => new Date(NOW - h * 3_600_000).toISOString();

const session = (name: string, over: Partial<WorkSession> = {}): WorkSession => ({ name, dir: `/w/${name}`, created: ago(200), attached: 0, exited: false, agent: "claude", title: name, state_since: ago(1), ...over });
const entry = (name: string, state: string, over: Partial<WorkSession> = {}, box = "devl"): SessionEntry => ({ box, state: state as SessionEntry["state"], session: session(name, { agent_state: state as WorkSession["agent_state"], ...over }) });
const record = (id: string, names: string[], over: Partial<WorkRecord> = {}): WorkRecord => ({ id, title: id, status: "open", sessions: names.map((name) => ({ name })), created: ago(100), last_seen: ago(50), ...over });

const queue = (sessions: SessionEntry[], records: WorkRecord[] = [], pins: BoxWork["pins"] = [], over: Partial<QueueInput> = {}) => buildQueue({ sessions, work: { devl: { records, pins } }, now: NOW, ...over });
const titles = (q: { title: string }[]) => q.map((i) => i.title);

test("the order is pinned decisions, decisions, pinned tasks, tasks, results, then the deck", () => {
  const q = queue(
    [
      entry("result", "finished"),
      entry("task", "waiting", { agent_sub_state: "act" }),
      entry("pinned-task", "waiting", { agent_sub_state: "act" }),
      entry("decision", "waiting", { agent_sub_state: "decision" }),
      entry("pinned-decision", "waiting"),
    ],
    [record("result", ["result"]), record("task", ["task"]), record("pinned-task", ["pinned-task"], { pinned: true }), record("decision", ["decision"]), record("pinned-decision", ["pinned-decision"], { pinned: true }), record("stale", ["gone"], { last_seen: ago(24 * 5) })],
  );
  assert.deepEqual(titles(q), ["pinned-decision", "decision", "pinned-task", "task", "result", "stale"]);
  assert.deepEqual(q.map((i) => i.kind), ["decision", "decision", "task", "task", "result", "close-out"]);
});

test("within a pass, what has gone longest unseen comes first", () => {
  const q = queue([entry("new", "waiting"), entry("old", "waiting")], [record("new", ["new"], { last_seen: ago(1) }), record("old", ["old"], { last_seen: ago(90) })]);
  assert.deepEqual(titles(q), ["old", "new"]);
});

test("a waiting session with no sub-state is a decision, and act is a task", () => {
  assert.equal(queue([entry("a", "waiting")], [record("a", ["a"])])[0].kind, "decision");
  assert.equal(queue([entry("a", "waiting", { agent_sub_state: "someone-elses-state" })], [record("a", ["a"])])[0].kind, "decision");
  assert.equal(queue([entry("a", "waiting", { agent_sub_state: "act" })], [record("a", ["a"])])[0].kind, "task");
});

test("queued and parked sessions stay out", () => {
  assert.deepEqual(queue([entry("a", "waiting", { queued: 1 })], [record("a", ["a"])]), []);
  assert.deepEqual(queue([entry("a", "ready", { parked: true })], [record("a", ["a"])]), []);
  assert.deepEqual(queue([entry("a", "waiting")], [record("a", ["a"], { status: "parked" })]), []);
});

test("a running agent, a shell and a service never enter", () => {
  assert.deepEqual(queue([entry("a", "running")], [record("a", ["a"], { last_seen: ago(24 * 9) })]), []);
  assert.deepEqual(queue([entry("shell", "exited", { agent: undefined, command: "bash" })]), []);
  assert.deepEqual(queue([entry("svc", "waiting", { service: "web" })]), []);
});

test("a result enters until it's seen since it ended", () => {
  assert.equal(queue([entry("a", "finished", { state_since: ago(1) })], [record("a", ["a"])])[0].kind, "result");
  const seen = record("a", ["a"]);
  seen.sessions[0].last_seen = ago(0.5);
  assert.deepEqual(queue([entry("a", "finished", { state_since: ago(1) })], [seen]), []);
});

test("a session that ended on a question has a card with no form", () => {
  const q = queue([entry("a", "finished", { title: "❓ ACME-1 Sweep scope" })], [record("a", ["a"], { title: "Sweep scope" })]);
  assert.equal(q[0].kind, "decision");
  assert.equal(q[0].focus?.noForm, true);
  const blocked = queue([entry("a", "finished", { title: "⛔ ACME-1 no key" })], [record("a", ["a"])]);
  assert.equal(blocked[0].kind, "decision");
});

test("turn check's wait asks in its last message", () => {
  const q = queue([entry("a", "waiting", { soft_wait: true })], [record("a", ["a"])]);
  assert.equal(q[0].kind, "decision");
  assert.equal(q[0].focus?.noForm, true);
  assert.equal(q[0].why, "Claude Code asked in its last message");
});

test("Not now and Waiting on leave, and come back when due", () => {
  const later = record("a", ["a"], { status: "parked", not_until: ago(-2) });
  assert.deepEqual(queue([entry("a", "waiting")], [later]), []);
  const back = queue([entry("a", "waiting")], [{ ...later, not_until: ago(1) }]);
  assert.equal(back[0].kind, "decision");
  assert.equal(back[0].returned, true);

  const waits = record("a", ["a"], { status: "waiting", waiting_on: "Theo", check_back: ago(-2) });
  assert.deepEqual(queue([entry("a", "ready")], [waits]), []);
  const checked = queue([entry("a", "ready")], [{ ...waits, check_back: ago(1) }]);
  assert.equal(checked[0].kind, "task");
  assert.equal(checked[0].why, "Waiting on Theo: time to check back");
});

test("parked with no date stays parked", () => {
  assert.equal(due({ status: "parked" }, NOW), false);
  assert.deepEqual(queue([entry("a", "waiting")], [record("a", ["a"], { status: "parked" })]), []);
});

test("a closed record never enters, but it's still there", () => {
  const closed = record("a", ["a"], { status: "closed" });
  assert.deepEqual(queue([entry("a", "waiting")], [closed]), []);
});

test("the deck is what's stale for 3 days and on its way to nothing", () => {
  assert.equal(queue([entry("a", "ready")], [record("a", ["a"], { last_seen: ago(24 * 3 + 1) })])[0].kind, "close-out");
  assert.deepEqual(queue([entry("a", "ready")], [record("a", ["a"], { last_seen: ago(24 * 3 - 1) })]), []);
  assert.deepEqual(queue([entry("a", "running")], [record("a", ["a"], { last_seen: ago(24 * 9) })]), []);
  // An unread result is a result, however old.
  assert.equal(queue([entry("a", "finished", { state_since: ago(24 * 5) })], [record("a", ["a"], { last_seen: ago(24 * 9) })])[0].kind, "result");
});

test("work in a project kept off the board stays out, and doesn't look abandoned", () => {
  const stale = record("a", ["a"], { last_seen: ago(24 * 9) });
  assert.equal(queue([], [stale]).length, 1);
  assert.deepEqual(queue([], [stale], [], { offBoard: new Set(["devl/a"]) }), []);
});

test("a pin on a repo or a project reaches every box's cards", () => {
  const q = buildQueue({
    sessions: [entry("a", "waiting", { location: "shop/fix" }), entry("b", "waiting", { location: "evals" }), entry("c", "waiting", { location: "docs" }, "gpu")],
    work: { devl: { records: [record("a", ["a"]), record("b", ["b"])], pins: [{ kind: "repo", target: "docs" }] }, gpu: { records: [record("c", ["c"], { project: "Launch" })], pins: [{ kind: "project", target: "Launch" }] } },
    now: NOW,
  });
  assert.equal(q.find((i) => i.title === "c")?.pinned, true);
  assert.equal(q.find((i) => i.title === "a")?.pinned, false);
});

test("the count is the list: one card per piece of work, with its most urgent session in front", () => {
  const q = queue([entry("lead", "running"), entry("w1", "finished"), entry("w2", "waiting")], [record("job", ["lead", "w1", "w2"])]);
  assert.equal(q.length, 1);
  assert.equal(q[0].kind, "decision");
  assert.equal(q[0].focus?.name, "w2");
  assert.equal(q[0].members.length, 3);
});

test("an answer takes the card off at once, and comes back if it didn't take", () => {
  const e = entry("a", "waiting", { state_since: ago(1) });
  const answered = { "devl/a": { since: e.session.state_since, at: NOW - 5_000 } };
  assert.deepEqual(queue([e], [record("a", ["a"])], [], { answered }), []);
  assert.equal(queue([e], [record("a", ["a"])], [], { answered: { "devl/a": { since: e.session.state_since, at: NOW - 120_000 } } }).length, 1);
  assert.equal(queue([{ ...e, session: { ...e.session, state_since: ago(0) } }], [record("a", ["a"])], [], { answered }).length, 1);
});

test("a session no record holds yet still enters while it waits, but not as a result", () => {
  assert.equal(queue([entry("a", "waiting")])[0].key, "devl/s:a");
  assert.deepEqual(queue([entry("a", "finished")]), []);
  const box = buildQueue({ sessions: [entry("a", "waiting")], work: {}, now: NOW });
  assert.equal(box.length, 1);
});

test("a suggested group is a card of its own, for records still open", () => {
  const a = record("a", ["a"], { suggested_with: ["b", "gone"], last_seen: ago(1) });
  const b = record("b", ["b"], { last_seen: ago(1) });
  const q = queue([entry("a", "ready"), entry("b", "ready")], [a, b]);
  assert.deepEqual(q.map((i) => i.kind), ["group"]);
  assert.equal(q[0].others?.[0].id, "b");
  assert.deepEqual(queue([entry("a", "ready")], [a, { ...b, status: "closed" }]), []);
});

test("a new pinned question becomes next and doesn't move the card in front", () => {
  const before = queue([entry("a", "waiting"), entry("b", "waiting")], [record("a", ["a"], { last_seen: ago(90) }), record("b", ["b"], { last_seen: ago(80) })]);
  const current = walkOrder(before, undefined)[0].key;
  const after = queue([entry("a", "waiting"), entry("b", "waiting"), entry("new", "waiting")], [record("a", ["a"], { last_seen: ago(90) }), record("b", ["b"], { last_seen: ago(80) }), record("new", ["new"], { pinned: true, last_seen: ago(1) })]);
  assert.deepEqual(titles(after), ["new", "a", "b"]);
  assert.deepEqual(titles(walkOrder(after, current)), ["a", "new", "b"]);
});

test("a card passed over waits behind the rest, and the front falls back when it's gone", () => {
  const q = queue([entry("a", "waiting"), entry("b", "waiting"), entry("c", "waiting")], [record("a", ["a"], { last_seen: ago(90) }), record("b", ["b"], { last_seen: ago(80) }), record("c", ["c"], { last_seen: ago(70) })]);
  assert.deepEqual(titles(walkOrder(q, "devl/c", ["devl/a"])), ["c", "b", "a"]);
  assert.deepEqual(titles(walkOrder(q, "devl/none")), ["a", "b", "c"]);
  assert.deepEqual(walkOrder([], undefined), []);
});

test("the header says how many and about how long", () => {
  assert.equal(leftLine([]), "Nothing left");
  assert.equal(leftLine([{ kind: "group" }]), "1 left, about 1 min");
  assert.equal(leftLine(Array.from({ length: 7 }, () => ({ kind: "decision" as const }))), "7 left, about 11 min");
});

test("a group draws as a tree from started_by, orchestrator on top", () => {
  const m = (name: string, startedBy?: string) => ({ box: "devl", name, title: name, startedBy });
  const tree = sessionTree([m("worker-2", "lead"), m("loner"), m("worker-1", "lead"), m("lead"), m("sub", "worker-1")]);
  assert.deepEqual(tree.map((t) => [t.member.name, t.depth]), [["lead", 0], ["worker-2", 1], ["worker-1", 1], ["sub", 2], ["loner", 0]]);
});

test("a parent outside the group makes a root, and a loop still shows everyone", () => {
  const m = (name: string, startedBy?: string) => ({ box: "devl", name, title: name, startedBy });
  assert.deepEqual(sessionTree([m("a", "elsewhere")]).map((t) => t.depth), [0]);
  assert.deepEqual(sessionTree([m("a", "b"), m("b", "a")]).map((t) => t.member.name).sort(), ["a", "b"]);
});

test("what's put off says whom it waits for, soonest back first, and never an internal status", () => {
  const later = laterList({ devl: { pins: [], records: [record("a", [], { status: "parked" }), record("b", [], { status: "waiting", waiting_on: "Theo", check_back: ago(-10) }), record("c", [], { status: "parked", not_until: ago(-2) }), record("d", [], { status: "parked", not_until: ago(1) })] } }, NOW);
  assert.deepEqual(later.map((l) => l.record.id), ["c", "b", "a"]);
  assert.match(later[1].line, /^Waiting on Theo, check back /);
  assert.equal(later[2].line, "Parked until you bring it back");
});

test("every card the queue builds is valid in the card format", () => {
  const q = queue(
    [entry("form", "waiting"), entry("soft", "waiting", { soft_wait: true }), entry("task", "waiting", { agent_sub_state: "act" }), entry("res", "finished"), entry("old", "ready")],
    [record("form", ["form"]), record("soft", ["soft"]), record("task", ["task"]), record("res", ["res"]), record("old", ["old"], { last_seen: ago(24 * 5), suggested_with: ["form"] })],
  );
  assert.ok(q.length >= 5);
  const facts = { form: { tool: "t1", questions: [{ question: "Which?", options: [{ label: "A" }] }] }, said: "Done.", choices: [{ key: "1", label: "Yes" }], asks: "Wants to use Bash" };
  for (const item of q) {
    for (const f of [{}, facts, { said: "x" }, { choices: facts.choices }]) {
      const r = validateCard(buildCard(item, f));
      assert.equal(r.ok, true, JSON.stringify(r));
    }
  }
});

test("a card's actions follow what it is", () => {
  const result = queue([entry("a", "finished")], [record("a", ["a"])])[0];
  assert.deepEqual(buildCard(result, { said: "Shipped." }).actions, ["reply", "read", "not-now", "waiting-on", "close", "pin"]);
  const deck = queue([entry("a", "ready")], [record("a", ["a"], { last_seen: ago(24 * 5) })])[0];
  assert.deepEqual(buildCard(deck).actions, ["close", "park", "still-needed"]);
  const form = queue([entry("a", "waiting")], [record("a", ["a"])])[0];
  assert.deepEqual(buildCard(form, { form: { tool: "t", questions: [] } }).actions, ["answer", "read", "not-now", "waiting-on", "close", "pin"]);
  const task = queue([entry("a", "waiting", { agent_sub_state: "act" })], [record("a", ["a"])])[0];
  assert.deepEqual(buildCard(task, { said: "Add the key." }).actions, ["reply", "done", "read", "not-now", "waiting-on", "close", "pin"]);
  const noRecord = queue([entry("a", "waiting")])[0];
  assert.deepEqual(buildCard(noRecord, { said: "Which?" }).actions, ["reply", "close"]);
});
