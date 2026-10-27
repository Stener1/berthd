import { type App, expect, mockOnly, test } from "./fixtures";

// Saved tabs that come back after a restart (#137): a Compare tab of two
// agents' chats, as the report had it, used to leave the window empty on
// every launch; and a pane that broke as it drew took the whole window.

const A = "devl:/home/me/work/shop-checkout-fix";
const B = "devl:/home/me/work/shop-search-perf";

const agentPane = (id: string, session: string, wt?: string) => ({ kind: "leaf", id, content: { kind: "terminal", box: "devl", session, agent: "claude", command: "claude", view: "conversation" }, ...(wt ? { wt } : {}) });

// What berth.workspaces held: the worktree's own agent in a tab, then a
// Compare tab of it and search-perf in the Chat lane with Sync on.
function saved(opts: { active?: "compare" | "agent"; compare?: unknown; extra?: Record<string, unknown> } = {}) {
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
          { id: "agent-tab", root: { kind: "leaf", id: "own", content: { kind: "terminal", box: "devl", session: "checkout-fix-claude", agent: "claude", command: "claude", view: "terminal" } }, focus: "own" },
          {
            id: "compare-tab",
            root: { kind: "split", id: "pair", dir: "row", ratio: 0.5, a: agentPane("left", "checkout-fix-claude"), b: agentPane("right", "search-perf-claude", B) },
            focus: "left",
            compare: opts.compare ?? { a: A, b: B, lane: "chat", sync: true, panes: { agent: ["left", "right"] }, parked: [] },
          },
        ],
        hidden: [],
        known: ["checkout-fix-claude", "svc-shop-checkout-fix-web"],
        visitedAt: 1,
        active: opts.active === "agent" ? "agent-tab" : "compare-tab",
      },
      ...opts.extra,
    },
  };
}

// openWith starts the app on a save, as a restart would.
async function openWith(app: App, ws: unknown, crash?: string) {
  await app.context.addInitScript((state) => {
    if (sessionStorage.getItem("e2e.seeded")) return;
    sessionStorage.setItem("e2e.seeded", "1");
    localStorage.setItem("berth.prefs", JSON.stringify({ whatsNewSeen: "999.0.0" }));
    localStorage.setItem("berth.workspaces", JSON.stringify(state));
  }, ws);
  await app.open({ params: { view: "conversation", ...(crash ? { crash } : {}) } });
}

const bar = (app: App) => app.page.getByRole("toolbar", { name: "Compare checkout-fix and search-perf" });
const compareTab = (app: App) => app.page.getByRole("tab", { name: "Compare checkout-fix ⇄ search-perf" });
const broken = (app: App) => app.page.getByRole("alert").filter({ hasText: "Something in this side of the Compare tab broke" });

test.beforeEach(() => mockOnly("seeds a save of the mock's worktrees"));

test("a saved Compare tab of two chats opens as it was, restart after restart", async ({ app }) => {
  await openWith(app, saved());
  for (let i = 0; i < 2; i++) {
    await expect(bar(app)).toBeVisible();
    await expect(bar(app).getByRole("button", { name: "Chat lane" })).toHaveAttribute("aria-pressed", "true");
    await expect(app.page.getByRole("region", { name: "Claude Code, search-perf" })).toBeVisible();
    await expect(app.page.getByRole("region", { name: "Claude Code, checkout-fix" }).first()).toBeVisible();
    await app.page.reload();
    await expect(app.page.getByTestId("nav-home")).toBeVisible();
  }
});

test("a saved Compare tab behind another tab opens, and is there to go back to", async ({ app }) => {
  await openWith(app, saved({ active: "agent" }));
  await expect(compareTab(app)).toBeVisible();
  await compareTab(app).click();
  await expect(bar(app)).toBeVisible();
});

test("a saved Compare tab that is malformed opens as what can be drawn", async ({ app }) => {
  // No worktrees named, a pane with nothing in it, and a workspace that is
  // no workspace: the panes left show as a plain tab, and the rest goes.
  const ws = saved({ compare: { lane: "bogus", sync: "yes", parked: "x" }, extra: { "devl:/nowhere": { tabs: null }, "devl:/nothing": null } });
  ws.spaces[A].tabs.push({ id: "empty-tab", root: { kind: "leaf", id: "void" }, focus: "void" } as never);
  await openWith(app, ws);
  await expect(app.page.locator("[data-tab=compare-tab]")).toBeVisible();
  await expect(bar(app)).toHaveCount(0);
  await expect(app.panes).toHaveCount(2);
  await expect(app.page.locator("[data-tab=empty-tab]")).toHaveCount(0);
});

test("a side that breaks says so in its place, and the rest of the window carries on", async ({ app }) => {
  await openWith(app, saved(), "search-perf-claude");
  await expect(broken(app)).toBeVisible();
  await expect(broken(app).getByRole("button", { name: "Close tab" })).toBeVisible();
  // The other side, the bar, the other tabs and the sidebar all still work.
  await expect(bar(app)).toBeVisible();
  await expect(app.page.locator("[data-compare-side='0'] [data-testid=chat]")).toBeVisible();
  await app.page.locator("[data-tab=agent-tab]").click();
  await expect(app.page.locator("[data-tab=agent-tab]")).toHaveAttribute("aria-selected", "true");
  await expect(broken(app)).toBeHidden();
  await app.openWorktree("devl/search-perf");
});

test("Show both terminals puts a broken Compare tab right", async ({ app }) => {
  await openWith(app, saved(), "search-perf-claude");
  await broken(app).getByRole("button", { name: "Show both terminals" }).click();
  await expect(bar(app).getByRole("button", { name: "Terminal lane" })).toHaveAttribute("aria-pressed", "true");
  await expect(broken(app)).toHaveCount(0);
  await expect(app.page.locator("[data-compare-side]:visible")).toHaveCount(2);
});

test("Close tab takes a broken Compare tab away, for good, and stops nothing", async ({ app }) => {
  await openWith(app, saved(), "search-perf-claude");
  await broken(app).getByRole("button", { name: "Close tab" }).click();
  await expect(compareTab(app)).toHaveCount(0);
  // The tab beside it is in front.
  await expect(app.page.locator("[role=tab][aria-selected=true]")).toHaveCount(1);
  await expect(app.panes.first()).toBeVisible();
  await expect(app.page.getByText(/Stopped/)).toHaveCount(0);
  await expect.poll(async () => JSON.stringify(await app.stored("berth.workspaces"))).not.toContain("compare-tab");
  await app.page.reload();
  await expect(app.page.getByTestId("nav-home")).toBeVisible();
  await expect(compareTab(app)).toHaveCount(0);
});
