// node --experimental-strip-types --test src/lib/restore.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { sides } from "./compare.ts";
import { leaves, type PaneContent } from "./layout.ts";
import { contentOf, PANE_KINDS, restoreSaved, restoreSpaces } from "./restore.ts";

const A = "devl:/home/me/work/shop-checkout-fix";
const B = "devl:/home/me/work/shop-search-perf";

// A Compare tab as #137's report had it: both agents' chats side by side,
// in the Chat lane, with Sync on.
function saved() {
  return {
    current: A,
    shown: [A],
    folded: [],
    tones: {},
    recentUrls: [],
    spaces: {
      [A]: {
        ref: { box: "devl", location: "shop", worktree: "checkout-fix", path: "/home/me/work/shop-checkout-fix" },
        tabs: [
          { id: "t1", root: { kind: "leaf", id: "p1", content: { kind: "terminal", box: "devl", session: "checkout-fix-claude", agent: "claude" } }, focus: "p1" },
          {
            id: "cmp",
            root: {
              kind: "split",
              id: "s1",
              dir: "row",
              ratio: 0.5,
              a: { kind: "leaf", id: "l", content: { kind: "terminal", box: "devl", session: "checkout-fix-claude", view: "conversation", agent: "codex" } },
              b: { kind: "leaf", id: "r", content: { kind: "terminal", box: "devl", session: "search-perf-claude", view: "conversation", agent: "omp" }, wt: B },
            },
            focus: "l",
            compare: { a: A, b: B, lane: "chat", sync: true, panes: { agent: ["l", "r"] }, parked: [] },
          },
        ],
        hidden: [],
        known: ["checkout-fix-claude"],
        visitedAt: 1,
        active: "cmp",
      },
    },
  } as Record<string, any>;
}

const tabs = (s: ReturnType<typeof restoreSaved>) => s.spaces[A]?.tabs ?? [];
const cmp = (s: Record<string, any>) => s.spaces[A].tabs[1];

test("a saved Compare tab comes back as it was", () => {
  const out = restoreSaved(saved());
  assert.equal(out.current, A);
  assert.deepEqual(out.shown, [A]);
  const t = tabs(out)[1];
  assert.equal(out.spaces[A].active, "cmp");
  assert.deepEqual(t.compare, { a: A, b: B, lane: "chat", sync: true, panes: { agent: ["l", "r"] }, parked: [] });
  const pair = sides(t);
  assert.ok(pair);
  assert.equal(pair[1].wt, B);
  assert.deepEqual(pair[0].content, { kind: "terminal", box: "devl", session: "checkout-fix-claude", agent: "codex", view: "conversation" });
  // Round trip: what is restored restores to itself.
  assert.deepEqual(restoreSaved(JSON.parse(JSON.stringify(out))), out);
});

test("a Compare tab missing its worktrees becomes a plain tab of its panes", () => {
  for (const compare of [{}, { lane: "chat", sync: true }, { a: A, b: A, lane: "chat" }, { a: 3, b: B }, "x", 7]) {
    const s = saved();
    cmp(s).compare = compare;
    const t = tabs(restoreSaved(s))[1];
    assert.equal(t.compare, undefined, JSON.stringify(compare));
    assert.deepEqual(leaves(t.root).map((l) => l.id), ["l", "r"]);
  }
});

test("a Compare tab that is no longer a pair of panes side by side becomes a plain tab", () => {
  const s = saved();
  cmp(s).root = cmp(s).root.a;
  const t = tabs(restoreSaved(s))[1];
  assert.equal(t.compare, undefined);
  assert.equal(t.root.kind, "leaf");
  assert.equal(t.focus, "l");
});

test("a Compare tab's fields of the wrong kind take their defaults", () => {
  const s = saved();
  Object.assign(cmp(s).compare, { lane: "bogus", sync: "yes", panes: { agent: "x", diff: ["d1"], preview: ["p1", "p2"], nope: ["a", "b"] }, parked: [null, 3, { kind: "leaf" }, { kind: "leaf", id: "k", content: { kind: "browser", url: "http://x/" } }], path: 5 });
  const c = tabs(restoreSaved(s))[1].compare;
  assert.deepEqual(c, { a: A, b: B, lane: "terminal", sync: true, panes: { preview: ["p1", "p2"] }, parked: [{ kind: "leaf", id: "k", content: { kind: "browser", url: "http://x/" } }] });
  const t = saved();
  cmp(t).compare.parked = "x";
  assert.deepEqual(tabs(restoreSaved(t))[1].compare?.parked, []);
});

test("a pane that can't be drawn goes, and its split's other side takes its place", () => {
  for (const bad of [undefined, null, { kind: "leaf", id: "r" }, { kind: "leaf", id: "r", content: { kind: "nope" } }, { kind: "leaf", id: "r", content: { kind: "terminal", box: "devl" } }, { kind: "leaf", content: { kind: "file", path: "a" } }]) {
    const s = saved();
    cmp(s).root.b = bad;
    const t = tabs(restoreSaved(s))[1];
    assert.equal(t.compare, undefined);
    assert.equal(t.root.kind, "leaf");
    assert.equal(t.root.id, "l");
  }
});

test("a tab with nothing left to draw goes, and the active tab moves to one that is there", () => {
  const s = saved();
  delete cmp(s).root;
  const out = restoreSaved(s);
  assert.deepEqual(tabs(out).map((t) => t.id), ["t1"]);
  assert.equal(out.spaces[A].active, "t1");
});

test("a pane's fields of the wrong kind are dropped, not kept", () => {
  assert.deepEqual(contentOf({ kind: "terminal", box: "b", session: "s", agent: 3, title: "T", view: "sideways", extra: 1 }), { kind: "terminal", box: "b", session: "s", title: "T" });
  assert.deepEqual(contentOf({ kind: "browser", url: "", watch: "x" }), { kind: "browser", url: "" });
  assert.deepEqual(contentOf({ kind: "panel", plugin: "diff", panel: "diff" }), { kind: "panel", plugin: "diff", panel: "diff", title: "" });
  assert.deepEqual(contentOf({ kind: "empty" }), { kind: "empty", label: "Agent" });
  assert.equal(contentOf({ kind: "log", box: "b" }), undefined);
  assert.equal(contentOf("terminal"), undefined);
});

test("a split's bad ratio or direction is put right", () => {
  const s = saved();
  Object.assign(cmp(s).root, { ratio: "x", dir: "diagonal" });
  const t = tabs(restoreSaved(s))[1];
  assert.equal(t.root.kind === "split" && t.root.ratio, 0.5);
  // Its direction is a row, so it is still the Compare tab it was.
  assert.ok(t.compare);
  const u = saved();
  Object.assign(cmp(u).root, { dir: "col" });
  assert.equal(tabs(restoreSaved(u))[1].compare, undefined);
});

test("panes that were starting go, but a Compare tab keeps its two sides", () => {
  const s = saved();
  cmp(s).root.b.content = { kind: "starting", label: "Codex" };
  s.spaces[A].tabs.push({ id: "t3", root: { kind: "split", id: "s3", dir: "row", ratio: 0.5, a: { kind: "leaf", id: "x1", content: { kind: "error", message: "no" } }, b: { kind: "leaf", id: "x2", content: { kind: "browser", url: "http://x/" } } }, focus: "x1" });
  const out = tabs(restoreSaved(s));
  assert.deepEqual(sides(out[1])?.[1].content, { kind: "empty", label: "Agent" });
  assert.equal(out[2].root.kind, "leaf");
  assert.equal(out[2].focus, "x2");
});

test("a pane id or tab id seen before is dropped: panes are drawn in one keyed list", () => {
  const s = saved();
  s.spaces[A].tabs.push({ id: "t1", root: { kind: "leaf", id: "q", content: { kind: "file", path: "a" } }, focus: "q" });
  s.spaces[B] = { ref: { box: "devl", path: "/home/me/work/shop-search-perf" }, tabs: [{ id: "u", root: { kind: "leaf", id: "p1", content: { kind: "file", path: "b" } }, focus: "p1" }] };
  const out = restoreSaved(s);
  assert.deepEqual(tabs(out).map((t) => t.id), ["t1", "cmp"]);
  assert.deepEqual(out.spaces[B].tabs, []);
  assert.equal(out.spaces[B].ref.worktree, "shop-search-perf");
});

test("a workspace that isn't one is dropped, and the others come back", () => {
  const s = saved();
  Object.assign(s.spaces, { "devl:/a": null, "devl:/b": { tabs: [] }, "devl:/c": { ref: { box: "devl", path: "/c" }, tabs: null, hidden: "x", known: [1, "k"] }, nokey: { ref: { box: "devl", path: "/d" }, tabs: [] } });
  const out = restoreSpaces(s.spaces);
  assert.deepEqual(Object.keys(out).sort(), [A, "devl:/c"].sort());
  assert.deepEqual(out["devl:/c"], { ref: { box: "devl", location: "", worktree: "c", path: "/c" }, tabs: [], active: undefined, hidden: [], known: ["k"] });
});

test("nothing saved, or something that is no save at all, opens on nothing", () => {
  for (const v of [undefined, null, 3, "x", [], { spaces: "x" }, { spaces: [1, 2] }]) {
    assert.deepEqual(restoreSaved(v), { current: undefined, shown: undefined, folded: [], tones: {}, spaces: {}, recentUrls: [] });
  }
  const s = restoreSaved({ current: "devl:/gone", shown: "x", folded: ["a", 1], tones: { a: "#f00", b: 2 }, recentUrls: [null, "http://x/"] });
  assert.deepEqual(s, { current: undefined, shown: undefined, folded: ["a"], tones: { a: "#f00" }, spaces: {}, recentUrls: ["http://x/"] });
});

test("every kind of pane comes back from a save as it was", () => {
  const full: { [K in PaneContent["kind"]]: Extract<PaneContent, { kind: K }> } = {
    terminal: { kind: "terminal", box: "devl", session: "s1", agent: "claude", command: "claude", title: "Fix it", view: "conversation" },
    browser: { kind: "browser", url: "http://localhost:3000/", watch: 5 },
    preview: { kind: "preview", url: "http://localhost:3000/" },
    log: { kind: "log", box: "devl", location: "shop", worktree: "fix", service: "web" },
    file: { kind: "file", path: "src/a.ts" },
    helper: { kind: "helper", box: "devl", session: "s1", helper: "h1", title: "Explore" },
    panel: { kind: "panel", plugin: "p", panel: "q", title: "Q" },
    artifact: { kind: "artifact", id: "a1", title: "Chart", focus: "f", art: "chart" },
    starting: { kind: "starting", label: "Claude" },
    empty: { kind: "empty", label: "Agent" },
    error: { kind: "error", message: "no" },
  };
  assert.deepEqual([...PANE_KINDS].sort(), Object.keys(full).sort());
  for (const c of Object.values(full)) assert.deepEqual(contentOf(JSON.parse(JSON.stringify(c))), c);
});
